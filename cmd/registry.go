package cmd

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/martinmares/oci-toolbox/internal/cleanup"
	"github.com/martinmares/oci-toolbox/internal/registry"
	"github.com/martinmares/oci-toolbox/internal/registryconfig"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type rootOptions struct {
	configPath    string
	apply         bool
	repoFilter    string
	ignoreMissing bool
	timeout       time.Duration
	insecure      bool
	caCertPath    string
	credentials   string
	copyPlan      copyPlanOptions
}

type cleanupSummary struct {
	Repositories        int
	RepositoriesFailed  int
	RepositoriesMissing int
	TagsScanned         int
	ProtectedManifests  int
	KeptManifests       int
	RemoveManifests     int
	RemoveTags          int
	TotalBytes          int64
	ReferencedBytes     int64
	ReclaimableBytes    int64
	RemainingBytes      int64
}

type infoSummary struct {
	Images       int
	UniqueImages map[string]int64
	TotalBytes   int64
	Missing      int
	Failed       int
}

type copyPlanOptions struct {
	DestRegistry string
	SrcRegistry  string
	DestPrefix   string
	Format       string
	Output       string
}

func newRegistryCommand() *cobra.Command {
	opts := rootOptions{}

	cmd := &cobra.Command{
		Use:           "registry",
		Short:         "Inspect and maintain OCI registries",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	cmd.PersistentFlags().StringVarP(&opts.configPath, "config", "c", "oci-toolbox-registry.yml", "registry maintenance YAML configuration")
	cmd.PersistentFlags().DurationVar(&opts.timeout, "timeout", 60*time.Second, "registry HTTP timeout")
	cmd.PersistentFlags().BoolVar(&opts.insecure, "insecure", false, "skip registry TLS certificate verification")
	cmd.PersistentFlags().StringVar(&opts.caCertPath, "ca-cert", "", "path to an additional PEM CA certificate")
	cmd.PersistentFlags().StringVar(&opts.credentials, "creds", "", "registry credentials user:password")

	cmd.AddCommand(newCleanupCommand(&opts))
	cmd.AddCommand(newCopyPlanCommand(&opts))
	cmd.AddCommand(newInfoCommand(&opts))
	cmd.AddCommand(newListRepositoriesCommand(&opts))

	return cmd
}

func init() {
	rootCmd.AddCommand(newRegistryCommand())
}

func newCopyPlanCommand(opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "copy-plan",
		Aliases: []string{"copy-batch"},
		Short:   "Generate oci-toolbox copy commands or batch YAML for configured repositories",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCopyPlan(cmd.Context(), *opts)
		},
	}
	cmd.Flags().StringVar(&opts.repoFilter, "repo", "", "Optional comma-separated repository allowlist.")
	cmd.Flags().BoolVar(&opts.ignoreMissing, "ignore-missing", false, "Skip missing repositories without per-repository error logs.")
	cmd.Flags().StringVar(&opts.copyPlan.DestRegistry, "dest-registry", "", "Destination registry host/prefix, for example artifactory.example.com")
	cmd.Flags().StringVar(&opts.copyPlan.SrcRegistry, "src-registry", "", "Source registry host/prefix override. Defaults to registry.url host/path from config.")
	cmd.Flags().StringVar(&opts.copyPlan.DestPrefix, "dest-prefix", "", "Optional destination repository prefix.")
	cmd.Flags().StringVar(&opts.copyPlan.Format, "format", "batch", "Output format: batch or commands.")
	cmd.Flags().StringVarP(&opts.copyPlan.Output, "output", "o", "", "Write output to file instead of stdout.")
	return cmd
}

func newCleanupCommand(opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Clean repositories from config using the configured retention mode",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCleanup(cmd.Context(), *opts)
		},
	}
	cmd.Flags().BoolVar(&opts.apply, "apply", false, "delete selected manifests; default is dry-run")
	cmd.Flags().StringVar(&opts.repoFilter, "repo", "", "Optional comma-separated repository allowlist.")
	cmd.Flags().BoolVar(&opts.ignoreMissing, "ignore-missing", false, "Skip missing repositories without per-repository error logs.")
	return cmd
}

