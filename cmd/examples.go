package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

var examplesCmd = &cobra.Command{
	Use:     "examples",
	Aliases: []string{"example"},
	Short:   "Show common commands and YAML file examples",
	Args:    cobra.NoArgs,
	RunE: func(c *cobra.Command, _ []string) error {
		return writeExamples(c.OutOrStdout(), isTTYWriter(c.OutOrStdout()))
	},
}

func init() {
	rootCmd.AddCommand(examplesCmd)
}

type exampleFile struct {
	Name    string
	Format  string
	Content string
}

type commandExample struct {
	Title       string
	Description string
	Shell       string
	Files       []exampleFile
}

type commandExamples struct {
	Command     string
	Description string
	Examples    []commandExample
}

var examplesCatalog = []commandExamples{
	{
		Command:     "delete-tag",
		Description: "Remove one explicit tag; preview by default, never fall back to digest deletion.",
		Examples: []commandExample{
			{Title: "Preview one tag", Shell: "oci-toolbox delete-tag registry.example.com/team/app:bad-release"},
			{Title: "Delete the reviewed tag", Description: "--apply sends DELETE; --expect-digest refuses if the tag points to another manifest.", Shell: "oci-toolbox delete-tag registry.example.com/team/app:bad-release --expect-digest sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa --apply"},
			{Title: "Artifactory storage API preview", Description: "Use the actual REPO_KEY/IMAGE_PATH; review the folder before adding --apply.", Shell: "oci-toolbox delete-tag artifactory.example.com/team/app:bad-release --artifactory-url https://artifactory.example.com/artifactory --artifactory-path docker-local/team/app"},
		},
	},
	{
		Command:     "delete-manifest",
		Description: "Remove a manifest digest and every tag pointing to it in this repository. Shared layers are not deleted.",
		Examples: []commandExample{
			{Title: "Preview manifest and affected tags", Shell: "oci-toolbox delete-manifest registry.example.com/team/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			{Title: "Delete the reviewed manifest", Description: "Deletes all its tag aliases; storage reclamation depends on registry garbage collection.", Shell: "oci-toolbox delete-manifest registry.example.com/team/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa --apply"},
		},
	},
	{
		Command:     "copy",
		Description: "Copy OCI images, mirror releases, or apply filesystem recipes.",
		Examples: []commandExample{
			{
				Title: "Direct registry copy",
				Shell: "oci-toolbox copy docker://registry.example.com/team/app:1.2 " +
					"docker://registry.example.com/team/app:1.2-copy",
			},
			{
				Title:       "Different proxy policy for source and destination",
				Description: "Pull through a proxy and connect directly to the internal destination registry.",
				Shell: `oci-toolbox copy \
  docker://registry.external.example/team/app:1.2 \
  docker://registry.internal.example/team/app:1.2 \
  --src-proxy http://proxy.example.com:8080 \
  --dest-proxy direct`,
			},
			{
				Title:       "Batch copy",
				Description: "Copy all declared images and record actual destination digests in a release manifest.",
				Files: []exampleFile{{Name: "images.yml", Format: "YAML", Content: `copy:
  - src: registry.example.com/team/config:release-1.0
    dest: registry.example.com/team/config:test-latest
  - src: registry.example.com/team/ticket:release-1.0
    dest: registry.example.com/team/ticket:test-latest`}},
				Shell: `oci-toolbox copy --batch-file images.yml \
  --release-id RE_2026.08.10.01 \
  --output releases/RE_2026.08.10.01-dev.yml \
  --src-insecure \
  --dest-insecure \
  --progress`,
			},
			{
				Title:       "Mirror a digest-locked release under a new release ID",
				Description: "Source digests remain immutable; the new release ID becomes the destination tag.",
				Shell: `oci-toolbox copy \
  --release-manifest releases/2026.06.25.01.yml \
  --src-base quay.example.com/nac-test \
  --dest-base artifactory.example.com/nac-test \
  --release-id RE_2026.09.10.02 \
  --output releases/RE_2026.09.10.02-replica.yml \
  --src-insecure \
  --dest-insecure \
  --retries 5 \
  --retry-initial-delay 2s \
  --retry-max-delay 30s \
  --progress`,
			},
			{
				Title:       "Build a Java application image",
				Description: "Add the application, configure its runtime identity, and keep recipe changes in one delta layer.",
				Files: []exampleFile{{Name: "app.yml", Format: "YAML", Content: `name: java-application
steps:
  - action: add-file
    src: ./target/uberjar/myapp.jar
    dst: /app/myapp.jar
    owner: "1001:0"
  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
    setgid-dirs: true
  - action: set-user
    value: "1001:1001"
  - action: set-workdir
    value: /app
  - action: set-entrypoint
    value_list: ["java", "-jar", "/app/myapp.jar"]
  - action: set-cmd
    value_list: ["--server.port=8080"]
  - action: set-env
    key: JAVA_TOOL_OPTIONS
    value: "-XX:MaxRAMPercentage=75"
  - action: set-label
    key: org.opencontainers.image.title
    value: myapp`}},
				Shell: `oci-toolbox copy --recipe app.yml --reduce-changes \
  docker://registry.example.com/team/app:1.2 \
  docker://registry.example.com/team/app:patched`,
			},
			{
				Title: "Replace tools and configuration",
				Files: []exampleFile{{Name: "patch-tools.yml", Format: "YAML", Content: `steps:
  - action: replace-file
    src: ./bin/encjson
    dst: /usr/local/bin/encjson
    find-all: encjson
    mode: "0755"
  - action: add-dir
    src: ./config/
    dst: /etc/myapp/
    owner: "1001:0"
  - action: remove-file
    dst: /etc/myapp/default-password
  - action: remove-dir
    dst: /usr/share/myapp/examples
  - action: add-file
    src: ./bin/healthcheck
    dst: /usr/local/bin/healthcheck
    mode: "0755"
    condition:
      if-not-exists-in: [/bin, /usr/bin, /usr/local/bin]`}},
				Shell: "oci-toolbox copy --recipe patch-tools.yml SOURCE DEST",
			},
			{
				Title:       "Rebase application files onto a clean runtime image",
				Description: "The CLI source remains available as original after switching the output base.",
				Files: []exampleFile{{Name: "rebase.yml", Format: "YAML", Content: `sources:
  - id: original
    ref: "{{.SourceImage}}"
  - id: runtime
    ref: docker://registry.example.com/library/jre-slim:21
steps:
  - action: use-base
    from: runtime
  - action: copy-file-from
    from: original
    src: /app/myapp.jar
    dst: /app/myapp.jar
    owner: "1001:0"
  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
  - action: set-entrypoint
    value_list: ["java", "-jar", "/app/myapp.jar"]`}},
				Shell: "oci-toolbox copy --recipe rebase.yml --reduce-changes SOURCE DEST",
			},
			{
				Title: "Unpack an archive and extract an artifact",
				Files: []exampleFile{{Name: "artifacts.yml", Format: "YAML", Content: `sources:
  - id: original
    ref: "{{.SourceImage}}"
steps:
  - action: unzip-to
    src: /app/myapp.jar
    dst: /app/classes/
    owner: "1001:0"
  - action: extract-file
    from: original
    src: /app/myapp.jar
    dst-local: ./artifacts/myapp.jar`}},
				Shell: "oci-toolbox copy --recipe artifacts.yml SOURCE DEST",
			},
			{
				Title:       "Protect an application directory",
				Description: "Replace /app with a self-extracting bootstrap while preserving Entrypoint and Cmd.",
				Files: []exampleFile{{Name: "shadow.yml", Format: "YAML", Content: `steps:
  - action: shadow
    path: /app
    extract-to: /app
    output: /app/app.self
    wrap-entrypoint: true
  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
    setgid-dirs: true`}},
				Shell: "oci-toolbox copy --recipe shadow.yml --reduce SOURCE DEST",
			},
		},
	},
	{
		Command:     "bundle validate",
		Description: "Validate the strict schema and deployment identity of an image bundle.",
		Examples: []commandExample{{
			Title: "Validate a bundle kept in Git",
			Files: []exampleFile{{Name: "bundle.yml", Format: "YAML", Content: `name: stable
registries:
  source:
    url: registry-source.example.com
  target:
    url: registry-release.example.com
    project: release
extra_tags: [stable]
images:
  - id: config
    source:
      repository: team/config
      tag: 2.3-latest
    target:
      repository: config
    deployment:
      app_name: config
      container_name: config`}},
			Shell: "oci-toolbox bundle validate bundle.yml",
		}},
	},
	{
		Command:     "bundle plan",
		Description: "Resolve and display the release operations without publishing images.",
		Examples:    []commandExample{{Title: "Plan a release", Shell: "oci-toolbox bundle plan bundle.yml --release-id RE_2026.07.28.01"}},
	},
	{
		Command:     "bundle inspect",
		Description: "Review manifest digests from the registry without downloading layers or writing images.",
		Examples: []commandExample{{
			Title:       "Publish exactly the reviewed digests",
			Description: "Use the same release and selective options for inspection and publishing; retain the JSON as a trusted local artifact.",
			Shell: `oci-toolbox bundle inspect bundle.yml --release-id release-1 > review.json
oci-toolbox bundle publish bundle.yml --release-id release-1 --digest-plan review.json --output releases/release-1.yml`,
		}},
	},
	{
		Command:     "bundle publish",
		Description: "Publish all or selected bundle images and write a digest-locked release manifest.",
		Examples: []commandExample{
			{
				Title: "Publish a complete release",
				Shell: `oci-toolbox bundle publish bundle.yml \
  --release-id RE_2026.07.28.01 \
  --bundle-revision abc123 \
  --output releases/RE_2026.07.28.01.yml \
  --progress`,
			},
			{
				Title:       "Patch selected Deployment/container targets",
				Description: "Selected images are updated; other immutable digests are carried forward from the base release.",
				Shell: `oci-toolbox bundle publish bundle.yml \
  --base-release releases/RE_2026.08.01.01.yml \
  --release-id RE_2026.08.03.01 \
  --select example-api/application \
  --select example-worker/application \
  --output releases/RE_2026.08.03.01.yml \
  --progress`,
			},
			{
				Title: "Load selectors from a file and preserve base-only targets",
				Files: []exampleFile{{Name: "selected-images.yml", Format: "YAML", Content: `selectors:
  - example-api/application
  - example-worker/application`}},
				Shell: `oci-toolbox bundle publish bundle.yml \
  --base-release releases/RE_2026.08.01.01.yml \
  --release-id RE_2026.08.03.01 \
  --select-file selected-images.yml \
  --preserve-base-only \
  --output releases/RE_2026.08.03.01.yml`,
			},
		},
	},
	{
		Command:     "release reconstruct",
		Description: "Create a digest-locked release manifest from Kubernetes or existing registry images.",
		Examples: []commandExample{
			{
				Title: "Reconstruct running Deployment containers",
				Shell: `oci-toolbox release reconstruct \
  --namespace example \
  --image-prefix registry.example.com/team \
  --exclude-scaled-to-zero \
  --release-id RE_2026.07.28.01 \
  --kube-insecure \
  --insecure \
  --output releases/RE_2026.07.28.01.yml \
  --discover-tags \
  --progress`,
			},
			{
				Title: "Reconstruct directly from tagged images",
				Shell: `oci-toolbox release reconstruct \
  registry.example.com/release/api:RE_2026.07.28.01 \
  registry.example.com/release/ui:RE_2026.07.28.01 \
  --output releases/RE_2026.07.28.01.yml \
  --discover-tags \
  --progress`,
			},
			{
				Title:       "Use explicit deployment identities",
				Description: "Use a strict input file when deployment and repository names differ.",
				Files: []exampleFile{{Name: "release-images.yml", Format: "YAML", Content: `images:
  - ref: registry.example.com/release/diagnostics:RE_2026.07.28.01
    id: ui-diagnostics
    app_name: ui
    container_name: diagnostics`}},
				Shell: `oci-toolbox release reconstruct \
  --file release-images.yml \
  --release-id RE_2026.07.28.01 \
  --output releases/RE_2026.07.28.01.yml`,
			},
		},
	},
	{
		Command:     "release convert",
		Description: "Explicit offline native ImageRelease v1/v2 to v2 conversion; ordinary release output stays v1.",
		Examples: []commandExample{{
			Title:       "Convert a native v1 file and compare content",
			Description: "Preserves the source and refuses an existing output; external products still use yaml-map.",
			Files: []exampleFile{{Name: "native-v1.yml", Format: "YAML", Content: `apiVersion: oci-toolbox/v1
kind: ImageRelease
release_id: release-1
images:
  - app_name: api
    image: registry.example.com/team/api
    platform: multi-platform
    digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`}},
			Shell: `oci-toolbox release convert native-v1.yml --output native-v2.yml
oci-toolbox release compare native-v1.yml native-v2.yml --mode full --fail-on-change`,
		}},
	},
	{
		Command:     "release to-bundle",
		Description: "Convert a release manifest back into a reusable image bundle.",
		Examples: []commandExample{
			{
				Title:       "Keep immutable source digests",
				Description: "Digest mode is the default and reproduces the exact recorded source content.",
				Shell: `oci-toolbox release to-bundle releases/RE_2026.08.10.01.yml \
  --name example-2.4 \
  --output bundles/example-2.4.yml`,
			},
			{
				Title: "Use mutable source tags",
				Shell: `oci-toolbox release to-bundle releases/RE_2026.08.10.01.yml \
  --name example-2.4 \
  --source-mode tag \
  --output bundles/example-2.4-tags.yml`,
			},
		},
	},
	{
		Command:     "release selectors",
		Description: "List Deployment/container selectors available for selective publishing.",
		Examples:    []commandExample{{Title: "Print selectors as plain text", Shell: "oci-toolbox release selectors releases/current.yml --format plain"}},
	},
	{
		Command:     "release compare",
		Description: "Compare two release manifests at the desired semantic level.",
		Examples: []commandExample{
			{
				Title: "Return changed digests as JSON",
				Shell: `oci-toolbox release compare releases/previous.yml releases/current.yml \
  --mode digest \
  --format json \
  --changed-only`,
			},
			{
				Title: "Produce a one-line CI result and fail on differences",
				Shell: `oci-toolbox release compare releases/previous.yml releases/current.yml \
  --mode digest \
  --format summary \
  --fail-on-change`,
			},
		},
	},
	{
		Command:     "release verify",
		Description: "Verify release digests, tags, and optional Kubernetes deployment state.",
		Examples: []commandExample{
			{Title: "Verify immutable digests and primary tags in the registry", Shell: "oci-toolbox release verify releases/current.yml --progress"},
			{
				Title: "Detect Kubernetes drift and emit JSON",
				Shell: `oci-toolbox release verify releases/current.yml \
  --namespace example \
  --image-prefix registry.example.com/team \
  --format json`,
			},
		},
	},
	{
		Command:     "registry info",
		Description: "Inspect repository metadata using a shared registry configuration.",
		Examples: []commandExample{{
			Title: "Inspect configured repositories",
			Files: []exampleFile{{Name: "oci-toolbox-registry.yml", Format: "YAML", Content: `registry:
  url: https://registry.example.com
cleanup:
  mode: created
  keep_count: 14
  protect_latest: true
  protect_tag_patterns:
    - "^stable$"
repositories:
  - name: team/api
  - name: team/ui`}},
			Shell: "oci-toolbox registry info --config oci-toolbox-registry.yml --creds user:password",
		}},
	},
	{
		Command:     "registry copy-plan",
		Description: "Generate a batch copy file from configured registry repositories.",
		Examples: []commandExample{{
			Title: "Create images.yml for a mirror registry",
			Shell: `oci-toolbox registry copy-plan --config oci-toolbox-registry.yml \
  --dest-registry mirror.example.com \
  --output images.yml`,
		}},
	},
	{
		Command:     "registry list-repositories",
		Description: "List the registry catalog, optionally restricted to one repository prefix.",
		Examples: []commandExample{
			{Title: "List every repository", Shell: "oci-toolbox registry list-repositories --config oci-toolbox-registry.yml"},
			{Title: "List repositories below a prefix", Shell: "oci-toolbox registry list-repositories --config oci-toolbox-registry.yml team"},
		},
	},
	{
		Command:     "registry cleanup",
		Description: "Plan or apply registry retention rules.",
		Examples: []commandExample{
			{Title: "Preview cleanup", Description: "Cleanup is a dry-run unless --apply is supplied.", Shell: "oci-toolbox registry cleanup --config oci-toolbox-registry.yml"},
			{Title: "Apply cleanup", Shell: "oci-toolbox registry cleanup --config oci-toolbox-registry.yml --apply"},
			{
				Title:       "Preview GitLab API tag cleanup",
				Description: "Registry reads use registry credentials; GitLab deletes use a separate API token from the environment. Add --apply only after reviewing the plan.",
				Files: []exampleFile{{Name: "gitlab-cleanup.yml", Format: "YAML", Content: `registry:
  url: https://gitlab.example.com:5050
  username: ${REGISTRY_USERNAME}
  password: ${REGISTRY_PASSWORD}
gitlab:
  url: https://gitlab.example.com
  project: "1582"
  token: ${GITLAB_API_TOKEN}
cleanup:
  delete_via: gitlab
  mode: timestamp
  keep_count: 14
  protect_latest: true
repositories:
  - name: team/project/api`}},
				Shell: "oci-toolbox registry cleanup --config gitlab-cleanup.yml",
			},
		},
	},
	{
		Command:     "snapshot create",
		Description: "Save Kubernetes Deployment images and a restoration manifest.",
		Examples: []commandExample{
			{Title: "Snapshot every Deployment image", Shell: "oci-toolbox snapshot create --namespace example --output-dir ./snapshots"},
			{Title: "Restrict images with a full-reference glob", Shell: "oci-toolbox snapshot create --namespace example '*/team/docs-*:*'"},
		},
	},
	{
		Command:     "snapshot restore",
		Description: "Verify snapshot checksums and restore images to their original references.",
		Examples: []commandExample{{
			Title: "Restore a namespace snapshot",
			Shell: `oci-toolbox snapshot restore --namespace example \
  ./snapshots/example/20260728-120000.yml`,
		}},
	},
	{
		Command:     "spring-config discover",
		Description: "Extract a static Spring Boot configuration inventory from an image.",
		Examples: []commandExample{{
			Title:       "Discover configuration in JIB classpath directories and JARs",
			Description: "The output keeps files separate and writes manifest.yml plus a searchable manifest.svg.",
			Shell: `oci-toolbox spring-config discover \
  docker://registry.example.com/team/application:2.4 \
  --output ./spring-config-analysis \
  --include 'config/*.yml' \
  --progress \
  --src-insecure`,
		}},
	},
	{
		Command:     "tag-existing",
		Description: "Add a tag to a manifest that already exists in the same registry repository.",
		Examples: []commandExample{{
			Title:       "Tag an existing digest without transferring layers",
			Description: "Source and destination must use the same repository.",
			Shell: `oci-toolbox tag-existing \
  docker://registry.example.com/team/app@sha256:0123456789abcdef... \
  docker://registry.example.com/team/app:release-1`,
		}},
	},
	{
		Command:     "inspect",
		Description: "Print image metadata and configuration.",
		Examples:    []commandExample{{Title: "Inspect an image", Shell: "oci-toolbox inspect docker://registry.example.com/team/app:latest"}},
	},
	{
		Command:     "tree",
		Description: "Browse an image filesystem as a directory tree.",
		Examples:    []commandExample{{Title: "Show the /app subtree", Shell: "oci-toolbox tree docker://registry.example.com/team/app:latest /app"}},
	},
	{
		Command:     "ls",
		Description: "List direct entries in one image directory.",
		Examples:    []commandExample{{Title: "List application resources", Shell: "oci-toolbox ls docker://registry.example.com/team/app:latest /app/resources"}},
	},
	{
		Command:     "cat",
		Description: "Write one image file to standard output.",
		Examples:    []commandExample{{Title: "Read an application artifact", Shell: "oci-toolbox cat docker://registry.example.com/team/app:latest /app/app.jar"}},
	},
}

func writeExamples(w io.Writer, color bool) error {
	writeExamplesIntro(w, color)
	for _, command := range examplesCatalog {
		writeCommandHeader(w, command.Command, command.Description, color)
		for _, example := range command.Examples {
			writeExample(w, example, color)
		}
	}

	fmt.Fprintln(w)
	if color {
		fmt.Fprintf(w, "%sRun 'oci-toolbox <command> --help' for all flags. Full reference: README.md%s\n", ansiDim, ansiReset)
	} else {
		fmt.Fprintln(w, "Run 'oci-toolbox <command> --help' for all flags. Full reference: README.md")
	}
	return nil
}

func writeExamplesIntro(w io.Writer, color bool) {
	if color {
		fmt.Fprintf(w, "%s%sOCI-TOOLBOX COMMAND EXAMPLES%s\n", ansiBold, ansiCyan, ansiReset)
		fmt.Fprintf(w, "%sCommands are green; external files are blue.%s\n", ansiDim, ansiReset)
		return
	}
	fmt.Fprintln(w, "OCI-TOOLBOX COMMAND EXAMPLES")
	fmt.Fprintln(w, "Commands are marked SHELL; external files are marked FILE.")
}

func writeCommandHeader(w io.Writer, command, description string, color bool) {
	fmt.Fprintln(w)
	title := strings.ToUpper(command)
	if color {
		fmt.Fprintf(w, "%s%s## %s%s\n", ansiBold, ansiCyan, title, ansiReset)
		fmt.Fprintf(w, "   %s%s%s\n", ansiDim, description, ansiReset)
		return
	}
	fmt.Fprintf(w, "## %s\n", title)
	fmt.Fprintf(w, "   %s\n", description)
}

func writeExample(w io.Writer, example commandExample, color bool) {
	fmt.Fprintln(w)
	if color {
		fmt.Fprintf(w, "  %s%s› %s%s\n", ansiBold, ansiYellow, example.Title, ansiReset)
		if example.Description != "" {
			fmt.Fprintf(w, "    %s%s%s\n", ansiDim, example.Description, ansiReset)
		}
	} else {
		fmt.Fprintf(w, "  › %s\n", example.Title)
		if example.Description != "" {
			fmt.Fprintf(w, "    %s\n", example.Description)
		}
	}
	for _, file := range example.Files {
		writeFileBlock(w, file, color)
	}
	if example.Shell != "" {
		writeShellBlock(w, example.Shell, color)
	}
}

func writeShellBlock(w io.Writer, shell string, color bool) {
	fmt.Fprintln(w)
	if color {
		fmt.Fprintf(w, "    %s%sSHELL%s\n", ansiBold, ansiGreen, ansiReset)
	} else {
		fmt.Fprintln(w, "    SHELL")
	}
	for _, line := range strings.Split(shell, "\n") {
		if color {
			fmt.Fprintf(w, "%s      %s%s\n", ansiGreen, line, ansiReset)
		} else {
			fmt.Fprintf(w, "      %s\n", line)
		}
	}
}

func writeFileBlock(w io.Writer, file exampleFile, color bool) {
	fmt.Fprintln(w)
	label := "FILE · " + file.Name
	if file.Format != "" {
		label += " (" + strings.ToUpper(file.Format) + ")"
	}
	if color {
		fmt.Fprintf(w, "    %s%s%s%s\n", ansiBold, ansiBlue, label, ansiReset)
	} else {
		fmt.Fprintf(w, "    %s\n", label)
	}
	for _, line := range strings.Split(file.Content, "\n") {
		if color {
			fmt.Fprintf(w, "%s        %s%s\n", ansiBlue, line, ansiReset)
		} else {
			fmt.Fprintf(w, "        %s\n", line)
		}
	}
}
