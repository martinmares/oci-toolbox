package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/spf13/cobra"
)

var releaseConvertCmd = newReleaseConvertCommand()

func newReleaseConvertCommand() *cobra.Command {
	var output, releaseID string
	var selectors []string
	cmd := &cobra.Command{
		Use:   "convert RELEASE_MANIFEST",
		Short: "Convert a supported release manifest to the internal images format (offline)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer file.Close()
			manifest, err := bundle.DecodeReleaseDocument(file)
			if err != nil {
				return err
			}
			if releaseID != "" {
				manifest.ReleaseID = releaseID
			}
			seen := map[string]bool{}
			for _, override := range selectors {
				id, selector, ok := strings.Cut(override, "=")
				app, container, hasContainer := strings.Cut(selector, "/")
				if !ok || id == "" || app == "" || (hasContainer && (container == "" || strings.Contains(container, "/"))) {
					return fmt.Errorf("invalid --selector %q: expected image-id=app[/container]", override)
				}
				if seen[id] {
					return fmt.Errorf("duplicate --selector for %q", id)
				}
				seen[id] = true
				found := false
				for i := range manifest.Images {
					if manifest.Images[i].ID == id {
						manifest.Images[i].AppName = app
						manifest.Images[i].ContainerName = container
						found = true
					}
				}
				if !found {
					return fmt.Errorf("unknown image ID in --selector: %q", id)
				}
			}
			if err := manifest.Validate(); err != nil {
				return err
			}
			return writeReleaseManifest(cmd, output, manifest)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "-", "internal release YAML output path, or - for stdout; existing files are never overwritten")
	cmd.Flags().StringVar(&releaseID, "release-id", "", "override the output release identifier (image tags stay unchanged)")
	cmd.Flags().StringArrayVar(&selectors, "selector", nil, "override a deployment selector: image-id=app[/container] (repeatable; frontend IDs are frontend-<key>)")
	return cmd
}