func runCleanup(ctx context.Context, opts rootOptions) error {
	cfg, client, err := loadConfigAndClient(opts)
	if err != nil {
		return err
	}
	protectPatterns, err := compileProtectPatterns(cfg.Cleanup.ProtectTagPatterns)
	if err != nil {
		return err
	}
	if cfg.Cleanup.KeepCount < 1 {
		return fmt.Errorf("cleanup.keep_count must be >= 1")
	}
	if cfg.Cleanup.Mode != "timestamp" && cfg.Cleanup.Mode != "created" {
		return fmt.Errorf("cleanup.mode must be timestamp or created")
	}
	if len(cfg.Repositories) == 0 {
		return fmt.Errorf("repositories must contain at least one repository")
	}

	allowedRepos := parseRepoFilter(opts.repoFilter)
	dryRun := !opts.apply
	summary := cleanupSummary{}
	totalBlobs := map[string]int64{}
	preservedBlobs := map[string]int64{}
	removeBlobs := map[string]int64{}

	for _, repo := range cfg.Repositories {
		if repo.Name == "" {
			continue
		}
		if len(allowedRepos) > 0 && !allowedRepos[repo.Name] {
			continue
		}
		repoSummary, err := cleanupRepository(ctx, client, cfg, repo.Name, dryRun, protectPatterns, totalBlobs, preservedBlobs, removeBlobs)
		if err != nil {
			summary.Repositories++
			if opts.ignoreMissing && registry.IsNameUnknown(err) {
				summary.RepositoriesMissing++
				continue
			}
			summary.RepositoriesFailed++
			log.Printf("repo=%s error=%v", repo.Name, err)
			continue
		}
		summary.add(repoSummary)
	}

	for _, size := range totalBlobs {
		summary.TotalBytes += size
	}
	for digest, size := range removeBlobs {
		summary.ReferencedBytes += size
		if _, kept := preservedBlobs[digest]; !kept {
			summary.ReclaimableBytes += size
		}
	}
	summary.RemainingBytes = summary.TotalBytes - summary.ReclaimableBytes
	printSummary(summary, dryRun)

	return nil
}

func runCopyPlan(ctx context.Context, opts rootOptions) error {
	cfg, client, err := loadConfigAndClient(opts)
	if err != nil {
		return err
	}
	if len(cfg.Repositories) == 0 {
		return fmt.Errorf("repositories must contain at least one repository")
	}
	if opts.copyPlan.DestRegistry == "" {
		return fmt.Errorf("--dest-registry is required")
	}
	if opts.copyPlan.Format != "batch" && opts.copyPlan.Format != "commands" {
		return fmt.Errorf("--format must be batch or commands")
	}

	srcRegistry := opts.copyPlan.SrcRegistry
	if srcRegistry == "" {
		srcRegistry, err = registryPrefixFromURL(cfg.Registry.URL)
		if err != nil {
			return err
		}
	}

	allowedRepos := parseRepoFilter(opts.repoFilter)
	batch := copyBatch{}
	missing := 0
	failed := 0
	for _, repo := range cfg.Repositories {
		repoName := strings.Trim(repo.Name, "/")
		if repoName == "" {
			continue
		}
		if len(allowedRepos) > 0 && !allowedRepos[repo.Name] && !allowedRepos[repoName] {
			continue
		}
		tags, err := client.ListTags(ctx, repoName)
		if err != nil {
			if opts.ignoreMissing && registry.IsNameUnknown(err) {
				missing++
				continue
			}
			failed++
			log.Printf("repo=%s error=%v", repoName, err)
			continue
		}
		groups := map[string][]string{}
		for _, tag := range tags {
			info, err := client.ManifestInfo(ctx, repoName, tag)
			if err != nil {
				failed++
				log.Printf("repo=%s tag=%s resolve manifest failed: %v", repoName, tag, err)
				continue
			}
			groups[info.Digest] = append(groups[info.Digest], tag)
		}
		copyGroups := make([]copyTagGroup, 0, len(groups))
		for _, tags := range groups {
			primary, additional := copyPlanTags(tags)
			copyGroups = append(copyGroups, copyTagGroup{
				PrimaryTag:     primary,
				AdditionalTags: additional,
			})
		}
		sort.Slice(copyGroups, func(i, j int) bool {
			return copyGroups[i].PrimaryTag > copyGroups[j].PrimaryTag
		})
		for _, group := range copyGroups {
			batch.Copy = append(batch.Copy, copyBatchItem{
				Src:      imageRef(srcRegistry, repoName, group.PrimaryTag),
				Dest:     imageRef(opts.copyPlan.DestRegistry, destRepository(opts.copyPlan.DestPrefix, repoName), group.PrimaryTag),
				DestTags: group.AdditionalTags,
			})
		}
	}

	output, err := renderCopyPlan(batch, opts.copyPlan)
	if err != nil {
		return err
	}
	if missing > 0 || failed > 0 {
		output += fmt.Sprintf("\n# copy-plan summary: missing=%d failed=%d\n", missing, failed)
	}
	if opts.copyPlan.Output != "" {
		return os.WriteFile(opts.copyPlan.Output, []byte(output), 0o644)
	}
	fmt.Print(output)
	return nil
}

