package cmd

import (
	"errors"
	"io"
	"log/slog"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/spf13/cobra"

	"github.com/martinmares/oci-patch/internal/image"
	"github.com/martinmares/oci-patch/internal/logging"
)

var (
	catCreds    string
	catOS       string
	catArch     string
	catVariant  string
	catInsecure bool
	catDebug    bool
)

var catCmd = &cobra.Command{
	Use:   "cat IMAGE PATH",
	Short: "Print a file from an OCI image",
	Args:  cobra.ExactArgs(2),
	Example: `  oci-patch cat docker://registry.example.com/app:latest /app/resources/git.properties
  oci-patch cat --src-insecure docker://registry.example.com/app:latest /app/resources/git.properties`,
	RunE: runCat,
}

func init() {
	catCmd.Flags().StringVar(&catCreds, "creds", "", "registry credentials user:password")
	catCmd.Flags().StringVar(&catOS, "override-os", "", "override OS (e.g. linux)")
	catCmd.Flags().StringVar(&catArch, "override-arch", "", "override architecture (e.g. arm64, amd64)")
	catCmd.Flags().StringVar(&catVariant, "override-variant", "", "override variant (e.g. v8)")
	catCmd.Flags().BoolVar(&catInsecure, "insecure", false, "skip TLS certificate verification")
	catCmd.Flags().BoolVar(&catInsecure, "src-insecure", false, "skip TLS certificate verification for source registry")
	catCmd.Flags().BoolVar(&catDebug, "debug", false, "enable verbose output")
	rootCmd.AddCommand(catCmd)
}

func runCat(c *cobra.Command, args []string) error {
	if catDebug {
		logging.Setup(slog.LevelDebug)
	}

	img, err := loadImage(args[0], catCreds, catInsecure, catOS, catArch, catVariant)
	if err != nil {
		return err
	}

	return catImageFile(c.OutOrStdout(), img, args[1], catDebug)
}

func catImageFile(out io.Writer, img v1.Image, path string, debug bool) error {
	data, err := image.ReadFile(img, path)
	if errors.Is(err, image.ErrFileNotFound) {
		if debug {
			slog.Warn("file not found in image", "path", path)
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}
