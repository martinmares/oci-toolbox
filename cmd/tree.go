package cmd

import (
	"errors"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/martinmares/oci-toolbox/internal/image"
	"github.com/martinmares/oci-toolbox/internal/logging"
)

var (
	treeCreds    string
	treeOS       string
	treeArch     string
	treeVariant  string
	treeInsecure bool
	treeDebug    bool
)

var treeCmd = &cobra.Command{
	Use:   "tree IMAGE PATH",
	Short: "Print a tree view of an OCI image path",
	Args:  cobra.ExactArgs(2),
	Example: `  oci-toolbox tree docker://registry.example.com/app:latest /app
  oci-toolbox tree --src-insecure docker://registry.example.com/app:latest /app`,
	RunE: runTree,
}

func init() {
	treeCmd.Flags().StringVar(&treeCreds, "creds", "", "registry credentials user:password")
	treeCmd.Flags().StringVar(&treeOS, "override-os", "", "override OS (e.g. linux)")
	treeCmd.Flags().StringVar(&treeArch, "override-arch", "", "override architecture (e.g. arm64, amd64)")
	treeCmd.Flags().StringVar(&treeVariant, "override-variant", "", "override variant (e.g. v8)")
	treeCmd.Flags().BoolVar(&treeInsecure, "insecure", false, "skip TLS certificate verification")
	treeCmd.Flags().BoolVar(&treeInsecure, "src-insecure", false, "skip TLS certificate verification for source registry")
	treeCmd.Flags().BoolVar(&treeDebug, "debug", false, "enable verbose output")
	rootCmd.AddCommand(treeCmd)
}

func runTree(c *cobra.Command, args []string) error {
	if treeDebug {
		logging.Setup(slog.LevelDebug)
	}

	img, err := loadImage(args[0], treeCreds, treeInsecure, treeOS, treeArch, treeVariant)
	if err != nil {
		return err
	}

	entries, err := image.CollectTree(img, args[1])
	if err != nil {
		if errors.Is(err, image.ErrPathNotFound) || errors.Is(err, image.ErrEmptyImage) {
			if treeDebug {
				slog.Warn("path not found in image", "path", args[1])
			}
			return nil
		}
		return err
	}

	return writeTree(c.OutOrStdout(), args[1], entries, isTTYWriter(c.OutOrStdout()))
}