func newInfoCommand(opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show tag, digest, image-created, and size metadata for configured repositories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInfo(cmd.Context(), *opts)
		},
	}
	cmd.Flags().StringVar(&opts.repoFilter, "repo", "", "Optional comma-separated repository allowlist.")
	cmd.Flags().BoolVar(&opts.ignoreMissing, "ignore-missing", false, "Skip missing repositories without per-repository error logs.")
	return cmd
}

func runInfo(ctx context.Context, opts rootOptions) error {
	cfg, client, err := loadConfigAndClient(opts)
	if err != nil {
		return err
	}
	if len(cfg.Repositories) == 0 {
		return fmt.Errorf("repositories must contain at least one repository")
	}
	protectPatterns, err := compileProtectPatterns(cfg.Cleanup.ProtectTagPatterns)
	if err != nil {
		return err
	}

	allowedRepos := parseRepoFilter(opts.repoFilter)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REPOSITORY\tTAG\tTIMESTAMP\tPROTECTED\tDIGEST\tCREATED\tSIZE")
	summary := infoSummary{UniqueImages: map[string]int64{}}
	for _, repo := range cfg.Repositories {
		if repo.Name == "" {
			continue
		}
		if len(allowedRepos) > 0 && !allowedRepos[repo.Name] {
			continue
		}
		if err := printRepoInfo(ctx, w, client, cfg, repo.Name, protectPatterns, &summary); err != nil {
			if opts.ignoreMissing && registry.IsNameUnknown(err) {
				summary.Missing++
				continue
			}
			summary.Failed++
			log.Printf("repo=%s error=%v", repo.Name, err)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	printInfoSummary(summary)
	return nil
}

func newListRepositoriesCommand(opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list-repositories [PREFIX]",
		Aliases: []string{"repos"},
		Short:   "List repositories from registry catalog, optionally filtered by prefix",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := ""
			if len(args) > 0 {
				prefix = strings.Trim(args[0], "/")
			}
			return runListRepositories(cmd.Context(), *opts, prefix)
		},
	}
	return cmd
}

func runListRepositories(ctx context.Context, opts rootOptions, prefix string) error {
	_, client, err := loadConfigAndClient(opts)
	if err != nil {
		return err
	}

	repositories, err := client.ListRepositories(ctx)
	if err != nil {
		return fmt.Errorf("list repositories: %w", err)
	}
	sort.Strings(repositories)

	fmt.Println("repositories:")
	for _, repo := range repositories {
		repo = strings.Trim(repo, "/")
		if prefix != "" && !strings.HasPrefix(repo, prefix) {
			continue
		}
		fmt.Printf("  - name: %s\n", repo)
	}

	return nil
}

