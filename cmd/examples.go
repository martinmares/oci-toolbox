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

func writeExamples(w io.Writer, color bool) error {
	section(w, "Quick copy", color)
	codeBlock(w, `# Copy an image unchanged.
oci-toolbox copy docker://registry.example.com/team/app:1.2 docker://registry.example.com/team/app:1.2-copy`, color)

	section(w, "Batch copy", color)
	codeBlock(w, `# Copy every image listed in images.yml. Both registries use an insecure TLS connection.
oci-toolbox copy --batch-file images.yml --src-insecure --dest-insecure`, color)
	fmt.Fprintln(w)
	codeBlock(w, `# images.yml
copy:
  - src: registry.example.com/team/config:release-1.0
    dest: registry.example.com/team/config:test-latest
  - src: registry.example.com/team/ticket:release-1.0
    dest: registry.example.com/team/ticket:test-latest`, color)

	section(w, "Copy a release manifest", color)
	codeBlock(w, `# Copy every digest-locked image in a release to another registry path.
oci-toolbox copy \
  --release-manifest releases/2026.06.25.01.yml \
  --src-prefix quay.example.com/nac-test \
  --dest-prefix artifactory.example.com/nac-test \
  --output releases/2026.06.25.01-replica.yml \
  --src-insecure \
  --dest-insecure \
  --progress`, color)

	section(w, "Publish an image bundle", color)
	codeBlock(w, `# bundle.yml: mutable desired state kept in Git.
name: stable
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
      container_name: config

oci-toolbox bundle validate bundle.yml
oci-toolbox bundle plan bundle.yml --release-id RE_2026.07.28.01
oci-toolbox bundle publish bundle.yml \
  --release-id RE_2026.07.28.01 \
  --bundle-revision abc123 \
  --output releases/RE_2026.07.28.01.yml \
  --progress`, color)

	section(w, "Reconstruct a release manifest", color)
	codeBlock(w, `# Reconstruct exact Deployment/container selectors from Kubernetes.
oci-toolbox release reconstruct \
  --namespace example \
  --image-prefix registry.example.com/team \
  --release-id RE_2026.07.28.01 \
  --kube-insecure \
  --insecure \
  --output releases/RE_2026.07.28.01.yml \
  --discover-tags \
  --progress

# Reconstruct directly from existing tagged images.
oci-toolbox release reconstruct \
  registry.example.com/release/api:RE_2026.07.28.01 \
  registry.example.com/release/ui:RE_2026.07.28.01 \
  --output releases/RE_2026.07.28.01.yml \
  --discover-tags \
  --progress

# Use a strict YAML input when deployment names differ from repository names.
# release-images.yml
images:
  - ref: registry.example.com/release/diagnostics:RE_2026.07.28.01
    id: ui-diagnostics
    app_name: ui
    container_name: diagnostics

oci-toolbox release reconstruct \
  --file release-images.yml \
	--release-id RE_2026.07.28.01 \
	--output releases/RE_2026.07.28.01.yml`, color)

	section(w, "Inspect and clean a registry", color)
	codeBlock(w, `# oci-toolbox-registry.yml
registry:
  url: https://registry.example.com
cleanup:
  mode: created
  keep_count: 14
  protect_latest: true
  protect_tag_patterns:
    - "^stable$"
repositories:
  - name: team/api
  - name: team/ui

# Inspect repository metadata and generate a batch copy file.
oci-toolbox registry info --config oci-toolbox-registry.yml --creds user:password
oci-toolbox registry copy-plan --config oci-toolbox-registry.yml \
  --dest-registry mirror.example.com --output images.yml

# Cleanup is a dry-run unless --apply is explicitly supplied.
oci-toolbox registry cleanup --config oci-toolbox-registry.yml
oci-toolbox registry cleanup --config oci-toolbox-registry.yml --apply`, color)

	section(w, "Snapshot Kubernetes deployment images", color)
	codeBlock(w, `# Save every image referenced by Deployments in a namespace.
oci-toolbox snapshot create --namespace example --output-dir ./snapshots

# Restrict the snapshot with a glob evaluated against the complete image reference.
oci-toolbox snapshot create --namespace example '*/team/docs-*:*'

# Verify archive checksums and restore every image to its original reference.
oci-toolbox snapshot restore --namespace example \
  ./snapshots/example/20260728-120000.yml`, color)

	section(w, "Java application", color)
	codeBlock(w, `# app.yml: add an application, make /app writable for the runtime user, and set startup metadata.
name: java-application
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
    value: myapp

# Keep base-image layers and make all recipe changes one delta layer.
# src can instead use ./target/uberjar/*SNAPSHOT.jar when it matches exactly one JAR; dst: /app/ then preserves its filename.
oci-toolbox copy --recipe app.yml --reduce-changes docker://registry.example.com/team/app:1.2 docker://registry.example.com/team/app:patched`, color)

	section(w, "Replace a tool and configuration", color)
	codeBlock(w, `# patch-tools.yml: replace every existing tool named encjson, copy a configuration tree, and remove obsolete content.
steps:
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
      if-not-exists-in: [/bin, /usr/bin, /usr/local/bin]

oci-toolbox copy --recipe patch-tools.yml SOURCE DEST`, color)

	section(w, "Rebase onto another image", color)
	codeBlock(w, `# rebase.yml: take application files from the CLI source, then write them onto a clean runtime image.
sources:
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
    value_list: ["java", "-jar", "/app/myapp.jar"]

oci-toolbox copy --recipe rebase.yml --reduce-changes SOURCE DEST`, color)

	section(w, "Unpack or extract an artifact", color)
	codeBlock(w, `# artifacts.yml: unzip a JAR already in the image and also write one source file to the local machine.
sources:
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
    dst-local: ./artifacts/myapp.jar

oci-toolbox copy --recipe artifacts.yml SOURCE DEST`, color)

	section(w, "Protect an application directory", color)
	codeBlock(w, `# shadow.yml: replace /app with a self-extracting encrypted bootstrap.
# The original Entrypoint and Cmd are preserved; app.self runs first and removes itself.
steps:
  - action: shadow
    path: /app
    extract-to: /app
    output: /app/app.self
    wrap-entrypoint: true
  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
    setgid-dirs: true

# --reduce creates one final filesystem layer; use it when the smallest individual image matters.
oci-toolbox copy --recipe shadow.yml --reduce SOURCE DEST`, color)

	section(w, "Inspect image contents", color)
	codeBlock(w, `oci-toolbox inspect docker://registry.example.com/team/app:latest
oci-toolbox tree docker://registry.example.com/team/app:latest /app
oci-toolbox cat docker://registry.example.com/team/app:latest /app/app.jar`, color)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'oci-toolbox <command> --help' for all flags. Full reference: README.md")
	return nil
}

