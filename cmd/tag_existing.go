package cmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"

	"github.com/martinmares/oci-patch/internal/auth"
)

var (
	tagExistingCreds    string
	tagExistingInsecure bool
)

var tagExistingCmd = &cobra.Command{
	Use:   "tag-existing SOURCE_BY_DIGEST DEST_TAG",
	Short: "Add a tag to an existing manifest already present in the target repository",
	Args:  cobra.ExactArgs(2),
	Example: `  oci-patch tag-existing docker://registry.example.com/app@sha256:abcd docker://registry.example.com/app:release-1
  oci-patch tag-existing --creds user:pass docker://registry.example.com/app@sha256:abcd docker://registry.example.com/app:release-2`,
	RunE: runTagExisting,
}

func init() {
	tagExistingCmd.Flags().StringVar(&tagExistingCreds, "creds", "", "registry credentials user:password")
	tagExistingCmd.Flags().BoolVar(&tagExistingInsecure, "insecure", false, "skip TLS certificate verification")
	rootCmd.AddCommand(tagExistingCmd)
}

func runTagExisting(_ *cobra.Command, args []string) error {
	sourceRef, err := name.ParseReference(stripScheme(args[0]))
	if err != nil {
		return fmt.Errorf("invalid source reference: %w", err)
	}
	if !strings.Contains(args[0], "@sha256:") {
		return fmt.Errorf("source reference must use a digest (@sha256:...)")
	}

	destinationTag, err := name.NewTag(stripScheme(args[1]), name.WeakValidation)
	if err != nil {
		return fmt.Errorf("invalid destination tag: %w", err)
	}

	if sourceRef.Context().Name() != destinationTag.Context().Name() {
		return fmt.Errorf("source and destination must point to the same repository")
	}

	opts := auth.RemoteOptions(parseCreds(tagExistingCreds))
	if tagExistingInsecure {
		opts = append(opts, auth.InsecureTransport())
	}

	desc, err := remote.Get(sourceRef, opts...)
	if err != nil {
		return fmt.Errorf("fetching existing manifest: %w", err)
	}

	if err := remote.Tag(destinationTag, desc, opts...); err != nil {
		return fmt.Errorf("tagging existing manifest: %w", err)
	}

	slog.Info("tagged existing manifest", "source", sourceRef.String(), "destination", destinationTag.String())
	return nil
}