func renderCopyPlan(batch copyBatch, opts copyPlanOptions) (string, error) {
	switch opts.Format {
	case "commands":
		var builder strings.Builder
		for _, item := range batch.Copy {
			builder.WriteString("oci-toolbox")
			builder.WriteString(" copy ")
			for _, tag := range item.DestTags {
				builder.WriteString("--dest-tag ")
				builder.WriteString(shellQuote(tag))
				builder.WriteByte(' ')
			}
			builder.WriteString(shellQuote(item.Src))
			builder.WriteByte(' ')
			builder.WriteString(shellQuote(item.Dest))
			builder.WriteByte('\n')
		}
		return builder.String(), nil
	case "batch":
		data, err := yaml.Marshal(batch)
		if err != nil {
			return "", err
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("unsupported copy plan format %q", opts.Format)
	}
}

type copyTagGroup struct {
	PrimaryTag     string
	AdditionalTags []string
}

func copyPlanTags(tags []string) (string, []string) {
	tags = append([]string(nil), tags...)
	sort.Sort(sort.Reverse(sort.StringSlice(tags)))
	if len(tags) == 0 {
		return "", nil
	}
	return tags[0], tags[1:]
}

func registryPrefixFromURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil {
		return "", err
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("registry.url must include host")
	}
	prefix := parsed.Host + strings.Trim(parsed.Path, "/")
	if parsed.Path != "" && strings.Trim(parsed.Path, "/") != "" {
		prefix = parsed.Host + "/" + strings.Trim(parsed.Path, "/")
	}
	return strings.Trim(prefix, "/"), nil
}

func imageRef(registryPrefix, repository, tag string) string {
	return strings.Trim(registryPrefix, "/") + "/" + strings.Trim(repository, "/") + ":" + tag
}

func destRepository(prefix, repository string) string {
	repository = strings.Trim(repository, "/")
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return repository
	}
	return prefix + "/" + repository
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			!strings.ContainsRune("@%_+=:,./-", r)
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func loadConfigAndClient(opts rootOptions) (registryconfig.Config, *registry.Client, error) {
	cfg, err := registryconfig.Load(opts.configPath)
	if err != nil {
		return registryconfig.Config{}, nil, fmt.Errorf("load config: %w", err)
	}
	if opts.insecure {
		cfg.Registry.TLS.Insecure = true
	}
	if opts.caCertPath != "" {
		cfg.Registry.TLS.CACert = opts.caCertPath
	}
	if opts.credentials != "" {
		username, password, ok := strings.Cut(opts.credentials, ":")
		if !ok || username == "" {
			return registryconfig.Config{}, nil, fmt.Errorf("--creds must use user:password format")
		}
		cfg.Registry.Username = username
		cfg.Registry.Password = password
	}

	client, err := registry.NewClient(registry.Config{
		BaseURL:            cfg.Registry.URL,
		Username:           cfg.Registry.Username,
		Password:           cfg.Registry.Password,
		Timeout:            opts.timeout,
		InsecureSkipVerify: cfg.Registry.TLS.Insecure,
		CACertPath:         cfg.Registry.TLS.CACert,
	})
	if err != nil {
		return registryconfig.Config{}, nil, fmt.Errorf("registry client: %w", err)
	}

	return cfg, client, nil
}

func cleanupRepository(ctx context.Context, client *registry.Client, cfg registryconfig.Config, repoName string, dryRun bool, protectPatterns []*regexp.Regexp, totalBlobs, preservedBlobs, removeBlobs map[string]int64) (cleanupSummary, error) {
	summary := cleanupSummary{Repositories: 1}
	fmt.Printf("\n=== %s ===\n", repoName)

	tags, err := client.ListTags(ctx, repoName)
	if err != nil {
		return summary, fmt.Errorf("list tags: %w", err)
	}
	summary.TagsScanned = len(tags)

	records := make([]cleanup.TagRecord, 0, len(tags))
	for _, tag := range tags {
		info, err := client.ManifestInfo(ctx, repoName, tag)
		if err != nil {
			log.Printf("repo=%s tag=%s resolve manifest failed: %v", repoName, tag, err)
			continue
		}
		record := cleanup.NewTagRecord(tag, info.Digest, cfg.Cleanup.TagPattern)
		record.Blobs = blobRecords(info.Blobs)
		record.Created = info.Created
		record.Protected = isProtectedTag(tag, protectPatterns)
		addRecordBlobs(record, totalBlobs)
		records = append(records, record)
	}

	plan := buildCleanupPlan(cfg.Cleanup.Mode, records, cfg.Cleanup.KeepCount, cfg.Cleanup.ProtectLatest)
	summary.ProtectedManifests = len(plan.Latest)
	summary.KeptManifests = len(plan.Keep)
	summary.RemoveManifests = len(plan.Remove)
	printDigestGroups("latest/protected", plan.Latest)
	printInvalidTags("invalid/ignored", plan.Invalid)
	printDigestGroups("keep images", plan.Keep)
	printDigestGroups("remove images", plan.Remove)

	for _, group := range append(plan.Latest, plan.Keep...) {
		addGroupBlobs(group, preservedBlobs)
	}
	for _, tag := range plan.Invalid {
		addRecordBlobs(tag, preservedBlobs)
	}
	for _, group := range plan.Remove {
		summary.RemoveTags += len(group.Tags)
		addGroupBlobs(group, removeBlobs)
		action := "dry-run"
		if !dryRun {
			action = "delete"
			if err := client.DeleteManifest(ctx, repoName, group.Digest); err != nil {
				return summary, fmt.Errorf("delete digest=%s tags=%s: %w", group.Digest, formatTagNames(group.Tags), err)
			}
		}
		fmt.Printf("%-8s %s <- %s\n", action, shortDigest(group.Digest), formatTagLabel(group.Tags))
	}

	return summary, nil
}

