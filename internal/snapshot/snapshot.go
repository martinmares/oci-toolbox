package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"gopkg.in/yaml.v3"
)

const ManifestVersion = 1

type ImageLister interface {
	ListImages(context.Context, string) ([]string, error)
}

type CopyOptions struct {
	SrcCreds     string
	DestCreds    string
	SrcInsecure  bool
	DestInsecure bool
	Progress     bool
}

type Copier interface {
	Copy(context.Context, string, string, CopyOptions) error
}

type Manifest struct {
	Version   int             `yaml:"version"`
	Namespace string          `yaml:"namespace"`
	Filter    string          `yaml:"filter,omitempty"`
	CreatedAt time.Time       `yaml:"created_at"`
	Complete  bool            `yaml:"complete"`
	Images    []ManifestImage `yaml:"images"`
}

type ManifestImage struct {
	Archive     string `yaml:"archive"`
	Source      string `yaml:"source"`
	Destination string `yaml:"destination"`
	SHA256      string `yaml:"sha256"`
	Size        int64  `yaml:"size"`
}

type BackupOptions struct {
	Namespace    string
	OutputDir    string
	ImagePattern string
	SrcCreds     string
	SrcInsecure  bool
	Now          func() time.Time
	Stdout       io.Writer
	Stderr       io.Writer
}

type RestoreOptions struct {
	ManifestPath string
	Namespace    string
	DestCreds    string
	DestInsecure bool
	Stdout       io.Writer
	Stderr       io.Writer
}

