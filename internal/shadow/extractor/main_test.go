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

func TestDecryptPayloadAndExtractArchive(t *testing.T) {
	password := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plain := buildTestArchive(t)

	payload, err := encryptForTest(plain, password)
	if err != nil {
		t.Fatalf("encryptForTest: %v", err)
	}

	gotPlain, err := decryptPayload(payload, password)
	if err != nil {
		t.Fatalf("decryptPayload: %v", err)
	}

	dir := t.TempDir()
	manifest, err := extractArchive(gotPlain, dir)
	if err != nil {
		t.Fatalf("extractArchive: %v", err)
	}
	if len(manifest) != 1 {
		t.Fatalf("manifest entries = %d, want 1", len(manifest))
	}
	if err := verifyExtractedFiles(dir, manifest); err != nil {
		t.Fatalf("verifyExtractedFiles: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "app.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("file contents = %q, want hello", data)
	}
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

func encryptForTest(plain []byte, password string) ([]byte, error) {
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
	out.WriteString(payloadPrefix)
	out.Write(nonce)
	out.Write(gcm.Seal(nil, nonce, plain, nil))
	return out.Bytes(), nil
}