func printDigestGroups(title string, groups []cleanup.DigestGroup) {
	if len(groups) == 0 {
		return
	}
	fmt.Printf("\n%s:\n", title)
	for _, group := range groups {
		fmt.Printf("  %s <- %s\n", shortDigest(group.Digest), formatTagLabel(group.Tags))
	}
}

func buildCleanupPlan(mode string, records []cleanup.TagRecord, keepCount int, protectLatest bool) cleanup.Plan {
	switch mode {
	case "created":
		return cleanup.BuildCreatedPlan(records, keepCount, protectLatest)
	default:
		return cleanup.BuildTimestampPlan(records, keepCount, protectLatest)
	}
}

func printInvalidTags(title string, tags []cleanup.TagRecord) {
	if len(tags) == 0 {
		return
	}
	fmt.Printf("\n%s:\n", title)
	for _, tag := range tags {
		fmt.Printf("  %-32s %s\n", tag.Name, shortDigest(tag.Digest))
	}
}

func printRepoInfo(ctx context.Context, w *tabwriter.Writer, client *registry.Client, cfg registryconfig.Config, repoName string, protectPatterns []*regexp.Regexp, summary *infoSummary) error {
	tags, err := client.ListTags(ctx, repoName)
	if err != nil {
		return fmt.Errorf("list tags: %w", err)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		info, err := client.ManifestInfo(ctx, repoName, tag)
		if err != nil {
			log.Printf("repo=%s tag=%s resolve manifest failed: %v", repoName, tag, err)
			continue
		}
		record := cleanup.NewTagRecord(tag, info.Digest, cfg.Cleanup.TagPattern)
		protected := tag == "latest" || isProtectedTag(tag, protectPatterns)
		size := manifestBlobSize(info.Blobs)
		summary.Images++
		summary.TotalBytes += size
		if _, ok := summary.UniqueImages[info.Digest]; !ok {
			summary.UniqueImages[info.Digest] = size
		}
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			strings.Trim(repoName, "/"),
			tag,
			boolText(record.Valid),
			boolText(protected),
			shortDigestForInfo(info.Digest),
			formatCreated(info.Created),
			formatBytes(size),
		)
	}
	return nil
}

func parseRepoFilter(value string) map[string]bool {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	result := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result[item] = true
		}
	}
	return result
}

func compileProtectPatterns(patterns []string) ([]*regexp.Regexp, error) {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile cleanup.protect_tag_patterns %q: %w", pattern, err)
		}
		result = append(result, re)
	}
	return result, nil
}

func isProtectedTag(tag string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(tag) {
			return true
		}
	}
	return false
}

func shortDigest(digest string) string {
	return strings.TrimPrefix(digest, "sha256:")
}

func shortDigestForInfo(digest string) string {
	digest = shortDigest(digest)
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func formatTagLabel(tags []cleanup.TagRecord) string {
	if len(tags) == 1 {
		return "tag " + tags[0].Name
	}
	return "tagy " + formatTagNames(tags)
}

func formatTagNames(tags []cleanup.TagRecord) string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	return strings.Join(names, ", ")
}

