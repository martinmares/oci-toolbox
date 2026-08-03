package ociarchive

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
)

const metadataName = "oci-toolbox-archive.json"

type metadata struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Digest  string `json:"digest"`
}

// Content is the root object stored in an OCI archive. Exactly one of Image and
// Index is non-nil. Close must be called after the object is no longer needed.
type Content struct {
	Image v1.Image
	Index v1.ImageIndex
	dir   string
}

func (c *Content) Close() error {
	if c == nil || c.dir == "" {
		return nil
	}
	err := os.RemoveAll(c.dir)
	c.dir = ""
	return err
}

func WriteImage(archivePath string, image v1.Image) error {
	digest, err := image.Digest()
	if err != nil {
		return fmt.Errorf("reading image digest: %w", err)
	}
	return write(archivePath, metadata{Version: 1, Kind: "image", Digest: digest.String()}, func(p layout.Path) error {
		return p.AppendImage(image)
	})
}

func WriteIndex(archivePath string, index v1.ImageIndex) error {
	digest, err := index.Digest()
	if err != nil {
		return fmt.Errorf("reading image index digest: %w", err)
	}
	return write(archivePath, metadata{Version: 1, Kind: "index", Digest: digest.String()}, func(p layout.Path) error {
		return p.AppendIndex(index)
	})
}

func write(archivePath string, meta metadata, appendRoot func(layout.Path) error) (returnErr error) {
	dir, err := os.MkdirTemp("", "oci-toolbox-oci-layout-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	p, err := layout.Write(dir, empty.Index)
	if err != nil {
		return fmt.Errorf("creating OCI image layout: %w", err)
	}
	if err := appendRoot(p); err != nil {
		return fmt.Errorf("writing OCI image layout: %w", err)
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, metadataName), raw, 0o644); err != nil {
		return err
	}

	file, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); returnErr == nil && closeErr != nil {
			returnErr = closeErr
		}
		if returnErr != nil {
			_ = os.Remove(archivePath)
		}
	}()
	gzw := gzip.NewWriter(file)
	tw := tar.NewWriter(gzw)
	if err := writeTar(tw, dir); err != nil {
		_ = tw.Close()
		_ = gzw.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		_ = gzw.Close()
		return err
	}
	if err := gzw.Close(); err != nil {
		return err
	}
	return nil
}

func writeTar(tw *tar.Writer, root string) error {
	return filepath.Walk(root, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filePath == root {
			return nil
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func Open(archivePath string) (*Content, error) {
	dir, err := os.MkdirTemp("", "oci-toolbox-oci-layout-")
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := extract(archivePath, dir); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, metadataName))
	if err != nil {
		return nil, fmt.Errorf("reading OCI archive metadata: %w", err)
	}
	var meta metadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parsing OCI archive metadata: %w", err)
	}
	if meta.Version != 1 {
		return nil, fmt.Errorf("unsupported OCI archive metadata version %d", meta.Version)
	}
	digest, err := v1.NewHash(meta.Digest)
	if err != nil {
		return nil, fmt.Errorf("invalid OCI archive root digest: %w", err)
	}
	p, err := layout.FromPath(dir)
	if err != nil {
		return nil, fmt.Errorf("opening OCI image layout: %w", err)
	}
	content := &Content{dir: dir}
	switch meta.Kind {
	case "image":
		content.Image, err = p.Image(digest)
	case "index":
		var root v1.ImageIndex
		root, err = p.ImageIndex()
		if err == nil {
			content.Index, err = root.ImageIndex(digest)
		}
	default:
		return nil, fmt.Errorf("unsupported OCI archive root kind %q", meta.Kind)
	}
	if err != nil {
		return nil, fmt.Errorf("loading OCI archive root: %w", err)
	}
	failed = false
	return content, nil
}

func extract(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("opening OCI archive: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading OCI archive: %w", err)
		}
		clean := path.Clean(header.Name)
		if clean == "." || strings.Contains(header.Name, `\`) || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return fmt.Errorf("unsafe path %q in OCI archive", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(clean))
		relative, err := filepath.Rel(destination, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path %q in OCI archive", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported entry %q in OCI archive", header.Name)
		}
	}
}
