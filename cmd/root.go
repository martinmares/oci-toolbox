package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/martinmares/oci-toolbox/internal/logging"
)

const defaultVersion = "0.1.0"

var (
	version = defaultVersion
	commit  = "unknown"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "oci-toolbox",
	Short: "Toolbox for inspecting, transferring, and modifying OCI images",
	Long: `oci-toolbox inspects, transfers, and modifies OCI images, maintains
registries, and snapshots Kubernetes deployment images without requiring a Docker daemon.`,
	// --version flag (skopeo-compatible: prints "oci-toolbox version X.Y.Z")
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
	rootCmd.SetVersionTemplate("oci-toolbox version {{.Version}}\n")
	logging.Setup(slog.LevelInfo)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
