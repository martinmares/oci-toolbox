package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/martinmares/oci-patch/internal/logging"
)

const defaultVersion = "0.1.0"

var (
	version = defaultVersion
	commit  = "unknown"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "oci-patch",
	Short: "OCI image transfer and patching tool",
	Long: `oci-patch copies OCI images between registries and applies recipes
to modify the image in-flight — without requiring a Docker daemon.`,
	// --version flag (skopeo-compatible: prints "oci-patch version X.Y.Z")
	Version:      version,
	SilenceUsage: true,
}

func SetVersion(value string) {
	if value == "" || version != defaultVersion {
		return
	}
	version = value
	rootCmd.Version = value
}

func init() {
	rootCmd.SetVersionTemplate("oci-patch version {{.Version}}\n")
	logging.Setup(slog.LevelInfo)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
