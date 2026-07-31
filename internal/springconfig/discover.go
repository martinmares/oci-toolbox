package springconfig

import (
	"archive/tar"
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"gopkg.in/yaml.v3"

	"github.com/martinmares/oci-toolbox/internal/image"
)

const (
	DefaultClasspathFile = "/app/jib-classpath-file"
	DefaultMainClassFile = "/app/jib-main-class-file"
	maxConfigFileSize    = 16 << 20
	maxTotalConfigSize   = 256 << 20
	maxJarFileSize       = 2 << 30
)

type Options struct {
	ClasspathFile string
	MainClassFile string
	Include       []string
	Reporter      func(string)
}

type Manifest struct {
	Image     ImageInfo        `yaml:"image"`
	JIB       JIBInfo          `yaml:"jib"`
	Classpath []ClasspathEntry `yaml:"classpath"`
	Configs   []ConfigFile     `yaml:"configs"`
	Warnings  []string         `yaml:"warnings,omitempty"`
}

type ImageInfo struct {
	Reference string `yaml:"reference"`
	Digest    string `yaml:"digest"`
	Platform  string `yaml:"platform,omitempty"`
	Workdir   string `yaml:"workdir,omitempty"`
}

type JIBInfo struct {
	ClasspathFile string `yaml:"classpath_file"`
	MainClassFile string `yaml:"main_class_file"`
	MainClass     string `yaml:"main_class"`
}

type ClasspathEntry struct {
	Index       int    `yaml:"index"`
	Path        string `yaml:"path"`
	Kind        string `yaml:"kind"`
	Status      string `yaml:"status"`
	OrderSource string `yaml:"order_source,omitempty"`
}

type ConfigFile struct {
	ID             string `yaml:"id"`
	ClasspathIndex int    `yaml:"classpath_index"`
	ClasspathPath  string `yaml:"classpath_path"`
	Source         string `yaml:"source"`
	ArchiveEntry   string `yaml:"archive_entry,omitempty"`
	Output         string `yaml:"output"`
	NameKind       string `yaml:"name_kind"`
	ProfileHint    string `yaml:"profile_hint,omitempty"`
	Activation     string `yaml:"activation"`
	SHA256         string `yaml:"sha256"`
	Size           int64  `yaml:"size"`
}

type classpathSpec struct {
	Path         string
	Kind         string
	WildcardRoot string
}

type stagedJar struct {
	Path     string
	TempPath string
	Found    bool
}

type stagedDirectoryConfig struct {
	SpecIndex int
	Root      string
	Path      string
	TempPath  string
}

type discoveredConfig struct {
	ClasspathIndex int
	ClasspathPath  string
	Source         string
	ArchiveEntry   string
	RelativePath   string
	TempPath       string
	NameKind       string
	ProfileHint    string
	Size           int64
	SHA256         string
	Duplicate      int
}

type scanState struct {
	workDir      string
	specs        []classpathSpec
	exactJars    map[string]*stagedJar
	wildcardJars map[int]map[string]*stagedJar
	dirConfigs   map[string]*stagedDirectoryConfig
	dirPresent   map[int]bool
	include      []string
	totalExtract int64
	warnings     []string
	reporter     func(string)
}

