package cmd

import (
	"errors"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/datalite/oci-patch/internal/image"
	"github.com/datalite/oci-patch/internal/logging"
)

var (
	lsCreds    string
	lsOS       string
	lsArch     string
	lsVariant  string
	lsInsecure bool
	lsDebug    bool
)

var lsCmd = &cobra.Command{
	Use:   "ls IMAGE PATH",
	Short: "List entries from an OCI image directory",
	Args:  cobra.ExactArgs(2),
	Example: `  oci-patch ls docker://registry.example.com/app:latest /app/resources
  oci-patch ls --src-insecure docker://registry.example.com/app:latest /app/resources`,
	RunE: runLS,
}

func init() {
	lsCmd.Flags().StringVar(&lsCreds, "creds", "", "registry credentials user:password")
	lsCmd.Flags().StringVar(&lsOS, "override-os", "", "override OS (e.g. linux)")
	lsCmd.Flags().StringVar(&lsArch, "override-arch", "", "override architecture (e.g. arm64, amd64)")
	lsCmd.Flags().StringVar(&lsVariant, "override-variant", "", "override variant (e.g. v8)")
	lsCmd.Flags().BoolVar(&lsInsecure, "insecure", false, "skip TLS certificate verification")
	lsCmd.Flags().BoolVar(&lsInsecure, "src-insecure", false, "skip TLS certificate verification for source registry")
	lsCmd.Flags().BoolVar(&lsDebug, "debug", false, "enable verbose output")
	rootCmd.AddCommand(lsCmd)
}

func runLS(c *cobra.Command, args []string) error {
	if lsDebug {
		logging.Setup(slog.LevelDebug)
	}

	img, err := loadImage(args[0], lsCreds, lsInsecure, lsOS, lsArch, lsVariant)
	if err != nil {
		return err
	}

	entries, err := image.ListEntries(img, args[1])
	if err != nil {
		if errors.Is(err, image.ErrPathNotFound) || errors.Is(err, image.ErrEmptyImage) {
			if lsDebug {
				slog.Warn("path not found in image", "path", args[1])
			}
			return nil
		}
		return err
	}

	return writeLS(c.OutOrStdout(), entries, isTTYWriter(c.OutOrStdout()))
}
