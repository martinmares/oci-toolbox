// extractor is a self-extracting binary embedded into the target image by oci-patch shadow action.
// At runtime inside the container it:
//  1. Reads its own payload (encrypted blob + password + extract-to path).
//  2. Decrypts the blob into a tar.gz archive.
//  3. Restores the archive into the configured extract-to directory.
//
// Compile with CGO_ENABLED=0 for a fully static binary.
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	charmlog "github.com/charmbracelet/log"
)

// Payload appended to this binary by oci-patch:
//
//	payloadData(payloadSize) | password(64 bytes hex) | extractTo(extractToSize bytes) | magic(8) | extractToSize(4) | payloadSize(4)
const (
	magicStr         = "OCIPATCH"
	passwordLen      = 64        // hex-encoded 32 random bytes = 64 chars
	footerSize       = 8 + 4 + 4 // magic(8) + extractToSize(4) + payloadSize(4)
	checksumFileName = ".app.sha256"
	payloadPrefix    = "GCM1"
)

type options struct {
	cleanup bool
	debug   bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("app.self failed", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	setupLogger(opts.debug)

	selfPath := os.Args[0]
	slog.Debug("starting extractor", "self", selfPath, "cleanup", opts.cleanup)

	self, err := os.ReadFile(selfPath)
	if err != nil {
		return fmt.Errorf("reading self: %w", err)
	}

	payloadData, password, extractTo, err := parsePayload(self)
	if err != nil {
		return fmt.Errorf("parsing payload: %w", err)
	}
	slog.Debug("payload parsed", "extract_to", extractTo, "payload_bytes", len(payloadData))

	archiveData, err := decryptPayload(payloadData, password)
	if err != nil {
		return fmt.Errorf("decrypting payload: %w", err)
	}
	slog.Debug("payload decrypted", "archive_bytes", len(archiveData))

	if err := os.MkdirAll(extractTo, 0o775); err != nil {
		return fmt.Errorf("creating extract dir %q: %w", extractTo, err)
	}

	manifest, err := extractArchive(archiveData, extractTo)
	if err != nil {
		return fmt.Errorf("extracting: %w", err)
	}
	if len(manifest) == 0 {
		if opts.cleanup {
			return fmt.Errorf("checksum manifest missing; refusing cleanup")
		}
		slog.Warn("checksum manifest missing; integrity verification skipped")
	} else {
		if err := verifyExtractedFiles(extractTo, manifest); err != nil {
			return fmt.Errorf("integrity check failed: %w", err)
		}
		slog.Info("integrity check passed", "files", len(manifest))
	}

	if opts.cleanup {
		slog.Debug("cleanup requested", "self", selfPath)
		if err := os.Remove(selfPath); err != nil {
			return fmt.Errorf("removing self: %w", err)
		}
		slog.Info("self removed", "path", selfPath)
	}

	return nil
}

func parseOptions(args []string) (options, error) {
	var opts options
	fs := flag.NewFlagSet("app.self", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.cleanup, "cleanup", false, "extract payload and remove app.self after a successful integrity check")
	fs.BoolVar(&opts.debug, "debug", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return opts, nil
}

func setupLogger(debug bool) {
	level := charmlog.WarnLevel
	if debug {
		level = charmlog.DebugLevel
	}
	handler := charmlog.NewWithOptions(os.Stderr, charmlog.Options{
		Level:           level,
		TimeFormat:      time.RFC3339,
		ReportTimestamp: true,
	})
	slog.SetDefault(slog.New(handler))
}

// parsePayload reads the payload appended after the ELF binary.
// Footer layout (from end of file, little-endian):
//
//	magic(8) | extractToSize(4) | payloadSize(4)
//
// Payload before footer (from right to left):
//
//	extractTo(extractToSize) | password(passwordLen) | payloadData(payloadSize)
func parsePayload(data []byte) (payloadData []byte, password, extractTo string, err error) {
	if len(data) < footerSize {
		return nil, "", "", fmt.Errorf("binary too small — no payload")
	}

	footer := data[len(data)-footerSize:]
	var extractToSize, payloadSize int
	switch {
	case string(footer[:8]) == magicStr:
		extractToSize = int(binary.LittleEndian.Uint32(footer[8:12]))
		payloadSize = int(binary.LittleEndian.Uint32(footer[12:16]))
	case string(footer[8:16]) == magicStr:
		// Legacy footer layout used older builder versions:
		// payloadSize(4) | extractToSize(4) | magic(8)
		payloadSize = int(binary.LittleEndian.Uint32(footer[0:4]))
		extractToSize = int(binary.LittleEndian.Uint32(footer[4:8]))
	default:
		return nil, "", "", fmt.Errorf("no payload attached — use oci-patch shadow action to build this binary")
	}

	totalPayload := payloadSize + passwordLen + extractToSize + footerSize
	if len(data) < totalPayload {
		return nil, "", "", fmt.Errorf("payload size mismatch (corrupt binary?)")
	}

	base := len(data) - totalPayload
	payloadData = data[base : base+payloadSize]
	password = string(data[base+payloadSize : base+payloadSize+passwordLen])
	extractTo = string(data[base+payloadSize+passwordLen : base+payloadSize+passwordLen+extractToSize])

	return payloadData, password, extractTo, nil
}

func decryptPayload(payloadData []byte, password string) ([]byte, error) {
	if len(payloadData) < len(payloadPrefix) || string(payloadData[:len(payloadPrefix)]) != payloadPrefix {
		return nil, fmt.Errorf("unsupported payload format")
	}

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

	payloadData = payloadData[len(payloadPrefix):]
	if len(payloadData) < gcm.NonceSize()+gcm.Overhead() {
		return nil, fmt.Errorf("encrypted payload too small")
	}
	nonce := payloadData[:gcm.NonceSize()]
	ciphertext := payloadData[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("opening gcm payload: %w", err)
	}
	return plain, nil
}

func extractArchive(archiveData []byte, destDir string) (map[string]string, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		return nil, fmt.Errorf("opening gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var manifestData []byte

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar entry: %w", err)
		}

		cleanName := filepath.Clean(hdr.Name)
		if cleanName == "." {
			continue
		}
		if cleanName == checksumFileName {
			slog.Debug("reading checksum manifest", "entry", cleanName)
			manifestData, err = io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("reading checksum manifest: %w", err)
			}
			continue
		}

		dstPath := filepath.Join(destDir, cleanName)
		if !isSubPath(destDir, dstPath) {
			return nil, fmt.Errorf("tar traversal detected: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			mode := fileModeFromHeader(hdr, 0o775)
			slog.Debug("creating directory", "path", dstPath, "mode", fmt.Sprintf("%#o", mode.Perm()))
			if err := os.MkdirAll(dstPath, mode); err != nil {
				return nil, fmt.Errorf("creating directory %s: %w", dstPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o775); err != nil {
				return nil, fmt.Errorf("creating parent for %s: %w", dstPath, err)
			}
			mode := fileModeFromHeader(hdr, 0o664)
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", cleanName, err)
			}
			slog.Debug("extracting file", "entry", cleanName, "path", dstPath, "bytes", len(data), "mode", fmt.Sprintf("%#o", mode.Perm()))
			if err := os.WriteFile(dstPath, data, mode); err != nil {
				return nil, fmt.Errorf("writing %s: %w", dstPath, err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o775); err != nil {
				return nil, fmt.Errorf("creating parent for %s: %w", dstPath, err)
			}
			if err := os.RemoveAll(dstPath); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("removing existing %s: %w", dstPath, err)
			}
			slog.Debug("creating symlink", "path", dstPath, "target", hdr.Linkname)
			if err := os.Symlink(hdr.Linkname, dstPath); err != nil {
				return nil, fmt.Errorf("creating symlink %s -> %s: %w", dstPath, hdr.Linkname, err)
			}
		case tar.TypeLink:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o775); err != nil {
				return nil, fmt.Errorf("creating parent for %s: %w", dstPath, err)
			}
			linkTarget := filepath.Join(destDir, filepath.Clean(hdr.Linkname))
			if !isSubPath(destDir, linkTarget) {
				return nil, fmt.Errorf("hardlink target escapes extract root: %s", hdr.Linkname)
			}
			slog.Debug("creating hardlink", "path", dstPath, "target", linkTarget)
			if err := os.Link(linkTarget, dstPath); err != nil {
				return nil, fmt.Errorf("creating hardlink %s -> %s: %w", dstPath, hdr.Linkname, err)
			}
		default:
			return nil, fmt.Errorf("unsupported tar entry %q for %s", hdr.Typeflag, cleanName)
		}
	}

	if len(manifestData) == 0 {
		return nil, nil
	}
	return parseChecksumManifest(manifestData)
}

func parseChecksumManifest(data []byte) (map[string]string, error) {
	manifest := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid checksum manifest line: %q", line)
		}
		manifest[filepath.Clean(parts[1])] = parts[0]
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading checksum manifest: %w", err)
	}
	return manifest, nil
}

func verifyExtractedFiles(destDir string, manifest map[string]string) error {
	for relPath, want := range manifest {
		dstPath := filepath.Join(destDir, relPath)
		data, err := os.ReadFile(dstPath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", relPath, err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if got != want {
			slog.Warn("checksum mismatch", "path", relPath, "expected", want, "actual", got)
			return fmt.Errorf("%s checksum mismatch", relPath)
		}
		slog.Debug("checksum verified", "path", relPath, "sha256", got)
	}
	return nil
}

func fileModeFromHeader(hdr *tar.Header, fallback os.FileMode) os.FileMode {
	mode := hdr.FileInfo().Mode()
	if mode.Perm() == 0 {
		mode = fallback
	}
	return mode
}

func isSubPath(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