func blobRecords(blobs []registry.BlobInfo) []cleanup.BlobRecord {
	records := make([]cleanup.BlobRecord, 0, len(blobs))
	seen := map[string]bool{}
	for _, blob := range blobs {
		if blob.Digest == "" || blob.Size <= 0 || seen[blob.Digest] {
			continue
		}
		seen[blob.Digest] = true
		records = append(records, cleanup.BlobRecord{Digest: blob.Digest, Size: blob.Size})
	}
	return records
}

func manifestBlobSize(blobs []registry.BlobInfo) int64 {
	var total int64
	seen := map[string]bool{}
	for _, blob := range blobs {
		if blob.Digest == "" || blob.Size <= 0 || seen[blob.Digest] {
			continue
		}
		seen[blob.Digest] = true
		total += blob.Size
	}
	return total
}

func addGroupBlobs(group cleanup.DigestGroup, blobs map[string]int64) {
	for _, tag := range group.Tags {
		addRecordBlobs(tag, blobs)
	}
}

func addRecordBlobs(tag cleanup.TagRecord, blobs map[string]int64) {
	for _, blob := range tag.Blobs {
		if blob.Digest == "" || blob.Size <= 0 {
			continue
		}
		if _, ok := blobs[blob.Digest]; !ok {
			blobs[blob.Digest] = blob.Size
		}
	}
}

func (s *cleanupSummary) add(other cleanupSummary) {
	s.Repositories += other.Repositories
	s.RepositoriesFailed += other.RepositoriesFailed
	s.RepositoriesMissing += other.RepositoriesMissing
	s.TagsScanned += other.TagsScanned
	s.ProtectedManifests += other.ProtectedManifests
	s.KeptManifests += other.KeptManifests
	s.RemoveManifests += other.RemoveManifests
	s.RemoveTags += other.RemoveTags
}

func printSummary(summary cleanupSummary, dryRun bool) {
	fmt.Println("\n=== summary ===")
	fmt.Printf("repositories: scanned=%d missing=%d failed=%d\n", summary.Repositories, summary.RepositoriesMissing, summary.RepositoriesFailed)
	fmt.Printf("tags scanned: %d\n", summary.TagsScanned)
	fmt.Printf("manifests: protected=%d keep=%d remove=%d\n", summary.ProtectedManifests, summary.KeptManifests, summary.RemoveManifests)
	fmt.Printf("remove tags: %d\n", summary.RemoveTags)
	fmt.Printf("current unique blobs size: %s\n", formatBytes(summary.TotalBytes))
	fmt.Printf("remove blobs referenced: %s\n", formatBytes(summary.ReferencedBytes))
	fmt.Printf("estimated space saved: %s\n", formatBytes(summary.ReclaimableBytes))
	fmt.Printf("estimated size after cleanup: %s\n", formatBytes(summary.RemainingBytes))
	if dryRun {
		fmt.Println("mode: dry-run")
	} else {
		fmt.Println("mode: delete")
	}
}

func printInfoSummary(summary infoSummary) {
	var uniqueBytes int64
	for _, size := range summary.UniqueImages {
		uniqueBytes += size
	}

	fmt.Println("\n=== info summary ===")
	fmt.Printf("unique images: %d\n", len(summary.UniqueImages))
	fmt.Printf("all images: %d\n", summary.Images)
	fmt.Printf("unique images size: %s\n", formatBytes(uniqueBytes))
	fmt.Printf("all images size: %s\n", formatBytes(summary.TotalBytes))
	if summary.Missing > 0 || summary.Failed > 0 {
		fmt.Printf("repositories: missing=%d failed=%d\n", summary.Missing, summary.Failed)
	}
}

func formatBytes(size int64) string {
	if size < 0 {
		size = 0
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", size, units[unit])
	}
	if math.Abs(value) >= 10 {
		return fmt.Sprintf("%.1f %s", value, units[unit])
	}
	return fmt.Sprintf("%.2f %s", value, units[unit])
}

func formatCreated(created *time.Time) string {
	if created == nil {
		return "-"
	}
	return created.UTC().Format(time.RFC3339)
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
