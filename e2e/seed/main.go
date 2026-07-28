package main

import (
	"archive/tar"
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

func main() {
	refFlag := flag.String("ref", "", "destination image reference")
	flag.Parse()

	if *refFlag == "" {
		fmt.Fprintln(os.Stderr, "missing --ref")
		os.Exit(2)
	}

	ref, err := name.ParseReference(*refFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse ref: %v\n", err)
		os.Exit(1)
	}

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	content := []byte("oci-toolbox e2e fixture\n")
	hdr := &tar.Header{
		Name:     "fixture.txt",
		Size:     int64(len(content)),
		Mode:     0o644,
		ModTime:  time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		fmt.Fprintf(os.Stderr, "tar header: %v\n", err)
		os.Exit(1)
	}
	if _, err := tw.Write(content); err != nil {
		fmt.Fprintf(os.Stderr, "tar write: %v\n", err)
		os.Exit(1)
	}
	if err := tw.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "tar close: %v\n", err)
		os.Exit(1)
	}

	layer, err := tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create layer: %v\n", err)
		os.Exit(1)
	}

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "append layer: %v\n", err)
		os.Exit(1)
	}

	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	if err := remote.Write(ref, img, remote.WithTransport(transport)); err != nil {
		fmt.Fprintf(os.Stderr, "seed image: %v\n", err)
		os.Exit(1)
	}
}
