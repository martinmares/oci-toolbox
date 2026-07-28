// extractor is a self-extracting binary embedded into the target image by oci-toolbox shadow action.
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
)

// Payload appended to this binary by oci-toolbox:
//
//	payloadData(payloadSize) | password(64 bytes hex) | extractTo(extractToSize bytes) | magic(8) | extractToSize(4) | payloadSize(4)
const (
	magicStr          = "OCIPATCH"
	passwordLen       = 64        // hex-encoded 32 random bytes = 64 chars
	footerSize        = 8 + 4 + 4 // magic(8) + extractToSize(4) + payloadSize(4)
	checksumFileName  = ".app.sha256"
	payloadPrefixGCM2 = "GCM2"
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

	self, meta, err := openPayload(selfPath)
	if err != nil {
		return fmt.Errorf("opening payload: %w", err)
	}
	defer self.Close()

	payloadReader := io.NewSectionReader(self, meta.payloadOffset, meta.payloadSize)
	archiveReader, err := decryptPayloadReader(payloadReader, meta.payloadSize, meta.password)
	if err != nil {
		return fmt.Errorf("decrypting payload: %w", err)
	}
	slog.Debug("payload parsed", "extract_to", meta.extractTo, "payload_bytes", meta.payloadSize)

	if err := os.MkdirAll(meta.extractTo, 0o775); err != nil {
		return fmt.Errorf("creating extract dir %q: %w", meta.extractTo, err)
	}

	manifest, sums, err := extractArchive(archiveReader, meta.extractTo)
	if err != nil {
		return fmt.Errorf("extracting: %w", err)
	}
	if len(manifest) == 0 {
		if opts.cleanup {
			return fmt.Errorf("checksum manifest missing; refusing cleanup")
		}
		slog.Warn("checksum manifest missing; integrity verification skipped")
	} else {
		if err := verifyExtractedChecksums(manifest, sums); err != nil {
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

type payloadMeta struct {
	payloadOffset int64
	payloadSize   int64
	password      string
	extractTo     string
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
	level := slog.LevelWarn
	if debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
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
		return nil, "", "", fmt.Errorf("no payload attached — use oci-toolbox shadow action to build this binary")
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

func openPayload(path string) (*os.File, payloadMeta, error) {
	self, err := os.Open(path)
	if err != nil {
		return nil, payloadMeta{}, fmt.Errorf("opening self: %w", err)
	}

	stat, err := self.Stat()
	if err != nil {
		self.Close()
		return nil, payloadMeta{}, fmt.Errorf("stat self: %w", err)
	}
	if stat.Size() < footerSize {
		self.Close()
		return nil, payloadMeta{}, fmt.Errorf("binary too small — no payload")
	}

	footer := make([]byte, footerSize)
	if _, err := self.ReadAt(footer, stat.Size()-footerSize); err != nil {
		self.Close()
		return nil, payloadMeta{}, fmt.Errorf("reading payload footer: %w", err)
	}

	var extractToSize, payloadSize int64
	switch {
	case string(footer[:8]) == magicStr:
		extractToSize = int64(binary.LittleEndian.Uint32(footer[8:12]))
		payloadSize = int64(binary.LittleEndian.Uint32(footer[12:16]))
	case string(footer[8:16]) == magicStr:
		payloadSize = int64(binary.LittleEndian.Uint32(footer[0:4]))
		extractToSize = int64(binary.LittleEndian.Uint32(footer[4:8]))
	default:
		self.Close()
		return nil, payloadMeta{}, fmt.Errorf("no payload attached — use oci-toolbox shadow action to build this binary")
	}

	totalPayload := payloadSize + passwordLen + extractToSize + footerSize
	if stat.Size() < totalPayload {
		self.Close()
		return nil, payloadMeta{}, fmt.Errorf("payload size mismatch (corrupt binary?)")
	}

	base := stat.Size() - totalPayload
	metaData := make([]byte, passwordLen+extractToSize)
	if _, err := self.ReadAt(metaData, base+payloadSize); err != nil {
		self.Close()
		return nil, payloadMeta{}, fmt.Errorf("reading payload metadata: %w", err)
	}

	return self, payloadMeta{
		payloadOffset: base,
		payloadSize:   payloadSize,
		password:      string(metaData[:passwordLen]),
		extractTo:     string(metaData[passwordLen:]),
	}, nil
}

func decryptPayloadReader(r io.Reader, payloadSize int64, password string) (io.Reader, error) {
	prefix := make([]byte, len(payloadPrefixGCM2))
	if _, err := io.ReadFull(r, prefix); err != nil {
		return nil, fmt.Errorf("reading payload prefix: %w", err)
	}
	if string(prefix) != payloadPrefixGCM2 {
		return nil, fmt.Errorf("unsupported payload format")
	}
	return newChunkedGCMReader(r, password)
}

type chunkedGCMReader struct {
	r       io.Reader
	gcm     cipher.AEAD
	nonce   []byte
	counter uint64
	buf     []byte
	done    bool
}

func newChunkedGCMReader(r io.Reader, password string) (io.Reader, error) {
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

	var chunkSize uint32
	if err := binary.Read(r, binary.LittleEndian, &chunkSize); err != nil {
		return nil, fmt.Errorf("reading chunk size: %w", err)
	}
	if chunkSize == 0 {
		return nil, fmt.Errorf("invalid chunk size 0")
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(r, nonce); err != nil {
		return nil, fmt.Errorf("reading nonce: %w", err)
	}
	return &chunkedGCMReader{r: r, gcm: gcm, nonce: nonce}, nil
}

func (r *chunkedGCMReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 && !r.done {
		if err := r.readChunk(); err != nil {
			return 0, err
		}
	}
	if len(r.buf) == 0 && r.done {
		return 0, io.EOF
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *chunkedGCMReader) readChunk() error {
	var sealedSize uint32
	if err := binary.Read(r.r, binary.LittleEndian, &sealedSize); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			r.done = true
			return nil
		}
		return fmt.Errorf("reading chunk header: %w", err)
	}
	if sealedSize < uint32(r.gcm.Overhead()) {
		return fmt.Errorf("invalid encrypted chunk size %d", sealedSize)
	}
	sealed := make([]byte, sealedSize)
	if _, err := io.ReadFull(r.r, sealed); err != nil {
		return fmt.Errorf("reading encrypted chunk: %w", err)
	}
	plain, err := r.gcm.Open(nil, chunkNonce(r.nonce, r.counter), sealed, nil)
	if err != nil {
		return fmt.Errorf("opening gcm chunk %d: %w", r.counter, err)
	}
	r.counter++
	r.buf = plain
	return nil
}

func chunkNonce(base []byte, counter uint64) []byte {
	nonce := append([]byte(nil), base...)
	binary.BigEndian.PutUint64(nonce[len(nonce)-8:], counter)
	return nonce
}

func extractArchive(archiveData io.Reader, destDir string) (map[string]string, map[string]string, error) {
	gzr, err := gzip.NewReader(archiveData)
	if err != nil {
		return nil, nil, fmt.Errorf("opening gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var manifestData []byte
	extractedSums := map[string]string{}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("reading tar entry: %w", err)
		}

		cleanName := filepath.Clean(hdr.Name)
		if cleanName == "." {
			continue
		}
		if cleanName == checksumFileName {
			slog.Debug("reading checksum manifest", "entry", cleanName)
			manifestData, err = io.ReadAll(tr)
			if err != nil {
				return nil, nil, fmt.Errorf("reading checksum manifest: %w", err)
			}
			continue
		}

		dstPath := filepath.Join(destDir, cleanName)
		if !isSubPath(destDir, dstPath) {
			return nil, nil, fmt.Errorf("tar traversal detected: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			mode := fileModeFromHeader(hdr, 0o775)
			slog.Debug("creating directory", "path", dstPath, "mode", fmt.Sprintf("%#o", mode.Perm()))
			if err := os.MkdirAll(dstPath, mode); err != nil {
				return nil, nil, fmt.Errorf("creating directory %s: %w", dstPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o775); err != nil {
				return nil, nil, fmt.Errorf("creating parent for %s: %w", dstPath, err)
			}
			mode := fileModeFromHeader(hdr, 0o664)
			out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return nil, nil, fmt.Errorf("opening %s: %w", dstPath, err)
			}
			hasher := sha256.New()
			written, copyErr := io.Copy(out, io.TeeReader(tr, hasher))
			closeErr := out.Close()
			if copyErr != nil {
				return nil, nil, fmt.Errorf("writing %s: %w", dstPath, copyErr)
			}
			if closeErr != nil {
				return nil, nil, fmt.Errorf("closing %s: %w", dstPath, closeErr)
			}
			if err := os.Chmod(dstPath, mode); err != nil {
				return nil, nil, fmt.Errorf("chmod %s: %w", dstPath, err)
			}
			extractedSums[cleanName] = hex.EncodeToString(hasher.Sum(nil))
			slog.Debug("extracted file", "entry", cleanName, "path", dstPath, "bytes", written, "mode", fmt.Sprintf("%#o", mode.Perm()))
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o775); err != nil {
				return nil, nil, fmt.Errorf("creating parent for %s: %w", dstPath, err)
			}
			if err := os.RemoveAll(dstPath); err != nil && !os.IsNotExist(err) {
				return nil, nil, fmt.Errorf("removing existing %s: %w", dstPath, err)
			}
			slog.Debug("creating symlink", "path", dstPath, "target", hdr.Linkname)
			if err := os.Symlink(hdr.Linkname, dstPath); err != nil {
				return nil, nil, fmt.Errorf("creating symlink %s -> %s: %w", dstPath, hdr.Linkname, err)
			}
		case tar.TypeLink:
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o775); err != nil {
				return nil, nil, fmt.Errorf("creating parent for %s: %w", dstPath, err)
			}
			linkTarget := filepath.Join(destDir, filepath.Clean(hdr.Linkname))
			if !isSubPath(destDir, linkTarget) {
				return nil, nil, fmt.Errorf("hardlink target escapes extract root: %s", hdr.Linkname)
			}
			slog.Debug("creating hardlink", "path", dstPath, "target", linkTarget)
			if err := os.Link(linkTarget, dstPath); err != nil {
				return nil, nil, fmt.Errorf("creating hardlink %s -> %s: %w", dstPath, hdr.Linkname, err)
			}
		default:
			return nil, nil, fmt.Errorf("unsupported tar entry %q for %s", hdr.Typeflag, cleanName)
		}
	}

	if len(manifestData) == 0 {
		return nil, extractedSums, nil
	}
	manifest, err := parseChecksumManifest(manifestData)
	if err != nil {
		return nil, nil, err
	}
	return manifest, extractedSums, nil
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

func verifyExtractedChecksums(manifest, extracted map[string]string) error {
	for relPath, want := range manifest {
		got, ok := extracted[relPath]
		if !ok {
			return fmt.Errorf("%s missing after extraction", relPath)
		}
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
