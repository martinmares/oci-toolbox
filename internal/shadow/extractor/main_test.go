package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParsePayload_acceptsCurrentFooterLayout(t *testing.T) {
	payloadData := []byte("payload")
	password := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	extractTo := "/app"
	bin := []byte("ELF")

	payload := make([]byte, 0, len(bin)+len(payloadData)+len(password)+len(extractTo)+footerSize)
	payload = append(payload, bin...)
	payload = append(payload, payloadData...)
	payload = append(payload, []byte(password)...)
	payload = append(payload, []byte(extractTo)...)

	footer := make([]byte, footerSize)
	copy(footer[:8], magicStr)
	binary.LittleEndian.PutUint32(footer[8:12], uint32(len(extractTo)))
	binary.LittleEndian.PutUint32(footer[12:16], uint32(len(payloadData)))
	payload = append(payload, footer...)

	gotPayload, gotPassword, gotExtractTo, err := parsePayload(payload)
	if err != nil {
		t.Fatalf("parsePayload: %v", err)
	}
	if string(gotPayload) != string(payloadData) || gotPassword != password || gotExtractTo != extractTo {
		t.Fatalf("unexpected payload: payload=%q password=%q extractTo=%q", gotPayload, gotPassword, gotExtractTo)
	}
}

func TestParsePayload_acceptsLegacyFooterLayout(t *testing.T) {
	payloadData := []byte("payload")
	password := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	extractTo := "/app"
	bin := []byte("ELF")

	payload := make([]byte, 0, len(bin)+len(payloadData)+len(password)+len(extractTo)+footerSize)
	payload = append(payload, bin...)
	payload = append(payload, payloadData...)
	payload = append(payload, []byte(password)...)
	payload = append(payload, []byte(extractTo)...)

	footer := make([]byte, footerSize)
	binary.LittleEndian.PutUint32(footer[0:4], uint32(len(payloadData)))
	binary.LittleEndian.PutUint32(footer[4:8], uint32(len(extractTo)))
	copy(footer[8:16], magicStr)
	payload = append(payload, footer...)

	gotPayload, gotPassword, gotExtractTo, err := parsePayload(payload)
	if err != nil {
		t.Fatalf("parsePayload: %v", err)
	}
	if string(gotPayload) != string(payloadData) || gotPassword != password || gotExtractTo != extractTo {
		t.Fatalf("unexpected payload: payload=%q password=%q extractTo=%q", gotPayload, gotPassword, gotExtractTo)
	}
}

func TestParseChecksumManifestAndVerify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := parseChecksumManifest([]byte("2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824  app.txt\n"))
	if err != nil {
		t.Fatalf("parseChecksumManifest: %v", err)
	}
	if err := verifyExtractedFiles(dir, manifest); err != nil {
		t.Fatalf("verifyExtractedFiles: %v", err)
	}
}

