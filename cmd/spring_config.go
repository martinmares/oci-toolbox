package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/martinmares/oci-toolbox/internal/logging"
	"github.com/martinmares/oci-toolbox/internal/springconfig"
)

type springConfigDiscoverOptions struct {
	output        string
	classpathFile string
	mainClassFile string
	include       []string
	creds         string
	os            string
	arch          string
	variant       string
	insecure      bool
	progress      bool
	debug         bool
}

var springConfigCmd = &cobra.Command{
	Use:   "spring-config",
	Short: "Analyze Spring Boot configuration packaged in OCI images",
}

func init() {
	var opts springConfigDiscoverOptions
	discoverCmd := &cobra.Command{
		Use:   "discover IMAGE",
		Short: "Extract Spring configuration candidates from a JIB application image",
		Args:  cobra.ExactArgs(1),
		Example: `  oci-toolbox spring-config discover \
    docker://registry.example.com/team/application:2.4 \
    --output ./spring-config-analysis \
    --progress \
    --src-insecure`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSpringConfigDiscover(cmd, args[0], opts)
		},
	}
	discoverCmd.Flags().StringVarP(&opts.output, "output", "o", "", "output directory for extracted files and manifest.yml")
	discoverCmd.Flags().StringVar(&opts.classpathFile, "classpath-file", springconfig.DefaultClasspathFile, "path to the JIB classpath file inside the image")
	discoverCmd.Flags().StringVar(&opts.mainClassFile, "main-class-file", springconfig.DefaultMainClassFile, "path to the JIB main class file inside the image")
	discoverCmd.Flags().StringArrayVar(&opts.include, "include", nil, "additional archive path or basename glob to extract (may be repeated)")
	discoverCmd.Flags().StringVar(&opts.creds, "creds", "", "registry credentials user:password")
	discoverCmd.Flags().StringVar(&opts.os, "override-os", "", "override OS (e.g. linux)")
	discoverCmd.Flags().StringVar(&opts.arch, "override-arch", "", "override architecture (e.g. arm64, amd64)")
	discoverCmd.Flags().StringVar(&opts.variant, "override-variant", "", "override variant (e.g. v8)")
	discoverCmd.Flags().BoolVar(&opts.insecure, "insecure", false, "skip TLS certificate verification")
	discoverCmd.Flags().BoolVar(&opts.insecure, "src-insecure", false, "skip TLS certificate verification for source registry")
	discoverCmd.Flags().BoolVar(&opts.progress, "progress", false, "show discovery phase progress")
	discoverCmd.Flags().BoolVar(&opts.debug, "debug", false, "enable verbose output")

	springConfigCmd.AddCommand(discoverCmd)
	rootCmd.AddCommand(springConfigCmd)
}

func runSpringConfigDiscover(cmd *cobra.Command, imageRef string, opts springConfigDiscoverOptions) error {
	if opts.output == "" {
		return fmt.Errorf("--output is required")
	}
	if opts.debug {
		logging.Setup(slog.LevelDebug)
	}
	var reporter func(string)
	if opts.progress {
		reporter = func(message string) {
			fmt.Fprintf(cmd.ErrOrStderr(), "phase: %s\n", message)
		}
		reporter("loading image")
	}
	img, err := loadImage(imageRef, opts.creds, opts.insecure, opts.os, opts.arch, opts.variant)
	if err != nil {
		return err
	}
	manifest, err := springconfig.Discover(img, imageRef, opts.output, springconfig.Options{
		ClasspathFile: opts.classpathFile,
		MainClassFile: opts.mainClassFile,
		Include:       opts.include,
		Reporter:      reporter,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(
		cmd.OutOrStdout(),
		"Discovered %d Spring configuration files in %s\nManifest: %s/manifest.yml\nMap: %s/manifest.svg\n",
		len(manifest.Configs),
		opts.output,
		opts.output,
		opts.output,
	)
	return nil
}
