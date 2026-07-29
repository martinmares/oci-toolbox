package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/martinmares/oci-toolbox/internal/kubeimages"
	"github.com/martinmares/oci-toolbox/internal/snapshot"
)

type snapshotOptions struct {
	namespace  string
	outputDir  string
	kubeconfig string
	context    string
	kubeUnsafe bool
	srcCreds   string
	destCreds  string
	srcUnsafe  bool
	destUnsafe bool
}

type directCopier struct{}

func (directCopier) Copy(ctx context.Context, source, destination string, options snapshot.CopyOptions) error {
	return runCopyOne(ctx, source, destination, "", nil, copyRunOptions{
		SrcCreds:       options.SrcCreds,
		DestCreds:      options.DestCreds,
		SrcInsecure:    options.SrcInsecure,
		DestInsecure:   options.DestInsecure,
		Progress:       options.Progress,
		ProgressJSON:   false,
		RecipePaths:    nil,
		Reduce:         false,
		ReduceChanges:  false,
		WrapEntrypoint: false,
	})
}

func newSnapshotCommand() *cobra.Command {
	opts := &snapshotOptions{}
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Back up and restore images used by Kubernetes deployments",
	}
	cmd.PersistentFlags().StringVarP(&opts.namespace, "namespace", "n", "", "Kubernetes namespace")
	cmd.AddCommand(newSnapshotCreateCommand(opts), newSnapshotRestoreCommand(opts))
	return cmd
}

func newSnapshotCreateCommand(opts *snapshotOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [IMAGE_GLOB]",
		Short: "Create a timestamped snapshot of deployment and init-container images",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.namespace == "" {
				return fmt.Errorf("--namespace is required")
			}
			client, err := kubeimages.New(kubeimages.Options{
				Kubeconfig: opts.kubeconfig,
				Context:    opts.context,
				Insecure:   opts.kubeUnsafe,
			})
			if err != nil {
				return err
			}
			pattern := ""
			if len(args) == 1 {
				pattern = args[0]
			}
			manifest, err := snapshot.Backup(cmd.Context(), client, directCopier{}, snapshot.BackupOptions{
				Namespace: opts.namespace, OutputDir: opts.outputDir, ImagePattern: pattern,
				SrcCreds: opts.srcCreds, SrcInsecure: opts.srcUnsafe,
				Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Snapshot complete: %s\n", manifest)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.outputDir, "output-dir", ".", "directory in which to create the snapshot")
	cmd.Flags().StringVar(&opts.kubeconfig, "kubeconfig", "", "kubeconfig path; defaults to KUBECONFIG or ~/.kube/config")
	cmd.Flags().StringVar(&opts.context, "context", "", "Kubernetes context override")
	cmd.Flags().BoolVar(&opts.kubeUnsafe, "kube-insecure", false, "skip Kubernetes API TLS certificate verification")
	cmd.Flags().StringVar(&opts.srcCreds, "src-creds", "", "source registry credentials user:password")
	cmd.Flags().BoolVar(&opts.srcUnsafe, "src-insecure", false, "skip source registry TLS certificate verification")
	return cmd
}

func newSnapshotRestoreCommand(opts *snapshotOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore MANIFEST",
		Short: "Restore all images described by a completed snapshot manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.namespace == "" {
				return fmt.Errorf("--namespace is required")
			}
			if err := snapshot.Restore(cmd.Context(), directCopier{}, snapshot.RestoreOptions{
				ManifestPath: args[0], Namespace: opts.namespace,
				DestCreds: opts.destCreds, DestInsecure: opts.destUnsafe,
				Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Restore complete: %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.destCreds, "dest-creds", "", "destination registry credentials user:password")
	cmd.Flags().BoolVar(&opts.destUnsafe, "dest-insecure", false, "skip destination registry TLS certificate verification")
	return cmd
}

func init() {
	rootCmd.AddCommand(newSnapshotCommand())
}