func Discover(img v1.Image, imageReference, outputDir string, opts Options) (*Manifest, error) {
	if img == nil {
		return nil, errors.New("image is required")
	}
	if strings.TrimSpace(outputDir) == "" {
		return nil, errors.New("output directory is required")
	}
	opts = defaultOptions(opts)
	report(opts.Reporter, "reading JIB metadata")

	if _, err := os.Stat(outputDir); err == nil {
		return nil, fmt.Errorf("output directory already exists: %s", outputDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	classpathRaw, err := image.ReadFile(img, opts.ClasspathFile)
	if err != nil {
		return nil, fmt.Errorf("reading JIB classpath file %q: %w", opts.ClasspathFile, err)
	}
	mainClassRaw, err := image.ReadFile(img, opts.MainClassFile)
	if err != nil {
		return nil, fmt.Errorf("reading JIB main class file %q: %w", opts.MainClassFile, err)
	}
	configFile, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("reading image configuration: %w", err)
	}
	specs, err := parseClasspath(string(classpathRaw), configFile.Config.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("parsing JIB classpath file %q: %w", opts.ClasspathFile, err)
	}

	parent := filepath.Dir(outputDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("creating output parent directory: %w", err)
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(outputDir)+".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("creating temporary output directory: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil {
		return nil, err
	}

	workDir := filepath.Join(stage, ".work")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		return nil, err
	}
	state, err := newScanState(workDir, specs, opts.Include)
	if err != nil {
		return nil, err
	}
	state.reporter = opts.Reporter
	if err := state.scanImage(img); err != nil {
		return nil, err
	}

	classpath, expandedJars := state.expandedClasspath()
	report(opts.Reporter, "examining %d classpath entries", len(classpath))
	configs, err := state.collectConfigs(expandedJars)
	if err != nil {
		return nil, err
	}
	report(opts.Reporter, "writing %d discovered configuration files", len(configs))
	if err := writeDiscoveredConfigs(stage, configs); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(stage, "classpath.txt"), classpathRaw, 0o600); err != nil {
		return nil, fmt.Errorf("writing classpath.txt: %w", err)
	}
	if err := os.RemoveAll(workDir); err != nil {
		return nil, fmt.Errorf("removing temporary analysis data: %w", err)
	}

	digest, err := img.Digest()
	if err != nil {
		return nil, fmt.Errorf("reading image digest: %w", err)
	}
	platform := strings.Trim(strings.Join([]string{configFile.OS, configFile.Architecture}, "/"), "/")
	if configFile.Variant != "" {
		platform += "/" + configFile.Variant
	}
	manifest := &Manifest{
		Image: ImageInfo{
			Reference: imageReference,
			Digest:    digest.String(),
			Platform:  platform,
			Workdir:   configFile.Config.WorkingDir,
		},
		JIB: JIBInfo{
			ClasspathFile: opts.ClasspathFile,
			MainClassFile: opts.MainClassFile,
			MainClass:     strings.TrimSpace(string(mainClassRaw)),
		},
		Classpath: classpath,
		Configs:   manifestConfigs(configs),
		Warnings:  state.warnings,
	}
	report(opts.Reporter, "writing YAML manifest and SVG map")
	if err := writeManifest(filepath.Join(stage, "manifest.yml"), manifest); err != nil {
		return nil, err
	}
	if err := writeManifestSVG(filepath.Join(stage, "manifest.svg"), manifest); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, outputDir); err != nil {
		return nil, fmt.Errorf("publishing analysis output: %w", err)
	}
	report(opts.Reporter, "discovery complete")
	return manifest, nil
}

func report(reporter func(string), format string, args ...any) {
	if reporter != nil {
		reporter(fmt.Sprintf(format, args...))
	}
}

func defaultOptions(opts Options) Options {
	if strings.TrimSpace(opts.ClasspathFile) == "" {
		opts.ClasspathFile = DefaultClasspathFile
	}
	if strings.TrimSpace(opts.MainClassFile) == "" {
		opts.MainClassFile = DefaultMainClassFile
	}
	return opts
}