func TestParseOptions(t *testing.T) {
	opts, err := parseOptions([]string{"--cleanup", "--debug"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if !opts.cleanup || !opts.debug {
		t.Fatalf("unexpected options: %+v", opts)
	}
}

func TestDecryptPayloadReaderAndExtractArchiveChunked(t *testing.T) {
	password := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plain := buildTestArchive(t)

	payload, err := encryptChunkedForTest(plain, password, 3)
	if err != nil {
		t.Fatalf("encryptChunkedForTest: %v", err)
	}

	archiveReader, err := decryptPayloadReader(bytes.NewReader(payload), int64(len(payload)), password)
	if err != nil {
		t.Fatalf("decryptPayloadReader: %v", err)
	}

	dir := t.TempDir()
	manifest, sums, err := extractArchive(archiveReader, dir)
	if err != nil {
		t.Fatalf("extractArchive: %v", err)
	}
	if len(manifest) != 1 {
		t.Fatalf("manifest entries = %d, want 1", len(manifest))
	}
	if err := verifyExtractedChecksums(manifest, sums); err != nil {
		t.Fatalf("verifyExtractedChecksums: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "app.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("file contents = %q, want hello", data)
	}
}

func TestRunExtractsAppendedPayload(t *testing.T) {
	password := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	archive := buildTestArchive(t)
	payload, err := encryptChunkedForTest(archive, password, 3)
	if err != nil {
		t.Fatalf("encryptChunkedForTest: %v", err)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	executableData, err := os.ReadFile(executable)
	if err != nil {
		t.Fatalf("os.ReadFile(%q): %v", executable, err)
	}

	destDir := filepath.Join(t.TempDir(), "app")
	selfPath := filepath.Join(t.TempDir(), "app.self")
	self := appendCurrentPayload(executableData, payload, password, destDir)
	if err := os.WriteFile(selfPath, self, 0o755); err != nil {
		t.Fatalf("os.WriteFile(%q): %v", selfPath, err)
	}

	oldArgs := os.Args
	os.Args = []string{selfPath}
	t.Cleanup(func() { os.Args = oldArgs })

	if err := run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "app.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile extracted file: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("extracted content = %q, want hello", data)
	}
}

func TestExecutableExtractsAppendedPayload(t *testing.T) {
	password := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	archive := buildTestArchive(t)
	payload, err := encryptChunkedForTest(archive, password, 3)
	if err != nil {
		t.Fatalf("encryptChunkedForTest: %v", err)
	}

	extractorPath := filepath.Join(t.TempDir(), "extractor")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", extractorPath, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building extractor: %v\n%s", err, output)
	}
	extractor, err := os.ReadFile(extractorPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q): %v", extractorPath, err)
	}

	destDir := filepath.Join(t.TempDir(), "app")
	selfPath := filepath.Join(t.TempDir(), "app.self")
	self := appendCurrentPayload(extractor, payload, password, destDir)
	if err := os.WriteFile(selfPath, self, 0o755); err != nil {
		t.Fatalf("os.WriteFile(%q): %v", selfPath, err)
	}

	runExtractor := exec.Command(selfPath)
	if output, err := runExtractor.CombinedOutput(); err != nil {
		t.Fatalf("running app.self: %v\n%s", err, output)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "app.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile extracted file: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("extracted content = %q, want hello", data)
	}
}

func appendCurrentPayload(executable, payload []byte, password, extractTo string) []byte {
	result := make([]byte, 0, len(executable)+len(payload)+len(password)+len(extractTo)+footerSize)
	result = append(result, executable...)
	result = append(result, payload...)
	result = append(result, password...)
	result = append(result, extractTo...)

	footer := make([]byte, footerSize)
	copy(footer[:8], magicStr)
	binary.LittleEndian.PutUint32(footer[8:12], uint32(len(extractTo)))
	binary.LittleEndian.PutUint32(footer[12:16], uint32(len(payload)))
	return append(result, footer...)
}

func buildTestArchive(t *testing.T) []byte {
	t.Helper()

	buf := &bytes.Buffer{}
	gzw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gzw)

	write := func(hdr *tar.Header, data []byte) {
		t.Helper()
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader(%s): %v", hdr.Name, err)
		}
		if len(data) > 0 {
			if _, err := tw.Write(data); err != nil {
				t.Fatalf("Write(%s): %v", hdr.Name, err)
			}
		}
	}

	write(&tar.Header{Name: "subdir/", Typeflag: tar.TypeDir, Mode: 0o775}, nil)
	write(&tar.Header{Name: "app.txt", Typeflag: tar.TypeReg, Mode: 0o664, Size: 5}, []byte("hello"))
	write(&tar.Header{Name: checksumFileName, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len("2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824  app.txt\n"))}, []byte("2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824  app.txt\n"))

	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzw.Close: %v", err)
	}
	return buf.Bytes()
}

func encryptChunkedForTest(plain []byte, password string, chunkSize int) ([]byte, error) {
	key, err := hex.DecodeString(password)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := bytes.Repeat([]byte{0x42}, gcm.NonceSize())
	out := bytes.NewBuffer(nil)
	out.WriteString(payloadPrefixGCM2)
	if err := binary.Write(out, binary.LittleEndian, uint32(chunkSize)); err != nil {
		return nil, err
	}
	out.Write(nonce)
	for counter, offset := uint64(0), 0; offset < len(plain); counter, offset = counter+1, offset+chunkSize {
		end := offset + chunkSize
		if end > len(plain) {
			end = len(plain)
		}
		sealed := gcm.Seal(nil, chunkNonce(nonce, counter), plain[offset:end], nil)
		if err := binary.Write(out, binary.LittleEndian, uint32(len(sealed))); err != nil {
			return nil, err
		}
		out.Write(sealed)
	}
	return out.Bytes(), nil
}