func section(w io.Writer, title string, color bool) {
	if color {
		fmt.Fprintf(w, "%s%s%s\n", ansiBold+ansiCyan, title, ansiReset)
		return
	}
	fmt.Fprintln(w, title)
}

func codeBlock(w io.Writer, block string, color bool) {
	commandContinuation := false
	for _, line := range strings.Split(block, "\n") {
		if color && commandContinuation && line != "" {
			fmt.Fprintln(w, ansiGreen+line+ansiReset)
		} else {
			fmt.Fprintln(w, highlightExampleLine(line, color))
		}
		trimmed := strings.TrimSpace(line)
		commandContinuation = (commandContinuation || strings.HasPrefix(trimmed, "oci-toolbox ")) &&
			strings.HasSuffix(trimmed, `\`)
	}
}

func highlightExampleLine(line string, color bool) string {
	if !color || line == "" {
		return line
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		return ansiBlue + line + ansiReset
	}
	if strings.HasPrefix(trimmed, "oci-toolbox ") {
		return ansiGreen + line + ansiReset
	}
	if key, value, ok := strings.Cut(trimmed, ": "); ok {
		indent := line[:len(line)-len(trimmed)]
		return indent + ansiYellow + key + ":" + ansiReset + " " + value
	}
	if strings.HasSuffix(trimmed, ":") {
		indent := line[:len(line)-len(trimmed)]
		return indent + ansiYellow + trimmed + ansiReset
	}
	return line
}
