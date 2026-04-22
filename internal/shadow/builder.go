// Package shadow implements the oci-patch shadow action:
// it packs a path inside an image into a tar.gz archive, encrypts the whole
// payload as a single opaque AES-256-GCM blob, then produces a self-extracting
// static binary (app.self) that unpacks the content at runtime inside the
// container.
package shadow

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/datalite/oci-patch/internal/image"
)

const (
	magicStr         = "OCIPATCH"
	passwordLen      = 64 // hex(32 random bytes)
	checksumFileName = ".app.sha256"
	payloadPrefix    = "GCM1"
)

// extractorSrcDir is the path to the extractor source relative to the module root.
// Resolved at build time via runtime.Caller.
var extractorSrcDir string

func init() {
	_, file, _, _ := runtime.Caller(0)
	extractorSrcDir = filepath.Join(filepath.Dir(file), "extractor")
}

// Apply reads shadowPath from srcImg, builds app.self, removes the original
// path from dstImg, and injects the bootstrap binary at outputPath.
func Apply(dstImg, srcImg v1.Image, shadowPath, extractTo, outputPath string) (v1.Image, error) {
	files, err := collectFromImage(srcImg, shadowPath)
	if err != nil {
		return nil, fmt.Errorf("collecting %q from image: %w", shadowPath, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("shadow-path %q not found in image", shadowPath)
	}

	password, err := generatePassword()
	if err != nil {
		return nil, fmt.Errorf("generating password: %w", err)
	}

	payloadData, err := buildEncryptedPayload(files, password)
	if err != nil {
		return nil, fmt.Errorf("building encrypted payload: %w", err)
	}

	goos, goarch, err := detectPlatform(dstImg)
	if err != nil {
		return nil, fmt.Errorf("detecting platform: %w", err)
	}

	extractorBin, err := compileExtractor(goos, goarch)
	if err != nil {
		return nil, fmt.Errorf("compiling extractor for %s/%s: %w", goos, goarch, err)
	}

	selfBin := appendPayload(extractorBin, payloadData, password, extractTo)
	return replaceWithBootstrap(dstImg, shadowPath, outputPath, selfBin, 0o755)
}

type fileEntry struct {
	name     string
	data     []byte
	mode     os.FileMode
	isDir    bool
	typeflag byte
	linkname string
}

func collectFromImage(img v1.Image, targetPath string) ([]fileEntry, error) {
	entries, err := image.CollectTree(img, targetPath)
	if err != nil {
		return nil, err
	}

	targetPath = "/" + strings.Trim(strings.TrimPrefix(targetPath, "/"), "/")
	var result []fileEntry
	for _, entry := range entries {
		relName := strings.TrimPrefix(entry.Path, targetPath)
		relName = strings.TrimPrefix(relName, "/")
		if relName == "" {
			if entry.IsDir {
				continue
			}
			relName = path.Base(entry.Path)
		}
		result = append(result, fileEntry{
			name:     relName,
			data:     entry.Data,
			mode:     entry.Mode,
			isDir:    entry.IsDir,
			typeflag: entry.Typeflag,
			linkname: entry.Linkname,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if rank := archiveOrder(result[i].typeflag) - archiveOrder(result[j].typeflag); rank != 0 {
			return rank < 0
		}
		return result[i].name < result[j].name
	})
	return result, nil
}

func generatePassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func buildEncryptedPayload(files []fileEntry, password string) ([]byte, error) {
	archiveData, err := buildArchive(files)
	if err != nil {
		return nil, err
	}
	return encryptPayload(archiveData, password)
}

func buildArchive(files []fileEntry) ([]byte, error) {
	buf := &bytes.Buffer{}
	gzw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gzw)
	manifest := buildChecksumManifest(files)

	for _, f := range files {
		hdr := &tar.Header{
			Name:     f.name,
			Mode:     int64(f.mode.Perm()),
			Typeflag: f.typeflag,
			Linkname: f.linkname,
		}
		if hdr.Typeflag == 0 {
			if f.isDir {
				hdr.Typeflag = tar.TypeDir
			} else {
				hdr.Typeflag = tar.TypeReg
			}
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if !strings.HasSuffix(hdr.Name, "/") {
				hdr.Name += "/"
			}
		case tar.TypeReg, tar.TypeRegA:
			hdr.Size = int64(len(f.data))
		case tar.TypeSymlink, tar.TypeLink:
			hdr.Size = 0
		default:
			return nil, fmt.Errorf("unsupported shadow entry type %q for %s", hdr.Typeflag, f.name)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("writing header for %s: %w", f.name, err)
		}
		if hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA {
			if _, err := tw.Write(f.data); err != nil {
				return nil, fmt.Errorf("writing %s: %w", f.name, err)
			}
		}
	}

	if len(manifest) > 0 {
		manifestHeader := &tar.Header{
			Name:     checksumFileName,
			Mode:     0o600,
			Size:     int64(len(manifest)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(manifestHeader); err != nil {
			return nil, fmt.Errorf("creating checksum manifest: %w", err)
		}
		if _, err := tw.Write(manifest); err != nil {
			return nil, fmt.Errorf("writing checksum manifest: %w", err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gzw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encryptPayload(plain []byte, password string) ([]byte, error) {
	key, err := hex.DecodeString(password)
	if err != nil {
		return nil, fmt.Errorf("decoding payload key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	out := bytes.NewBuffer(make([]byte, 0, len(payloadPrefix)+len(nonce)+len(plain)+gcm.Overhead()))
	out.WriteString(payloadPrefix)
	out.Write(nonce)
	out.Write(gcm.Seal(nil, nonce, plain, nil))
	return out.Bytes(), nil
}

func buildChecksumManifest(files []fileEntry) []byte {
	var lines []string
	for _, f := range files {
		if f.isDir || (f.typeflag != 0 && f.typeflag != tar.TypeReg && f.typeflag != tar.TypeRegA) {
			continue
		}
		sum := sha256.Sum256(f.data)
		lines = append(lines, hex.EncodeToString(sum[:])+"  "+f.name)
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func detectPlatform(img v1.Image) (goos, goarch string, err error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return "", "", err
	}
	goos = cfg.OS
	goarch = cfg.Architecture
	if goos == "" {
		goos = "linux"
	}
	if goarch == "" {
		goarch = "amd64"
	}
	return goos, goarch, nil
}

func compileExtractor(goos, goarch string) ([]byte, error) {
	tmpBin, err := os.CreateTemp("", "oci-patch-extractor-*")
	if err != nil {
		return nil, err
	}
	tmpBin.Close()
	defer os.Remove(tmpBin.Name())

	cmd := exec.Command("go", "build", "-ldflags=-s -w", "-o", tmpBin.Name(), ".")
	cmd.Dir = extractorSrcDir
	cmd.Env = append(os.Environ(),
		"GOOS="+goos,
		"GOARCH="+goarch,
		"CGO_ENABLED=0",
		"GOFLAGS=-trimpath",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go build failed: %w\n%s", err, out)
	}

	return os.ReadFile(tmpBin.Name())
}

func archiveOrder(typeflag byte) int {
	switch typeflag {
	case tar.TypeDir:
		return 0
	case tar.TypeReg, tar.TypeRegA:
		return 1
	case tar.TypeSymlink:
		return 2
	case tar.TypeLink:
		return 3
	default:
		return 1
	}
}

// appendPayload appends encrypted payload + password + extractTo + footer to the binary.
//
// Layout (appended after ELF):
//
//	payloadData | password(64 bytes) | extractTo | magic(8) | extractToSize(4) | payloadSize(4)
func appendPayload(bin []byte, payloadData []byte, password, extractTo string) []byte {
	var buf bytes.Buffer
	buf.Write(bin)
	buf.Write(payloadData)
	buf.WriteString(password)
	buf.WriteString(extractTo)

	footer := make([]byte, 16)
	copy(footer[0:8], magicStr)
	binary.LittleEndian.PutUint32(footer[8:12], uint32(len(extractTo)))
	binary.LittleEndian.PutUint32(footer[12:16], uint32(len(payloadData)))
	buf.Write(footer)

	return buf.Bytes()
}

func replaceWithBootstrap(img v1.Image, removePath, outputPath string, data []byte, mode os.FileMode) (v1.Image, error) {
	removePath = "/" + strings.Trim(strings.TrimPrefix(removePath, "/"), "/")
	outputPath = "/" + strings.Trim(strings.TrimPrefix(outputPath, "/"), "/")
	if removePath == "/" || outputPath == "/" {
		return nil, fmt.Errorf("root path is not supported")
	}

	whiteoutPath := path.Join(path.Dir(removePath), ".wh."+path.Base(removePath))
	tarBuf := &bytes.Buffer{}
	tw := tar.NewWriter(tarBuf)

	if err := writeEntry(tw, whiteoutPath, nil, 0o644, tar.TypeReg); err != nil {
		return nil, err
	}
	for _, dir := range collectParentDirs(outputPath) {
		if err := writeEntry(tw, dir, nil, 0o755, tar.TypeDir); err != nil {
			return nil, err
		}
	}
	if err := writeEntry(tw, outputPath, data, mode, tar.TypeReg); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}

	layer, err := tarball.LayerFromReader(bytes.NewReader(tarBuf.Bytes()))
	if err != nil {
		return nil, err
	}
	return mutate.AppendLayers(img, layer)
}

func collectParentDirs(p string) []string {
	var dirs []string
	cur := path.Dir(p)
	for cur != "." && cur != "/" && cur != "" {
		dirs = append(dirs, cur)
		cur = path.Dir(cur)
	}
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}
	return dirs
}

func writeEntry(tw *tar.Writer, dstPath string, data []byte, mode os.FileMode, typeflag byte) error {
	dstPath = strings.Trim(strings.TrimPrefix(dstPath, "/"), "/")
	size := int64(len(data))
	if typeflag == tar.TypeDir {
		size = 0
	}
	hdr := &tar.Header{
		Name:     dstPath,
		Size:     size,
		Mode:     tarHeaderMode(mode),
		Typeflag: typeflag,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if typeflag == tar.TypeDir {
		return nil
	}
	_, err := tw.Write(data)
	return err
}

func tarHeaderMode(mode os.FileMode) int64 {
	m := int64(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		m |= 0o4000
	}
	if mode&os.ModeSetgid != 0 {
		m |= 0o2000
	}
	if mode&os.ModeSticky != 0 {
		m |= 0o1000
	}
	return m
}