func Backup(ctx context.Context, lister ImageLister, copier Copier, opts BackupOptions) (string, error) {
	if opts.Namespace == "" {
		return "", fmt.Errorf("namespace is required")
	}
	applyDefaults(&opts)
	images, err := lister.ListImages(ctx, opts.Namespace)
	if err != nil {
		return "", fmt.Errorf("list deployment images in namespace %q: %w", opts.Namespace, err)
	}
	images = uniqueSorted(images)
	if opts.ImagePattern != "" {
		images, err = filterImages(images, opts.ImagePattern)
		if err != nil {
			return "", err
		}
	}
	if len(images) == 0 {
		if opts.ImagePattern != "" {
			return "", fmt.Errorf("no deployment images in namespace %q match %q", opts.Namespace, opts.ImagePattern)
		}
		return "", fmt.Errorf("no images found in deployments in namespace %q", opts.Namespace)
	}

	now := opts.Now()
	timestamp := now.Format("20060102-150405")
	namespaceDir := filepath.Join(opts.OutputDir, opts.Namespace)
	snapshotDir := filepath.Join(namespaceDir, timestamp)
	manifestPath := filepath.Join(namespaceDir, timestamp+".yml")
	if err := os.MkdirAll(namespaceDir, 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(snapshotDir); !os.IsNotExist(err) {
		return "", fmt.Errorf("snapshot path already exists: %s", snapshotDir)
	}
	if err := os.Mkdir(snapshotDir, 0o755); err != nil {
		return "", err
	}

	manifest := Manifest{
		Version: ManifestVersion, Namespace: opts.Namespace, Filter: opts.ImagePattern,
		CreatedAt: now, Images: []ManifestImage{},
	}
	if err := writeManifest(manifestPath, manifest); err != nil {
		return "", err
	}
	usedNames := map[string]bool{}
	for _, source := range images {
		archive := archiveName(source, usedNames)
		usedNames[archive] = true
		archivePath := filepath.Join(snapshotDir, archive)
		partPath := filepath.Join(snapshotDir, "."+archive+".part")
		fmt.Fprintf(opts.Stdout, "Backing up docker://%s -> file://%s\n", source, archivePath)
		if err := copier.Copy(ctx, "docker://"+source, "file://"+partPath, CopyOptions{
			SrcCreds: opts.SrcCreds, SrcInsecure: opts.SrcInsecure, Progress: true,
		}); err != nil {
			_ = os.Remove(partPath)
			return manifestPath, fmt.Errorf("copying backup image %q: %w", source, err)
		}
		if err := os.Rename(partPath, archivePath); err != nil {
			return manifestPath, err
		}
		digest, size, err := fileMetadata(archivePath)
		if err != nil {
			return manifestPath, err
		}
		manifest.Images = append(manifest.Images, ManifestImage{
			Archive: filepath.ToSlash(filepath.Join(timestamp, archive)), Source: source,
			Destination: restorableDestination(source), SHA256: digest, Size: size,
		})
		if err := writeManifest(manifestPath, manifest); err != nil {
			return manifestPath, err
		}
	}
	manifest.Complete = true
	if err := writeManifest(manifestPath, manifest); err != nil {
		return manifestPath, err
	}
	return manifestPath, nil
}

func Restore(ctx context.Context, copier Copier, opts RestoreOptions) error {
	if opts.ManifestPath == "" {
		return fmt.Errorf("manifest path is required")
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	raw, err := os.ReadFile(opts.ManifestPath)
	if err != nil {
		return err
	}
	var manifest Manifest
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.Version != ManifestVersion {
		return fmt.Errorf("unsupported manifest version %d", manifest.Version)
	}
	if opts.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if manifest.Namespace != opts.Namespace {
		return fmt.Errorf("manifest namespace %q does not match --namespace %q", manifest.Namespace, opts.Namespace)
	}
	if !manifest.Complete {
		return fmt.Errorf("backup manifest is incomplete; refusing restore")
	}
	if len(manifest.Images) == 0 {
		return fmt.Errorf("backup manifest contains no images")
	}
	baseDir, err := filepath.Abs(filepath.Dir(opts.ManifestPath))
	if err != nil {
		return err
	}
	for _, image := range manifest.Images {
		archivePath, err := safeArchivePath(baseDir, image.Archive)
		if err != nil {
			return err
		}
		digest, size, err := fileMetadata(archivePath)
		if err != nil {
			return err
		}
		if size != image.Size || digest != image.SHA256 {
			return fmt.Errorf("archive integrity check failed: %s", archivePath)
		}
		fmt.Fprintf(opts.Stdout, "Restoring file://%s -> docker://%s\n", archivePath, image.Destination)
		if err := copier.Copy(ctx, "file://"+archivePath, "docker://"+image.Destination, CopyOptions{
			DestCreds: opts.DestCreds, DestInsecure: opts.DestInsecure, Progress: true,
		}); err != nil {
			return fmt.Errorf("restoring image %q: %w", image.Destination, err)
		}
	}
	return nil
}

func applyDefaults(opts *BackupOptions) {
	if opts.OutputDir == "" {
		opts.OutputDir = "."
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
}

func uniqueSorted(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func filterImages(images []string, pattern string) ([]string, error) {
	matcher, err := imageGlobRegexp(pattern)
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(images))
	for _, image := range images {
		if matcher.MatchString(image) {
			filtered = append(filtered, image)
		}
	}
	return filtered, nil
}

func imageGlobRegexp(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, fmt.Errorf("image glob must not be empty")
	}
	var expression strings.Builder
	expression.WriteByte('^')
	for _, char := range pattern {
		switch char {
		case '*':
			expression.WriteString(".*")
		case '?':
			expression.WriteByte('.')
		default:
			expression.WriteString(regexp.QuoteMeta(string(char)))
		}
	}
	expression.WriteByte('$')
	matcher, err := regexp.Compile(expression.String())
	if err != nil {
		return nil, fmt.Errorf("invalid image glob %q: %w", pattern, err)
	}
	return matcher, nil
}

var unsafeFilename = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func archiveName(ref string, used map[string]bool) string {
	last := ref[strings.LastIndex(ref, "/")+1:]
	if at := strings.IndexByte(last, '@'); at >= 0 {
		last = last[:at]
	}
	if colon := strings.LastIndex(last, ":"); colon >= 0 {
		last = last[:colon]
	}
	name := unsafeFilename.ReplaceAllString(last, "_") + ".tgz"
	if !used[name] {
		return name
	}
	sum := sha256.Sum256([]byte(ref))
	return strings.TrimSuffix(name, ".tgz") + "-" + hex.EncodeToString(sum[:4]) + ".tgz"
}

func restorableDestination(source string) string {
	ref, err := name.ParseReference(source, name.WeakValidation)
	if err != nil {
		return source
	}
	digest, ok := ref.(name.Digest)
	if !ok {
		return source
	}
	value := strings.TrimPrefix(digest.DigestStr(), "sha256:")
	if len(value) > 12 {
		value = value[:12]
	}
	return digest.Context().Tag("snapshot-" + value).String()
}

func writeManifest(path string, manifest Manifest) error {
	raw, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func fileMetadata(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

func safeArchivePath(baseDir, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("unsafe archive path %q", relative)
	}
	path := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(relative)))
	rel, err := filepath.Rel(baseDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", relative)
	}
	return path, nil
}