func parseClasspath(raw, workdir string) ([]classpathSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("classpath is empty")
	}
	parts := strings.Split(raw, ":")
	specs := make([]classpathSpec, 0, len(parts))
	for index, value := range parts {
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if value == "" {
			return nil, fmt.Errorf("classpath entry %d is empty", index)
		}
		if !strings.HasPrefix(value, "/") {
			base := workdir
			if base == "" {
				base = "/"
			}
			value = path.Join(base, value)
		}
		value = path.Clean(value)
		if value == "/" || strings.HasPrefix(value, "../") {
			return nil, fmt.Errorf("classpath entry %d has unsafe path %q", index, value)
		}
		spec := classpathSpec{Path: value, Kind: "directory"}
		if strings.HasSuffix(value, "/*") {
			spec.Kind = "wildcard"
			spec.WildcardRoot = strings.TrimSuffix(value, "/*")
		} else if strings.EqualFold(path.Ext(value), ".jar") || strings.EqualFold(path.Ext(value), ".zip") {
			spec.Kind = "jar"
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func newScanState(workDir string, specs []classpathSpec, include []string) (*scanState, error) {
	state := &scanState{
		workDir:      workDir,
		specs:        specs,
		exactJars:    map[string]*stagedJar{},
		wildcardJars: map[int]map[string]*stagedJar{},
		dirConfigs:   map[string]*stagedDirectoryConfig{},
		dirPresent:   map[int]bool{},
		include:      append([]string(nil), include...),
	}
	for index, spec := range specs {
		switch spec.Kind {
		case "jar":
			if _, ok := state.exactJars[spec.Path]; !ok {
				state.exactJars[spec.Path] = &stagedJar{
					Path:     spec.Path,
					TempPath: filepath.Join(workDir, fmt.Sprintf("jar-%x", sha256.Sum256([]byte(spec.Path)))),
				}
			}
		case "wildcard":
			state.wildcardJars[index] = map[string]*stagedJar{}
		}
	}
	for _, pattern := range include {
		if _, err := path.Match(pattern, "application.yml"); err != nil {
			return nil, fmt.Errorf("invalid include pattern %q: %w", pattern, err)
		}
	}
	return state, nil
}

func (s *scanState) scanImage(img v1.Image) error {
	layers, err := img.Layers()
	if err != nil {
		return err
	}
	for layerIndex, layer := range layers {
		report(s.reporter, "scanning image layer %d/%d", layerIndex+1, len(layers))
		rc, err := layer.Uncompressed()
		if err != nil {
			return fmt.Errorf("opening image layer %d: %w", layerIndex+1, err)
		}
		if err := s.scanLayer(tar.NewReader(rc)); err != nil {
			rc.Close()
			return fmt.Errorf("scanning image layer %d: %w", layerIndex+1, err)
		}
		if err := rc.Close(); err != nil {
			return fmt.Errorf("closing image layer %d: %w", layerIndex+1, err)
		}
	}
	return nil
}

func (s *scanState) scanLayer(tr *tar.Reader) error {
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := cleanArchivePath(header.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		dir, base := path.Dir(name), path.Base(name)
		if base == ".wh..wh..opq" {
			opaqueDir := "/" + strings.Trim(dir, "/")
			s.removeHidden(opaqueDir)
			s.markDirectoriesPresent(opaqueDir)
			continue
		}
		if strings.HasPrefix(base, ".wh.") {
			s.removeHidden("/" + path.Join(dir, strings.TrimPrefix(base, ".wh.")))
			continue
		}

		absolute := "/" + name
		s.markDirectoriesPresent(absolute)
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && header.Typeflag != 0 {
			s.removeExact(absolute)
			continue
		}

		jarTargets := s.jarTargets(absolute)
		configTargets := s.directoryConfigTargets(absolute)
		if len(jarTargets) == 0 && len(configTargets) == 0 {
			continue
		}
		if len(jarTargets) > 0 {
			if len(configTargets) > 0 {
				return fmt.Errorf("path %q matched both a classpath archive and a configuration file", absolute)
			}
			for _, target := range jarTargets {
				if header.Size > maxJarFileSize {
					return fmt.Errorf("classpath archive %q exceeds %d bytes", absolute, maxJarFileSize)
				}
				if err := writeLimitedFile(target.TempPath, tr, header.Size, maxJarFileSize); err != nil {
					return fmt.Errorf("staging classpath archive %q: %w", absolute, err)
				}
				target.Found = true
			}
			continue
		}

		if header.Size > maxConfigFileSize {
			return fmt.Errorf("configuration file %q exceeds %d bytes", absolute, maxConfigFileSize)
		}
		data, err := readLimited(tr, maxConfigFileSize)
		if err != nil {
			return fmt.Errorf("reading configuration file %q: %w", absolute, err)
		}
		s.totalExtract += int64(len(data)) * int64(len(configTargets))
		if s.totalExtract > maxTotalConfigSize {
			return fmt.Errorf("extracted configuration data exceeds %d bytes", maxTotalConfigSize)
		}
		for _, target := range configTargets {
			if err := os.WriteFile(target.TempPath, data, 0o600); err != nil {
				return err
			}
			s.dirConfigs[dirTargetKey(target.SpecIndex, target.Path)] = target
		}
	}
}

func (s *scanState) jarTargets(absolute string) []*stagedJar {
	var targets []*stagedJar
	if target, ok := s.exactJars[absolute]; ok {
		targets = append(targets, target)
	}
	for index, spec := range s.specs {
		if spec.Kind != "wildcard" || path.Dir(absolute) != spec.WildcardRoot {
			continue
		}
		ext := strings.ToLower(path.Ext(absolute))
		if ext != ".jar" && ext != ".zip" {
			continue
		}
		target, ok := s.wildcardJars[index][absolute]
		if !ok {
			target = s.stagedJarForPath(absolute)
			if target == nil {
				target = &stagedJar{
					Path:     absolute,
					TempPath: filepath.Join(s.workDir, fmt.Sprintf("wildcard-%04d-%x", index, sha256.Sum256([]byte(absolute)))),
				}
			}
			s.wildcardJars[index][absolute] = target
		}
		targets = append(targets, target)
	}
	return uniqueJarTargets(targets)
}

func (s *scanState) stagedJarForPath(archivePath string) *stagedJar {
	if target := s.exactJars[archivePath]; target != nil {
		return target
	}
	for _, targets := range s.wildcardJars {
		if target := targets[archivePath]; target != nil {
			return target
		}
	}
	return nil
}

func uniqueJarTargets(input []*stagedJar) []*stagedJar {
	seen := map[*stagedJar]struct{}{}
	out := make([]*stagedJar, 0, len(input))
	for _, item := range input {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func (s *scanState) directoryConfigTargets(absolute string) []*stagedDirectoryConfig {
	var targets []*stagedDirectoryConfig
	for index, spec := range s.specs {
		if spec.Kind != "directory" || !isWithin(absolute, spec.Path) {
			continue
		}
		relative := strings.TrimPrefix(absolute, strings.TrimSuffix(spec.Path, "/")+"/")
		if !isConfigCandidate(relative, s.include) {
			continue
		}
		key := dirTargetKey(index, absolute)
		target := &stagedDirectoryConfig{
			SpecIndex: index,
			Root:      spec.Path,
			Path:      absolute,
			TempPath:  filepath.Join(s.workDir, fmt.Sprintf("config-%x", sha256.Sum256([]byte(key)))),
		}
		targets = append(targets, target)
	}
	return targets
}

func (s *scanState) markDirectoriesPresent(absolute string) {
	for index, spec := range s.specs {
		if spec.Kind == "directory" && isWithin(absolute, spec.Path) {
			s.dirPresent[index] = true
		}
	}
}

func (s *scanState) removeHidden(hidden string) {
	for _, target := range s.exactJars {
		if pathHiddenBy(hidden, target.Path) {
			_ = os.Remove(target.TempPath)
			target.Found = false
		}
	}
	for _, targets := range s.wildcardJars {
		for archivePath, target := range targets {
			if pathHiddenBy(hidden, archivePath) {
				_ = os.Remove(target.TempPath)
				delete(targets, archivePath)
			}
		}
	}
	for key, target := range s.dirConfigs {
		if pathHiddenBy(hidden, target.Path) {
			_ = os.Remove(target.TempPath)
			delete(s.dirConfigs, key)
		}
	}
	for index, spec := range s.specs {
		if spec.Kind == "directory" && pathHiddenBy(hidden, spec.Path) {
			s.dirPresent[index] = false
		}
	}
}

func (s *scanState) removeExact(targetPath string) {
	if target, ok := s.exactJars[targetPath]; ok {
		_ = os.Remove(target.TempPath)
		target.Found = false
	}
	for _, targets := range s.wildcardJars {
		if target, ok := targets[targetPath]; ok {
			_ = os.Remove(target.TempPath)
			delete(targets, targetPath)
		}
	}
	for key, target := range s.dirConfigs {
		if target.Path == targetPath {
			_ = os.Remove(target.TempPath)
			delete(s.dirConfigs, key)
		}
	}
}

func pathHiddenBy(hidden, candidate string) bool {
	hidden = path.Clean(hidden)
	candidate = path.Clean(candidate)
	return candidate == hidden || strings.HasPrefix(candidate, strings.TrimSuffix(hidden, "/")+"/")
}

func isWithin(candidate, root string) bool {
	candidate = path.Clean(candidate)
	root = path.Clean(root)
	return candidate == root || strings.HasPrefix(candidate, strings.TrimSuffix(root, "/")+"/")
}

func (s *scanState) expandedClasspath() ([]ClasspathEntry, map[int]*stagedJar) {
	var entries []ClasspathEntry
	jars := map[int]*stagedJar{}
	for specIndex, spec := range s.specs {
		switch spec.Kind {
		case "directory":
			status := "found"
			if !s.dirPresent[specIndex] {
				status = "missing"
				s.warnings = append(s.warnings, fmt.Sprintf("classpath directory not found: %s", spec.Path))
			}
			entries = append(entries, ClasspathEntry{
				Index:  len(entries),
				Path:   spec.Path,
				Kind:   "directory",
				Status: status,
			})
		case "jar":
			target := s.exactJars[spec.Path]
			status := "found"
			if target == nil || !target.Found {
				status = "missing"
				s.warnings = append(s.warnings, fmt.Sprintf("classpath archive not found: %s", spec.Path))
			}
			index := len(entries)
			entries = append(entries, ClasspathEntry{
				Index:  index,
				Path:   spec.Path,
				Kind:   "jar",
				Status: status,
			})
			if target != nil && target.Found {
				jars[index] = target
			}
		case "wildcard":
			paths := make([]string, 0, len(s.wildcardJars[specIndex]))
			for archivePath := range s.wildcardJars[specIndex] {
				paths = append(paths, archivePath)
			}
			sort.Strings(paths)
			if len(paths) == 0 {
				entries = append(entries, ClasspathEntry{
					Index:       len(entries),
					Path:        spec.Path,
					Kind:        "wildcard",
					Status:      "missing",
					OrderSource: "lexical",
				})
				s.warnings = append(s.warnings, fmt.Sprintf("classpath wildcard matched no archives: %s", spec.Path))
				continue
			}
			s.warnings = append(s.warnings, fmt.Sprintf("classpath wildcard %s was expanded in lexical order", spec.Path))
			for _, archivePath := range paths {
				index := len(entries)
				entries = append(entries, ClasspathEntry{
					Index:       index,
					Path:        archivePath,
					Kind:        "jar",
					Status:      "found",
					OrderSource: "wildcard-lexical",
				})
				jars[index] = s.wildcardJars[specIndex][archivePath]
			}
		}
	}
	return entries, jars
}

func (s *scanState) collectConfigs(expandedJars map[int]*stagedJar) ([]discoveredConfig, error) {
	var configs []discoveredConfig
	classpathIndexBySpec := make(map[int]int)
	expandedIndex := 0
	for specIndex, spec := range s.specs {
		if spec.Kind == "wildcard" {
			count := len(s.wildcardJars[specIndex])
			if count == 0 {
				count = 1
			}
			expandedIndex += count
			continue
		}
		classpathIndexBySpec[specIndex] = expandedIndex
		expandedIndex++
	}
	for _, staged := range s.dirConfigs {
		kind, profile := classifyConfigName(path.Base(staged.Path))
		hash, size, err := fileDigest(staged.TempPath)
		if err != nil {
			return nil, err
		}
		relative := strings.TrimPrefix(staged.Path, strings.TrimSuffix(staged.Root, "/")+"/")
		configs = append(configs, discoveredConfig{
			ClasspathIndex: classpathIndexBySpec[staged.SpecIndex],
			ClasspathPath:  staged.Root,
			Source:         staged.Path,
			RelativePath:   relative,
			TempPath:       staged.TempPath,
			NameKind:       kind,
			ProfileHint:    profile,
			Size:           size,
			SHA256:         hash,
		})
	}

	jarIndexes := make([]int, 0, len(expandedJars))
	for index := range expandedJars {
		jarIndexes = append(jarIndexes, index)
	}
	sort.Ints(jarIndexes)
	for position, index := range jarIndexes {
		if position == 0 || (position+1)%25 == 0 || position+1 == len(jarIndexes) {
			report(s.reporter, "scanning classpath archive %d/%d", position+1, len(jarIndexes))
		}
		archiveConfigs, err := s.scanJar(index, expandedJars[index])
		if err != nil {
			return nil, err
		}
		configs = append(configs, archiveConfigs...)
	}
	sort.SliceStable(configs, func(i, j int) bool {
		if configs[i].ClasspathIndex != configs[j].ClasspathIndex {
			return configs[i].ClasspathIndex < configs[j].ClasspathIndex
		}
		if configs[i].RelativePath != configs[j].RelativePath {
			return configs[i].RelativePath < configs[j].RelativePath
		}
		return configs[i].Duplicate < configs[j].Duplicate
	})
	return configs, nil
}

func (s *scanState) scanJar(classpathIndex int, archive *stagedJar) ([]discoveredConfig, error) {
	file, err := os.Open(archive.TempPath)
	if err != nil {
		return nil, fmt.Errorf("opening classpath archive %q: %w", archive.Path, err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(file, stat.Size())
	if err != nil {
		return nil, fmt.Errorf("opening classpath archive %q: %w", archive.Path, err)
	}
	var configs []discoveredConfig
	duplicates := map[string]int{}
	for entryIndex, entry := range reader.File {
		entryPath, err := cleanArchivePath(entry.Name)
		if err != nil {
			return nil, fmt.Errorf("unsafe entry in classpath archive %q: %w", archive.Path, err)
		}
		if entryPath == "" || entry.FileInfo().IsDir() || !isConfigCandidate(entryPath, s.include) {
			continue
		}
		if entry.UncompressedSize64 > maxConfigFileSize {
			return nil, fmt.Errorf(
				"configuration entry %q in %q exceeds %d bytes",
				entryPath,
				archive.Path,
				maxConfigFileSize,
			)
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("opening %q in %q: %w", entryPath, archive.Path, err)
		}
		data, readErr := readLimited(rc, maxConfigFileSize)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("reading %q in %q: %w", entryPath, archive.Path, readErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		s.totalExtract += int64(len(data))
		if s.totalExtract > maxTotalConfigSize {
			return nil, fmt.Errorf("extracted configuration data exceeds %d bytes", maxTotalConfigSize)
		}
		tempPath := filepath.Join(
			s.workDir,
			fmt.Sprintf("jar-config-%04d-%06d", classpathIndex, entryIndex),
		)
		if err := os.WriteFile(tempPath, data, 0o600); err != nil {
			return nil, err
		}
		kind, profile := classifyConfigName(path.Base(entryPath))
		duplicate := duplicates[entryPath]
		duplicates[entryPath]++
		sum := sha256.Sum256(data)
		configs = append(configs, discoveredConfig{
			ClasspathIndex: classpathIndex,
			ClasspathPath:  archive.Path,
			Source:         archive.Path + "!/" + entryPath,
			ArchiveEntry:   entryPath,
			RelativePath:   entryPath,
			TempPath:       tempPath,
			NameKind:       kind,
			ProfileHint:    profile,
			Size:           int64(len(data)),
			SHA256:         hex.EncodeToString(sum[:]),
			Duplicate:      duplicate,
		})
	}
	return configs, nil
}

func writeDiscoveredConfigs(outputDir string, configs []discoveredConfig) error {
	used := map[string]int{}
	for index := range configs {
		config := &configs[index]
		group := fmt.Sprintf(
			"%04d-%s",
			config.ClasspathIndex,
			sanitizeName(path.Base(strings.TrimSuffix(config.ClasspathPath, "/"))),
		)
		relative, err := safeRelativePath(config.RelativePath)
		if err != nil {
			return fmt.Errorf("unsafe configuration output path for %q: %w", config.Source, err)
		}
		targetRelative := path.Join("sources", group, relative)
		used[targetRelative]++
		if used[targetRelative] > 1 {
			ext := path.Ext(targetRelative)
			targetRelative = strings.TrimSuffix(targetRelative, ext) +
				fmt.Sprintf(".duplicate-%d", used[targetRelative]-1) + ext
		}
		target := filepath.Join(outputDir, filepath.FromSlash(targetRelative))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := copyFile(config.TempPath, target, 0o600); err != nil {
			return err
		}
		config.RelativePath = targetRelative
	}
	return nil
}

func manifestConfigs(configs []discoveredConfig) []ConfigFile {
	result := make([]ConfigFile, 0, len(configs))
	for index, config := range configs {
		result = append(result, ConfigFile{
			ID:             fmt.Sprintf("config-%04d", index+1),
			ClasspathIndex: config.ClasspathIndex,
			ClasspathPath:  config.ClasspathPath,
			Source:         config.Source,
			ArchiveEntry:   config.ArchiveEntry,
			Output:         config.RelativePath,
			NameKind:       config.NameKind,
			ProfileHint:    config.ProfileHint,
			Activation:     "unknown",
			SHA256:         config.SHA256,
			Size:           config.Size,
		})
	}
	return result
}

func writeManifest(target string, manifest *Manifest) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("creating analysis manifest: %w", err)
	}
	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)
	encodeErr := encoder.Encode(manifest)
	closeEncoderErr := encoder.Close()
	closeFileErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("encoding analysis manifest: %w", encodeErr)
	}
	if closeEncoderErr != nil {
		return closeEncoderErr
	}
	return closeFileErr
}

func isConfigCandidate(name string, includes []string) bool {
	base := strings.ToLower(path.Base(name))
	kind, _ := classifyConfigName(base)
	if kind != "other" {
		return true
	}
	extension := strings.ToLower(path.Ext(base))
	if path.Dir(name) == "." &&
		(extension == ".yml" || extension == ".yaml" || extension == ".properties") {
		return true
	}
	for _, pattern := range includes {
		if matched, _ := path.Match(pattern, name); matched {
			return true
		}
		if matched, _ := path.Match(pattern, path.Base(name)); matched {
			return true
		}
	}
	return false
}

func classifyConfigName(name string) (string, string) {
	lower := strings.ToLower(name)
	extension := strings.ToLower(path.Ext(lower))
	if extension != ".yml" && extension != ".yaml" && extension != ".properties" {
		return "other", ""
	}
	stem := strings.TrimSuffix(lower, extension)
	switch {
	case stem == "application":
		return "application", ""
	case strings.HasPrefix(stem, "application-") && len(stem) > len("application-"):
		return "profile-named", strings.TrimPrefix(stem, "application-")
	case stem == "bootstrap":
		return "bootstrap", ""
	case strings.HasPrefix(stem, "bootstrap-") && len(stem) > len("bootstrap-"):
		return "bootstrap-profile-named", strings.TrimPrefix(stem, "bootstrap-")
	default:
		return "other", ""
	}
}

func cleanArchivePath(value string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	clean := path.Clean(value)
	if clean == "." || clean == "" {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes archive root: %q", value)
	}
	return clean, nil
}

func safeRelativePath(value string) (string, error) {
	clean, err := cleanArchivePath(value)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return "", errors.New("path is empty")
	}
	native := filepath.FromSlash(clean)
	if filepath.IsAbs(native) || filepath.VolumeName(native) != "" {
		return "", fmt.Errorf("path has an absolute volume: %q", value)
	}
	return clean, nil
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('-')
		}
	}
	result := strings.Trim(builder.String(), "-.")
	if result == "" {
		return "classpath"
	}
	if len(result) > 96 {
		result = result[:96]
	}
	return result
}

func dirTargetKey(index int, filePath string) string {
	return fmt.Sprintf("%d:%s", index, filePath)
}

func writeLimitedFile(target string, reader io.Reader, expected, maximum int64) error {
	if expected < 0 || expected > maximum {
		return fmt.Errorf("invalid file size %d", expected)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.CopyN(file, reader, expected)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != expected {
		return fmt.Errorf("wrote %d bytes, expected %d", written, expected)
	}
	return nil
}

func readLimited(reader io.Reader, maximum int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maximum + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("content exceeds %d bytes", maximum)
	}
	return data, nil
}

func fileDigest(filePath string) (string, int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func copyFile(source, target string, mode os.FileMode) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
