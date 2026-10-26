# oci-toolbox

**OCI image transfer and in-flight patching tool — no Docker daemon required.**

`oci-toolbox` copies container images between registries (like `skopeo`) and additionally applies declarative *recipes* that modify the image during transfer — injecting files, rewriting metadata, rebasing onto a different base image, and more. Everything runs as a single static binary with zero runtime dependencies.

---

## Table of Contents

- [Why oci-toolbox](#why-oci-toolbox)
- [Comparison with skopeo](#comparison-with-skopeo)
- [Installation](#installation)
- [Commands](#commands)
  - [copy](#copy)
  - [inspect](#inspect)
  - [cat](#cat)
  - [ls](#ls)
  - [tree](#tree)
  - [examples](#examples-command)
  - [bundle](#bundle)
  - [External release formats](#external-release-formats)
  - [release to-bundle](#release-to-bundle)
  - [release reconstruct](#release-reconstruct)
  - [release selectors and compare](#release-selectors-and-compare)
  - [release verify](#release-verify)
  - [registry maintenance](#registry-maintenance)
  - [Targeted tag and manifest deletion](#targeted-tag-and-manifest-deletion)
  - [Kubernetes image snapshots](#kubernetes-image-snapshots)
- [Git release repository](#git-release-repository)
- [Credentials](#credentials)
- [Recipes](#recipes)
  - [Recipe structure](#recipe-structure)
  - [Actions reference](#actions-reference)
  - [Conditions](#conditions)
  - [Multi-source recipes](#multi-source-recipes)
- [Examples](#examples)
- [Running tests](#running-tests)

---

## Why oci-toolbox

The typical workflow for patching a container image looks like this:

```
docker pull image
docker run --entrypoint ... image   # extract something
docker build -f Dockerfile.patch .  # build patched image
docker push patched-image
```

This requires a running Docker daemon, a Dockerfile, and intermediate containers. `oci-toolbox` collapses the entire workflow into a single command with a YAML recipe — no daemon, no Dockerfile, no intermediate containers, no `docker` binary.

---

## Comparison with skopeo

`oci-toolbox` is **intentionally CLI-compatible** with `skopeo` for the commands it implements (`copy`, `inspect`, `--version`). You can replace the `skopeo` binary with `oci-toolbox` in existing scripts without changing arguments.

| Feature | skopeo | oci-toolbox |
|---------|--------|-----------|
| Copy image between registries | ✅ | ✅ |
| Inspect image metadata (JSON) | ✅ | ✅ |
| Print a file from an image | ❌ | ✅ |
| List repository tags | ✅ | ✅ (`inspect` fetches tags automatically) |
| No Docker daemon required | ✅ | ✅ |
| `~/.docker/config.json` auth | ✅ | ✅ |
| `--src-creds` / `--dest-creds` | ✅ | ✅ |
| `--override-os` / `--override-arch` | ✅ | ✅ |
| `--dest-no-reuse` fallback | ✅ | ✅ |
| `--debug` output | ✅ | ✅ |
| **Inject files into image** | ❌ | ✅ |
| **Rewrite ENTRYPOINT / CMD** | ❌ | ✅ |
| **Set ENV / LABEL / USER / WORKDIR** | ❌ | ✅ |
| **Remove files (whiteout layer)** | ❌ | ✅ |
| **Copy files between images** | ❌ | ✅ |
| **Rebase onto different base image** | ❌ | ✅ |
| **Extract files from image to local** | ❌ | ✅ |
| **Unzip archives inside image** | ❌ | ✅ |
| **List directory entries in image** | ❌ | ✅ |
| **Print tree view of image path** | ❌ | ✅ |
| **Declarative YAML recipes** | ❌ | ✅ |
| **Multiple recipes per copy** | ❌ | ✅ |
| **Conditional steps (if-not-exists)** | ❌ | ✅ |
| **Multi-source image composition** | ❌ | ✅ |
| Run commands inside container (`RUN`) | ✅ (via policy) | ❌ intentionally omitted |
| Signature verification (sigstore) | ✅ | ❌ not implemented |
| OCI image layout (directory) | ✅ | ❌ not implemented |

### Key differences explained

**1. In-flight modification**

`skopeo copy` is a pure transfer tool — it moves bits from A to B. `oci-toolbox copy` modifies the image as it flows through: it pulls layers, applies recipe steps (each step appends a new OCI layer or rewrites image config), then pushes the result. The original image is never mutated.

**2. No `RUN` support — intentionally**

Running arbitrary commands inside a container during build (like `apk upgrade`, `adduser`) requires a container runtime (runc, crun, or buildkit). `oci-toolbox` deliberately excludes this to remain a zero-dependency static binary. Everything it does is pure layer and config manipulation in memory.

**3. Multi-source composition**

A recipe can declare multiple named source images. Steps can extract files from any of them and compose them into a new image — similar to Docker multi-stage builds, but declarative and without a daemon.

**4. Conditional steps**

Steps can be guarded with `condition: if-not-exists: /path` — the step is skipped if the file already exists in the current image state. This is useful for idempotent patching: inject a tool only if it is not already present.

**5. skopeo CLI compatibility**

All flags used in the most common `skopeo` workflows are supported with identical names:

```bash
# skopeo — works unchanged as oci-toolbox
skopeo inspect --creds user:pass docker://registry.example.com/app:latest
skopeo copy --src-creds u:p --dest-creds u:p docker://src/img:tag docker://dst/img:tag
skopeo copy --debug --dest-no-reuse docker://src docker://dst
skopeo --version
```

---

## Installation

```bash
git clone https://github.com/martinmares/oci-toolbox
cd oci-toolbox
go build -o oci-toolbox .
```

Or cross-compile for Linux amd64:

```bash
GOOS=linux GOARCH=amd64 go build -o oci-toolbox-linux-amd64 .
```

With [just](https://just.systems/), local release-style binaries are written to `dist/`:

```bash
just build-linux
just build-macos
just build-windows
```

---

## Commands

### copy

```
oci-toolbox copy [flags] SOURCE DESTINATION
```

Copies an image from SOURCE to DESTINATION, applying any specified recipes in order.

For a plain registry-to-registry copy without recipes, layer reduction, entrypoint wrapping, or `--dest-no-reuse`, manifest digests are compared before layers are downloaded. If the destination tag already resolves to the source digest, no layers are transferred. If the destination tag is missing or differs but the same digest already exists under another tag in the destination repository, only the requested tag is created through the registry API. Layer pull/push starts only when the destination repository does not contain the source digest.

**Flags:**

| Flag | Description |
|------|-------------|
| `--src-creds user:pass` | Source registry credentials |
| `--dest-creds user:pass` | Destination registry credentials |
| `--src-proxy URL\|direct` | Proxy used only for source registry operations |
| `--dest-proxy URL\|direct` | Proxy used only for destination registry operations |
| `--recipe path/to/recipe.yml` | Recipe file to apply (repeatable) |
| `--debug` | Enable verbose output |
| `--batch-file path/to/images.yml` | Run multiple copy jobs from a YAML file |
| `--release-manifest path/to/release.yml` | Copy all digest-locked images from a release manifest |
| `--src-base registry/path` | Source registry/repository base replaced in release-manifest mode; defaults to `registry_base` |
| `--dest-base registry/path` | Required destination registry/repository base in release-manifest mode |
| `--output path/to/release.yml` | Write a release manifest for copied destination images |
| `--release-id ID` | Release ID for `--batch-file` output, or an override for destination tags and output in `--release-manifest` mode |
| `--retries count` | Additional attempts for transient network failures; default `4`, use `0` to disable |
| `--retry-initial-delay duration` | Delay before the first retry; default `2s` |
| `--retry-max-delay duration` | Maximum exponential retry delay; default `30s` |
| `--continue-on-error` | Continue batch processing after a copy fails |
| `--wrap-entrypoint` | Wrap the CLI `SOURCE` image's entrypoint and command for every `shadow` step; useful with `use-base` |
| `--no-wrap-entrypoint` | Disable entrypoint wrapping for every `shadow` step in the supplied recipes |
| `--dest-tag tag` | Add an extra destination tag in the same repository without copying layers again (repeatable) |
| `--dest-no-reuse` | Disable cross-repository blob mount/reuse at destination (retry fallback) |
| `--reduce` | Materialize the final filesystem into a single minimal layer |
| `--reduce-changes` | Preserve base layers and reduce recipe changes into one delta layer |
| `--progress` | Render terminal progress bars |
| `--progress-json` | Emit machine-readable progress events as JSONL |

Proxy routing is directional. For example, pull an external image through a corporate Squid proxy while pushing directly to an internal registry:

```bash
oci-toolbox copy \
  docker://registry.external.example/team/app:1.2 \
  docker://registry.internal.example/team/app:1.2 \
  --src-proxy http://proxy.example.com:8080 \
  --dest-proxy direct
```

When a proxy flag is omitted, the standard `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` environment behavior remains active. The special value `direct` disables environment proxies for that direction. Proxy routing covers manifest and digest checks, pulls, pushes, blob reuse, and remote tag operations. An HTTP proxy uses CONNECT for an HTTPS registry, so registry credentials and image data remain inside TLS unless the proxy performs TLS interception. Credentials embedded in a plain HTTP proxy URL are rejected; use an HTTPS proxy for authenticated proxy URLs.

**Examples:**

```bash
# Simple copy (no recipe)
oci-toolbox copy docker://registry.example.com/app:1.0 docker://registry.example.com/app:1.0-copy

# Copy with credentials
oci-toolbox copy \
  --src-creds srcuser:srcpass \
  --dest-creds dstuser:dstpass \
  docker://src.registry.com/app:2.3-latest \
  docker://dst.registry.com/app:2026.04.17.01

# Copy once and publish additional destination tags for the same manifest
oci-toolbox copy \
  --dest-creds dstuser:dstpass \
  --dest-tag latest \
  --dest-tag stable \
  docker://src.registry.com/app:2.3-latest \
  docker://dst.registry.com/app:2026.04.17.01

# Copy with a recipe (inject a tool)
oci-toolbox copy \
  --src-creds user:pass \
  --dest-creds user:pass \
  --recipe inject-tool.yml \
  docker://registry.example.com/app:latest \
  docker://registry.example.com/app:patched

# Copy with multiple recipes applied in sequence
oci-toolbox copy \
  --recipe recipe-1.yml \
  --recipe recipe-2.yml \
  docker://src/app:tag \
  docker://dst/app:tag

# Digest-based source
oci-toolbox copy \
  --src-creds user:pass \
  docker://registry.example.com/app@sha256:abc123... \
  docker://registry.example.com/app:stable

# Retry fallback with --dest-no-reuse
oci-toolbox copy --debug --dest-no-reuse \
  --src-creds user:pass --dest-creds user:pass \
  docker://src/app:tag docker://dst/app:tag

# Machine-readable progress for integrations
oci-toolbox copy --progress-json \
  docker://src/app:tag docker://dst/app:tag
```

### Batch Copy

Use `--batch-file` when you need to copy multiple images with the same CLI options. The shared flags are applied to every entry in the file.

```yaml
# images.yml
copy:
  - src: registry.example.com/team/config:release-1.0
    dest: registry.example.com/team/config:test-latest
    id: config
    app_name: config-service
    container_name: application
    dest_tags:
      - latest
      - stable
  - src: registry.example.com/team/ticket:release-1.0
    dest: registry.example.com/team/ticket:test-latest
```

```bash
oci-toolbox copy \
  --batch-file images.yml \
  --release-id RE_2026.08.10.01 \
  --output releases/RE_2026.08.10.01-dev.yml \
  --src-insecure \
  --dest-insecure \
  --retries 5 \
  --retry-initial-delay 2s \
  --retry-max-delay 30s \
  --progress
```

Batch jobs run sequentially. Without `--continue-on-error`, the first failed copy stops the batch. With `--continue-on-error`, later entries still run, but the command returns a non-zero exit code at the end if any entry failed and prints a failure summary.

The primary batch keys are `src` and `dest`, matching the `--src-*` and `--dest-*` CLI flags. `source` and `destination` are also accepted as aliases. `dest_tags` adds extra tags in the same destination repository after the image is copied once, equivalent to repeating `--dest-tag` for a single copy command. Optional `id`, `app_name`, and `container_name` values define deployment identity in the generated release manifest; when omitted, they are inferred from the destination repository name. Duplicate destination images, IDs, and selectors are rejected during validation.

Combining `--batch-file` with `--output` inspects every destination manifest after all copies succeed and records its actual digest, tag, and platform. The source reference and digest are stored in `source`. `--release-id` is optional only when all destinations use the same tag, in which case that tag becomes the release ID. Output is written atomically and an existing file is never overwritten.

### Release Manifest Copy

Use `--release-manifest` to copy an immutable release to another registry path without first generating a batch file:

```bash
oci-toolbox copy \
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
  --progress
```

Every source uses the manifest's `image@digest`. The source base is replaced with `--dest-base`, preserving the remaining repository path. By default every destination receives the input manifest's `release_id` as its tag; `--release-id` overrides that destination tag without changing which source digest is copied. When `--src-base` is omitted, `registry_base` from the manifest is used; if neither is available, the command fails before copying. Images outside the source base and duplicate destinations are rejected. Manifest `tag` and `extra_tags` values are intentionally not copied. The former `--src-prefix` and `--dest-prefix` flags were renamed; using either old spelling fails with a message pointing to `--src-base` or `--dest-base`.

`--release-manifest`, `--batch-file`, and positional `SOURCE DESTINATION` are separate input modes. Credentials, TLS settings, progress, debug output, and `--continue-on-error` apply to every generated copy job.

Every copied destination digest is verified against the release manifest. Multi-platform image indexes are copied between registries without selecting a child platform. Recipes, layer reduction, entrypoint wrapping, `--dest-no-reuse`, and `--dest-tag` are not supported in release-manifest mode because they could change or partially materialize locked content.

When `--output` is supplied, the file is written atomically only after every copy and digest verification succeeds. The new manifest keeps the original deployment selectors, platforms, and bundle metadata; it uses the overridden release ID when `--release-id` was supplied, points `registry_base` and each `image` to the destination, and records the previous release image in each item's `source`. Existing output files are never overwritten. Without `--output`, the command still applies `--release-id` to destination tags but does not write a manifest.

Transient network failures such as timeouts, connection resets, broken pipes, temporary registry responses, and unexpected EOFs retry the current image with exponential backoff. The default four retries wait `2s`, `4s`, `8s`, and `16s`. Completed batch items are not repeated, and authentication, validation, digest, and other permanent errors fail immediately. `--continue-on-error` is applied only after an item exhausts its retries.

### inspect

```
oci-toolbox inspect [flags] IMAGE
```

Fetches and displays image metadata as JSON (skopeo-compatible format).

**Flags:**

| Flag | Description |
|------|-------------|
| `--creds user:pass` | Registry credentials |
| `--override-os linux` | Select specific OS from multi-platform index |
| `--override-arch arm64` | Select specific architecture |
| `--override-variant v8` | Select specific variant |
| `--no-tags` | Skip fetching repository tags (faster) |
| `--insecure` / `--src-insecure` | Skip TLS certificate verification for the source registry |

**Examples:**

```bash
# Basic inspect
oci-toolbox inspect docker://docker.io/library/alpine:3.21

# With credentials
oci-toolbox inspect --creds user:pass docker://registry.example.com/app:latest

# Registry with a non-standard/self-signed certificate
oci-toolbox inspect --src-insecure docker://registry.example.com/app:latest

# Select specific platform from multi-arch index
oci-toolbox inspect \
  --override-os linux \
  --override-arch arm64 \
  docker://docker.io/library/alpine:3.21

# Fast inspect without tag list
oci-toolbox inspect --no-tags docker://registry.example.com/app:latest
```

**Output format** (skopeo-compatible):

```json
{
    "Name": "index.docker.io/library/alpine",
    "Digest": "sha256:f27cad9...",
    "RepoTags": ["3.19", "3.20", "3.21", "latest", "..."],
    "Created": "2026-04-16T23:53:15.704110414Z",
    "DockerVersion": "",
    "Labels": null,
    "Architecture": "amd64",
    "Os": "linux",
    "Layers": ["sha256:897d797d..."],
    "LayersData": [
        {
            "MIMEType": "application/vnd.oci.image.layer.v1.tar+gzip",
            "Digest": "sha256:897d797d...",
            "Size": 3646875,
            "Annotations": null
        }
    ],
    "Env": ["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"]
}
```

### cat

```
oci-toolbox cat [flags] IMAGE PATH
```

Fetches an image and prints one regular file from its filesystem to stdout.

If the file does not exist, `cat` exits successfully and prints nothing. With `--debug`, the command also logs a warning about the missing file.

**Flags:**

| Flag | Description |
|------|-------------|
| `--creds user:pass` | Registry credentials |
| `--override-os linux` | Select specific OS from multi-platform index |
| `--override-arch arm64` | Select specific architecture |
| `--override-variant v8` | Select specific variant |
| `--insecure` / `--src-insecure` | Skip TLS certificate verification for the source registry |
| `--debug` | Enable verbose output, including missing-file warnings |

**Examples:**

```bash
oci-toolbox cat \
  --src-insecure \
  docker://registry.example.com/team/gateway:2.2-test-latest \
  /app/resources/git.properties

# Missing files are silent unless --debug is used
oci-toolbox cat --debug docker://registry.example.com/app:latest /missing/file
```

### ls

```
oci-toolbox ls [flags] IMAGE PATH
```

Lists entries under a path inside an image. Missing paths are silent by default and only log a warning with `--debug`.

**Flags:**

| Flag | Description |
|------|-------------|
| `--creds user:pass` | Registry credentials |
| `--override-os linux` | Select specific OS from multi-platform index |
| `--override-arch arm64` | Select specific architecture |
| `--override-variant v8` | Select specific variant |
| `--insecure` / `--src-insecure` | Skip TLS certificate verification for the source registry |
| `--debug` | Enable verbose output, including missing-path warnings |

**Example:**

```bash
oci-toolbox ls --src-insecure docker://registry.example.com/app:latest /app/resources
```

### tree

```
oci-toolbox tree [flags] IMAGE PATH
```

Prints a tree view of the subtree rooted at the given path. Missing paths are silent by default and only log a warning with `--debug`.

**Flags:**

| Flag | Description |
|------|-------------|
| `--creds user:pass` | Registry credentials |
| `--override-os linux` | Select specific OS from multi-platform index |
| `--override-arch arm64` | Select specific architecture |
| `--override-variant v8` | Select specific variant |
| `--insecure` / `--src-insecure` | Skip TLS certificate verification for the source registry |
| `--debug` | Enable verbose output, including missing-path warnings |

**Example:**

```bash
oci-toolbox tree --src-insecure docker://registry.example.com/app:latest /app
```

### spring-config

```
oci-toolbox spring-config discover [flags] IMAGE
```

Analyzes a JIB-built Spring Boot image without running it. The command reads the JIB classpath and main-class files, preserves classpath order, scans classpath directories and JAR/ZIP archives, and exports Spring configuration candidates together with a deterministic `manifest.yml`.

By default it extracts:

- `application.yml`, `application.yaml`, `application.properties`, and profile-named variants from any classpath location;
- `bootstrap.yml`, `bootstrap.yaml`, `bootstrap.properties`, and profile-named variants;
- root-level `.yml`, `.yaml`, and `.properties` files from every classpath directory or archive, including custom defaults such as `database-copy-defaults.yml`;
- additional paths selected by repeated `--include` globs.

```bash
oci-toolbox spring-config discover \
  docker://registry.example.com/team/application:2.4 \
  --output ./spring-config-analysis \
  --include 'config/*.yml' \
  --progress \
  --src-insecure
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--output`, `-o` | Required new output directory; existing directories are never overwritten |
| `--classpath-file /app/jib-classpath-file` | JIB classpath file inside the image |
| `--main-class-file /app/jib-main-class-file` | JIB main-class file inside the image |
| `--include glob` | Additional archive path or basename glob to extract; may be repeated |
| `--progress` | Print progress while scanning image layers and classpath archives |
| `--creds user:pass` | Registry credentials |
| `--override-os linux` | Select an OS from a multi-platform index |
| `--override-arch amd64` | Select an architecture from a multi-platform index |
| `--override-variant v8` | Select a platform variant |
| `--insecure` / `--src-insecure` | Skip source registry TLS certificate verification |
| `--debug` | Enable verbose output |

The output groups files by classpath index, so equal filenames from different JARs do not collide:

```text
spring-config-analysis/
├── manifest.yml
├── manifest.svg
├── classpath.txt
└── sources/
    ├── 0000-resources/application.yml
    └── 0047-application-commons.jar/application-commons.yml
```

The manifest records the image digest and platform, main class, ordered classpath, original image or `jar!/entry` source, output path, SHA-256, and size. A name such as `application-commons.yml` receives `profile_hint: commons`, but `activation` remains `unknown`: the file may be loaded by a profile, `spring.config.import`, `@PropertySource`, or custom code.

`manifest.svg` is a searchable visual map from the image through classpath entries to configuration IDs, sources, and output files. It shows only classpath entries with discovered configurations and reports how many empty entries were omitted. A JAR source is marked as `JAR`, uses a `source` path containing `!/`, and has `archive_entry` in `manifest.yml`; direct image files are grouped under a classpath `directory` such as `/app/resources`.

This command performs static discovery only. It does not merge files or reconstruct the final Spring Environment. Runtime analysis still needs deployment environment variables and JVM arguments, ConfigMaps and Secrets, Spring Cloud Config sources, active profiles, and preferably Actuator `/actuator/env` and `/actuator/configprops` snapshots.

The output may contain credentials. Output directories use mode `0700` and extracted files use mode `0600`.

### examples command

```
oci-toolbox examples
```

Shows a structured command cookbook directly in the terminal. Every command has a prominent heading and description followed by named variants. Shell commands and required external files such as bundle, batch, registry configuration, and recipe YAML are rendered as separate blocks.

```bash
oci-toolbox examples

# Equivalent Cobra help form:
oci-toolbox help examples
```

When stdout is a terminal, command headings, variant names, shell blocks, and external file blocks use distinct colors. Piping or redirecting the output preserves the same hierarchy as plain text, so this is safe for `oci-toolbox examples | less` and `oci-toolbox examples > examples.txt`.

### Digest review before publishing

`bundle plan` remains an offline reference preview. For a registry-backed review:

```sh
oci-toolbox bundle inspect bundles/stable.yml --release-id release-1 > /tmp/review.json
oci-toolbox bundle publish bundles/stable.yml --release-id release-1 \
  --digest-plan /tmp/review.json --output releases/release-1.yml
```

`bundle inspect` emits JSON containing source references, destination references,
application/container selectors and source digests. It reads registry manifests
without downloading image layers or modifying either registry. It supports the
same source/destination credentials, TLS/proxy flags and selective base-release
options as publishing. For selective publishing, pass the same `--base-release`,
`--select` and `--preserve-base-only` options to both commands.

`--digest-plan` verifies the release identity and every planned source/destination
reference before registry writes, then publishes from the reviewed digest references.
Moving a source tag after inspection cannot change published content. Keep the
review JSON as a trusted local artifact; changed bundle references require a fresh
inspection. Existing publishing without this flag remains supported.

### bundle

A bundle is a mutable, Git-managed declaration of which image tags or digests should be published as one release. It contains no credentials and uses strict YAML: unknown fields, duplicate IDs, duplicate deployment selectors, and duplicate target repositories are rejected.

```yaml
name: stable
description: Stable application bundle

registries:
  source:
    url: registry-source.example.com
  target:
    url: registry-release.example.com
    project: release

extra_tags:
  - stable

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
```

Each `source` must contain exactly one of `tag` or `digest`. Registry URLs and project paths can be replaced at runtime with `--src-registry`, `--dest-registry`, `--src-project`, and `--dest-project`.

```bash
# Local validation without registry access
oci-toolbox bundle validate bundle.yml

# Show resolved source and destination references
oci-toolbox bundle plan bundle.yml \
  --release-id RE_2026.07.28.01

# Copy images and create the immutable digest-based release record
oci-toolbox bundle publish bundle.yml \
  --release-id RE_2026.07.28.01 \
  --bundle-revision "$(git rev-parse HEAD)" \
  --extra-tag candidate \
  --output releases/RE_2026.07.28.01.yml \
  --progress
```

`bundle publish` accepts the same independent `--src-proxy` and `--dest-proxy` routing as `copy`. This includes preflight inspection, selective-release digest reuse, copying, and retagging.

`bundle publish` preflights all images before copying. A missing primary release tag is created, the same existing digest is treated as an idempotent retry, and an existing tag with another digest aborts the operation. Extra tags such as `stable` are movable aliases. The output file must not already exist. Git commit, tag, and push operations deliberately remain the responsibility of CI or the caller.

#### Selective release from a base manifest

To publish only named application containers while carrying every other digest forward from an existing release, pass the previous manifest and repeat `--select` with the deployment identity `app_name/container_name`:

```bash
oci-toolbox bundle publish bundles/stable.yml \
  --base-release releases/RE_2026.08.01.01.yml \
  --release-id RE_2026.08.03.01 \
  --select zis-aq-bridge/application \
  --select zis-es-idx-view/application \
  --output releases/RE_2026.08.03.01.yml \
  --progress
```

For a longer list, use a strict YAML selection file:

```yaml
selectors:
  - zis-aq-bridge/application
  - zis-es-idx-view/application
```

```bash
oci-toolbox bundle publish bundles/stable.yml \
  --base-release releases/RE_2026.08.01.01.yml \
  --release-id RE_2026.08.03.01 \
  --select-file selected-images.yml \
  --preserve-base-only \
  --output releases/RE_2026.08.03.01.yml
```

The selector is deliberately based on `(app_name, container_name)`, not on a mutable tag or repository name. Selected targets use the current source from the bundle. Unselected targets are read from the base release by immutable digest and retagged with the new `release_id`; their digest therefore cannot change. By default, the base release and bundle must contain the same deployment selectors and target repositories. With `--preserve-base-only`, the base release becomes the authoritative template for the new release: its target list and ordering are preserved, selected blocks are replaced from the bundle, and all other blocks retain their digest. Unselected targets found only in the bundle are ignored. A selected target must exist in both the base release and the bundle, preventing an accidental deployment addition or removal. Missing, duplicate, or malformed selectors and target repository changes for selected entries fail before registry writes start. `--select` and `--select-file` can be combined.

When neither `--dest-registry` nor `registries.target.url` is set, selective publish first uses `registry_base` from the base release. If it is absent, the destination prefix is inferred by matching common deployment selectors and stripping each bundle `target.repository` from its base image. Inference succeeds only when all common targets produce the same prefix; an explicit destination always has precedence.

Source registry credentials are used for selected bundle images. Destination registry credentials are also used as source credentials when reusing unselected images from the previous release.

When a reused base image stays in the same repository, publish creates the new release tag directly through the registry API and transfers no layers. A full pull/push is performed only for selected images or when source and target repositories differ.

With `--progress`, a separate `preflight` progress bar is shown before image transfer. It reports the number of inspected images, the selector currently being checked, and whether the source digest or destination tag is being inspected.

New images selected from the bundle currently support only single-platform `linux/amd64` manifests. Unselected and base-only images reused by immutable digest may also be multi-platform OCI indexes; they are retagged without selecting or modifying a child platform.

### External release formats

All release commands accept the native `oci-toolbox/v1` `ImageRelease` schema
(`release_id`, `created_at`, `images`) and legacy native files without a header.
External product formats are not interpreted by oci-toolbox. The former
`release convert` command and automatic product imports have been removed.

Use the separate `yaml-map` utility with a Jinja-like template to prepare a
native release. The template belongs to the integration, so adding a new input
format does not require a change to oci-toolbox:

```bash
yaml-map --input product-manifest.yaml \
  --template /path/to/yaml-map/examples/product-release-to-images.yaml.j2 \
  --arg release_id=RE_2026.10.02.01 \
  --output releases/RE_2026.10.02.01.yml

oci-toolbox release selectors releases/RE_2026.10.02.01.yml --format plain
oci-toolbox release to-bundle releases/RE_2026.10.02.01.yml \
  --name product --output bundles/product.yml
```

`yaml-map` performs the conversion offline; oci-toolbox then validates and
processes the native output normally. Review deployment selectors and retain
the original input for metadata not included in the image release.
Adjust deployment identities directly in the template if they differ from the
inventory names; yaml-map does not enforce a release schema.

### release to-bundle

Convert an existing release manifest back into a bundle when its recorded source images should become the input for another publish workflow:

```bash
oci-toolbox release to-bundle releases/2026.08.10.01-dev.yml \
  --name tsm-sda-2.4 \
  --output bundles/tsm-sda-2.4.yml
```

The command infers the common source and target registry/project prefixes and keeps each image's source repository, target repository, ID, deployment selector, and extra tags. This also preserves repository renames such as a source `tsm-config` published as target `tsm-config-form`. Every release image must contain `source` metadata, all source images must use one registry, and all target images must use one registry. Ambiguous or unrepresentable input fails instead of generating a misleading bundle. The bundle name cannot be recovered reliably from every release manifest and is therefore required through `--name`. Use `--output -` (the default) to write YAML to stdout.

`--source-mode` controls what the generated bundle uses as its source reference:

- `--source-mode digest` is the default. It records each source digest, so a later `bundle publish` consumes exactly the bytes described by the release manifest. This is the safe choice for reproduction, rollback, and audit workflows.
- `--source-mode tag` records each original source tag instead. The generated bundle is easier to reuse as desired state for later releases, but resolving it later may produce different content if a registry tag moves. Conversion fails if any release image lacks a source tag.

```bash
oci-toolbox release to-bundle releases/2026.08.10.01-dev.yml \
  --name tsm-sda-2.4 \
  --source-mode tag \
  --output bundles/tsm-sda-2.4-tags.yml
```

Release-only metadata such as `release_id`, `created_at`, platform, the primary target tag, and target digest is intentionally not stored in the bundle. A new primary target tag is supplied later with `bundle publish --release-id`.

### release reconstruct

`release reconstruct` builds the same digest-based release manifest from images that already exist in a registry. References can be passed directly:

```bash
oci-toolbox release reconstruct \
  registry.example.com/release/config:RE_2026.07.28.01 \
  registry.example.com/release/gateway:RE_2026.07.28.01 \
  --output releases/RE_2026.07.28.01.yml \
  --discover-tags \
  --progress
```

Every reference must contain an explicit tag or digest; an implicit `latest` is rejected. When every reference uses the same tag, that tag becomes `release_id`. Otherwise, pass `--release-id`. The repository basename is used as `id`, `app_name`, and `container_name`.

For an initial manifest reconstructed from an existing Kubernetes environment, read the Deployment selectors directly from the API:

```bash
oci-toolbox release reconstruct \
  --namespace example \
  --image-prefix registry.example.com/team \
  --exclude-scaled-to-zero \
  --release-id RE_2026.07.28.01 \
  --kube-insecure \
  --insecure \
  --output releases/RE_2026.07.28.01.yml \
  --discover-tags \
  --progress
```

This mode records `app_name` from the Deployment name and `container_name` from each regular container in its pod template. It preserves repeated use of the same image under different selectors. Init containers are not included. Use `--image-prefix` to exclude platform and operator images outside the selected registry or repository path before registry inspection starts. `--exclude-scaled-to-zero` omits Deployments with an explicit `spec.replicas: 0`; Deployments requesting at least one replica remain included even when they currently have no ready pod. The result describes the current namespace topology; positional images, `--file`, and `--namespace` are mutually exclusive. Use `--kubeconfig` and `--context` when the default kubeconfig selection is not sufficient. Kubernetes TLS and registry TLS are controlled separately by `--kube-insecure` and `--insecure`.

Use a strict YAML input when deployment selectors differ from repository names:

```yaml
images:
  - ref: registry.example.com/release/config:RE_2026.07.28.01
    id: config
    app_name: config-service
    container_name: application
  - ref: registry.example.com/release/gateway:RE_2026.07.28.01
    app_name: gateway
    container_name: gateway
```

```bash
oci-toolbox release reconstruct \
  --file release-images.yml \
  --release-id RE_2026.07.28.01 \
  --extra-tag stable \
  --output releases/RE_2026.07.28.01.yml
```

Every image is resolved to its registry digest. Tag discovery is opt-in: `--discover-tags` lists the repository tags, resolves them, and records every tag that points to the same digest. An explicit input tag remains the primary `tag`; for digest inputs, a discovered tag equal to `release_id` is preferred, followed by a single unambiguous matching tag. Other matches are stored in the image's `extra_tags`. Each `--extra-tag` is recorded only after the command verifies that the tag already points to that same digest. Use `--progress` to show the number of inspected images, throughput, and ETA. Single images record their actual `os/architecture`; OCI indexes are locked by the index digest and recorded as `platform: multi-platform`. The manifest-level platform is omitted when the inputs mix platform types.

### release selectors and compare

List the stable `app_name/container_name` identities available for `bundle publish --select`:

```bash
# Human-readable table with image details
oci-toolbox release selectors releases/RE_2026.08.03.01.yml

# One selector per line for shell pipelines or a selection file generator
oci-toolbox release selectors releases/RE_2026.08.03.01.yml --format plain

# Structured output with release_id and image details
oci-toolbox release selectors releases/RE_2026.08.03.01.yml --format json
```

Compare two manifests by that identity. The comparison is directional from A to B and reports `same`, `changed`, `added`, and `removed`. Three comparison presets are available:

- `--mode digest` is the default and compares only immutable SHA digests. Use it to compare a release generated by another system with one reconstructed from a cluster.
- `--mode image` compares repository, digest, and platform. Tags remain informational, so a release-ID-only retag does not change the content status.
- `--mode full` compares all per-image release data: ID, source, repository, tag, digest, extra tags, and platform. Top-level release ID and creation time are not image identity fields.

Every mode matches rows by `app_name/container_name`; missing and additional selectors are always differences.

```bash
# Compact digest table (default mode)
oci-toolbox release compare releases/previous.yml releases/current.yml

# One-line result
oci-toolbox release compare releases/previous.yml releases/current.yml \
  --mode digest \
  --format summary

# Stable machine-readable image comparison
oci-toolbox release compare releases/previous.yml releases/current.yml \
  --mode image \
  --format json \
  --changed-only

# Strict CI gate
oci-toolbox release compare releases/previous.yml releases/current.yml \
  --mode full \
  --format summary \
  --fail-on-change
```

`--format summary` prints `MATCH` or `DIFFERENT` with counts. JSON additionally contains `mode`, `matched_by`, and the boolean `match`. `--changed-only` filters only detailed table and JSON rows; summaries always contain counts for the complete comparison.

Table statuses use git-diff-style colors when stdout is a terminal: `added` is green, `removed` red, `changed` yellow, and `same` dimmed. Control this with `--color auto|always|never`; JSON never contains ANSI escape sequences, and `NO_COLOR` disables automatic coloring.

### release verify

Without Kubernetes options, `release verify` checks the registry. Every immutable `image@digest` must exist and each primary release tag recorded by the manifest must still resolve to that digest:

```bash
oci-toolbox release verify releases/current.yml --progress

oci-toolbox release verify releases/current.yml \
  --creds "$REGISTRY_USER:$REGISTRY_PASS" \
  --insecure \
  --format json
```

Mutable aliases from `extra_tags`, such as `stable`, are deliberately not verified because they may legitimately move after a later release.

Pass `--namespace` to verify the manifest against regular containers in Kubernetes Deployments. Live image references are resolved through the registry and compared by `app_name/container_name`, repository, digest, and the manifest platform when specified. Missing and unexpected selectors are reported as drift. A tag-only difference is recorded as `tag_changed`, but identical immutable content remains verified.

```bash
oci-toolbox release verify releases/current.yml \
  --namespace example \
  --image-prefix registry.example.com/team \
  --kube-insecure \
  --insecure \
  --format json
```

Both modes print a complete report and exit non-zero when drift or an inspection error is found, so no additional CI gating flag is required. Kubernetes and registry TLS are controlled independently by `--kube-insecure` and `--insecure`.

The default table is deliberately compact: verified entries show only their status and selector, while `DETAILS` is used for drift, errors, or tag differences. Complete expected/actual values remain available through `--format json`.

### registry maintenance

Registry maintenance uses a strict YAML configuration. Keep credentials outside Git and pass them with `--creds` or environment-expanded values when compatibility with an existing configuration is required.

```yaml
# oci-toolbox-registry.yml
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
```

```bash
# Inspect tags, digests, creation timestamps, and deduplicated sizes.
oci-toolbox registry info \
  --config oci-toolbox-registry.yml \
  --creds user:password

# List repositories exposed by the registry catalog API.
oci-toolbox registry list-repositories \
  --config oci-toolbox-registry.yml

# Generate a file accepted by: oci-toolbox copy --batch-file images.yml
oci-toolbox registry copy-plan \
  --config oci-toolbox-registry.yml \
  --dest-registry mirror.example.com \
  --output images.yml

# Safe preview. No manifests are deleted.
oci-toolbox registry cleanup --config oci-toolbox-registry.yml

# Apply the displayed cleanup plan.
oci-toolbox registry cleanup --config oci-toolbox-registry.yml --apply
```

Cleanup works with unique manifest digests rather than counting aliases as separate images. `mode: timestamp` orders tags matching `YYYY.MM.DD.BUILD`; `mode: created` uses image configuration timestamps. Protected tags preserve their complete manifest digest. Registry operations also support `--insecure`, `--ca-cert`, `--timeout`, repository filtering, and missing-repository handling.

#### GitLab tag deletion

For GitLab installations that allow tag deletion through the GitLab API but do
not grant direct Registry API manifest deletion, select the GitLab backend:

```yaml
registry:
  url: https://gitlab.example.com:5050
  username: ${REGISTRY_USERNAME}
  password: ${REGISTRY_PASSWORD}

gitlab:
  url: https://gitlab.example.com
  project: "1582" # Numeric project ID or full path such as team/project.
  token: ${GITLAB_API_TOKEN}

cleanup:
  delete_via: gitlab # Default: registry (delete manifests by digest).
  mode: timestamp
  keep_count: 10
  protect_latest: true
  protect_tag_patterns:
    - '^2026\.08\.10\.01$'

repositories:
  - name: team/project/api
```

Use a personal access token with the `api` scope in `gitlab.token`. Its name is
only a label; the API authenticates with the generated secret in `PRIVATE-TOKEN`,
without a username. Registry reads still use `registry.username/password` or
`--creds`. GitLab API requests share the registry timeout and TLS configuration.

The existing `registry cleanup` command remains a dry-run unless `--apply` is
specified. Dry-run also validates GitLab access and repository mapping. Retention
still groups by digest: protecting one tag preserves the entire digest group.
GitLab mode deletes each selected tag individually and checks its current digest
through Registry API before deletion. It does not use GitLab tag-detail GET, which
can return 404 even when the registry lists the tag and GitLab DELETE succeeds.
Both HTTP 200 and 204 are accepted. After processing a repository,
the registry tag list is read again: only absent tags count as completed removals;
remaining tags or a failed verification return an error. Already absent tags are
accepted for retries; a tag that moved to another digest is not deleted. Requests
are not redirected with the API token.

Repository failures return a nonzero exit code, retain partial progress in the
summary, and report completed tag/digest-group removals separately from the plan.
If any tag cannot be inspected, that repository is skipped rather than building
an incomplete retention plan. Storage figures are estimates, not measured freed
space; deleting tags does not itself remove the manifest or reclaim its blobs.
Registry garbage collection is responsible for reclaiming unreferenced content.

### Targeted tag and manifest deletion

`delete-tag` removes one explicit tag. `delete-manifest` removes an explicit
manifest digest from one repository, affecting every tag pointing to that digest.
Available since **1.0.0**; `oci-toolbox examples` includes preview and apply commands.
Both default to **dry-run**: only `--apply` sends a DELETE request. Inspection
reads manifest metadata, not image layers. Docker credentials/helpers are used
unless `--creds user:password` is supplied. `--insecure` skips TLS verification;
`--timeout` bounds the complete operation (default 60 seconds).

```sh
# Inspect one tag without deleting it.
oci-toolbox delete-tag registry.example.com/team/app:bad-release

# Remove that tag only. Never substitute a digest reference here.
oci-toolbox delete-tag registry.example.com/team/app:bad-release --apply

# Optional guard: refuse if the tag points to a different manifest.
oci-toolbox delete-tag registry.example.com/team/app:bad-release \
  --expect-digest sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  --apply

# Preview deletion of a manifest; also lists its current tag aliases.
oci-toolbox delete-manifest \
  registry.example.com/team/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
# Repeat the same command with --apply to execute.
```

Tag deletion uses `DELETE /v2/<repository>/manifests/<tag>`. Some registries
disable or do not implement this optional OCI operation. Failure does **not**
fall back to deleting the digest, because that would affect other tags.

For Artifactory deployments requiring storage-folder deletion, explicitly supply
the Artifactory base URL and the storage path `REPO_KEY/IMAGE_PATH` (without the
tag). Obtain the real repository key from Artifactory; Docker routing alone may
not reveal it. The URL must use the same host as the Docker image reference, and
the storage path must match that image. The tool never guesses this mapping.

```sh
oci-toolbox delete-tag artifactory.example.com/team/app:bad-release \
  --artifactory-url https://artifactory.example.com/artifactory \
  --artifactory-path docker-local/team/app
# Review the displayed storage target, then repeat with --apply.
```

The Artifactory backend deletes exactly the selected tag folder using its REST
API. Credentials must have Artifactory Delete permission; supply `--creds` if
the Docker credential helper's credentials do not permit storage API access.
The command rechecks the digest before deleting and verifies the requested
reference is absent afterward. This is not an atomic compare-and-delete: avoid
concurrently retargeting tags while deleting them. A verification failure is
reported rather than claiming success; deletion may already have occurred.

A release's `sha256` identifies an **image manifest/index**, not a filesystem
layer. Manifest deletion does not delete shared layers or recursively delete
child manifests of a multi-platform index. Storage reclamation is handled by
registry retention/garbage collection. Neither command sends blob deletions.

References: [OCI content management](https://github.com/opencontainers/distribution-spec/blob/main/spec.md#content-management),
[Artifactory Docker tag deletion](https://docs.jfrog.com/artifactory/docs/additional-docker-information).

### Kubernetes image snapshots

`snapshot create` reads container and init-container image references from every Deployment in a Kubernetes namespace and saves compressed OCI image-layout archives plus an incremental YAML manifest. OCI archives preserve both single-platform images and complete multi-platform image indexes. Transfers run directly through the built-in copy engine; no Docker daemon or second `oci-toolbox` process is required.

```bash
# Use current-context from kubeconfig.
oci-toolbox snapshot create \
  --namespace example \
  --output-dir ./snapshots \
  --src-creds user:password \
  --src-proxy http://proxy.example.com:8080

# Optional glob; * also crosses / and is matched against the full image reference.
oci-toolbox snapshot create \
  --namespace example \
  '*/team/docs-*:*'

# Restore every archive to the original image reference.
oci-toolbox snapshot restore \
  --namespace example \
  --dest-creds user:password \
  --dest-proxy direct \
  ./snapshots/example/20260728-120000.yml
```

The manifest remains `complete: false` until every archive is written. Restore rejects incomplete manifests, namespace mismatches, path traversal, and archives whose size or SHA-256 checksum changed. Version 2 manifests use OCI archives; restore remains compatible with version 1 Docker-archive snapshots. A digest-addressed source is restored through a deterministic `snapshot-<digest>` tag so the original digest reference becomes available again. Use `--kubeconfig`, `--context`, and `--kube-insecure` for Kubernetes access; registry TLS has separate `--src-insecure` and `--dest-insecure` flags.

---

## Git release repository

Keep bundle declarations and generated release manifests in a dedicated Git repository named `tsm-releases`:

```text
tsm-releases/
├── README.md
├── bundles/
│   ├── stable.yml
│   ├── candidate.yml
│   └── development.yml
└── releases/
    ├── RE_2026.06.25.01.yml
    └── RE_2026.07.29.01.yml
```

The mental model is intentionally simple:

- `bundles/` describes **what should be released**. These files are mutable declarations managed through normal Git changes and reviews.
- `releases/` records **what was actually released**. These files contain resolved image digests and should be treated as immutable.
- Git history records **who changed or generated what, and when**.

`oci-toolbox` reads and generates these YAML files but deliberately does not commit, tag, or push the repository. Those operations remain the responsibility of CI/CD or the release operator.

---

## Credentials

Credentials are resolved in the following priority order:

1. `--src-creds` / `--dest-creds` / `--creds` CLI flags (`user:password`)
2. `~/.docker/config.json` (automatically, including credential helpers like `docker-credential-ecr-login`)
3. Anonymous access (public registries)

Per-registry credentials can also be declared inside a recipe file for auxiliary source images (see [Multi-source recipes](#multi-source-recipes)).

---

## Recipes

A recipe is a YAML file that declares a sequence of steps to apply to the image during copy. Multiple recipes can be passed to a single `copy` command — they are applied in order.

### Recipe structure

```yaml
name: "my-recipe"
description: "Human-readable description"

# Optional: auth for registries used as sources inside this recipe
registries:
  - url: registry.example.com
    username: user
    password: pass
  - url: docker.io
    # anonymous

# Optional: named source images (for copy-file-from, extract-file, use-base, shadow)
sources:
  - id: original
    ref: "{{.SourceImage}}"   # the --from image passed on the CLI
  - id: alpine-base
    ref: docker://docker.io/library/alpine:3.21

steps:
  - action: add-file
    src: ./utils/mytool
    dst: /usr/local/bin/mytool
    mode: "0755"
```

### Actions reference

#### File operations

**`add-file`** — Add a local file into the image as a new layer.

```yaml
- action: add-file
  src: ./utils/encjson          # local path
  dst: /usr/local/bin/encjson   # path inside image
  mode: "0755"                  # optional, octal string, default 0644
  owner: "1001:1001"            # optional uid:gid, default 0:0
  condition:
    if-not-exists: /usr/local/bin/encjson   # optional, see Conditions
```

`src` may contain a glob pattern such as `./target/uberjar/*SNAPSHOT.jar`, but it must match exactly one file. When `dst` ends with a slash, it is treated as a directory and the target filename is taken from the matched `src`.

**`add-dir`** — Add all files from a local directory as a new layer.

```yaml
- action: add-dir
  src: ./configs/
  dst: /etc/myapp/
  mode: "0644"
  owner: "1001:1001"
```

**`remove-file`** — Remove a file from the image (appends a whiteout layer).

```yaml
- action: remove-file
  dst: /etc/default/passwords
```

**`remove-dir`** — Remove a directory subtree from the image (appends a whiteout layer for the directory root).

```yaml
- action: remove-dir
  dst: /usr/share/java/maven-3
```

**`replace-file`** — Remove every occurrence of `find-all` (by basename) from the image, then add `src` at `dst`. If nothing is found, just adds the file. See the [conditions table](#conditions-and-overwrite-behaviour) for when to use this vs `add-file`.

```yaml
- action: replace-file
  src: ./utils/encjson
  dst: /usr/local/bin/encjson
  mode: "0755"
  find-all: encjson          # basename to search and whiteout (optional)
  owner: "0:0"
```

**`copy-file-from`** — Copy a file from another named source image into the current image.

```yaml
- action: copy-file-from
  from: original          # source id defined in sources:
  src: /app/dist/app.jar
  dst: /app/dist/app.jar
  mode: "0644"
  owner: "1001:1001"
```

**`extract-file`** — Extract a file from a named source image to a local path on disk.

```yaml
- action: extract-file
  from: original
  src: /app/dist/app.jar
  dst-local: /tmp/extracted/app.jar
```

**`unzip-to`** — Unzip a zip/jar file that lives inside the image into a directory inside the image.

```yaml
- action: unzip-to
  src: /app/dist/app.jar      # zip file path inside image
  dst: /app/dist/extracted/   # target directory inside image
  owner: "1001:1001"
```

#### Base image

**`use-base`** — Replace the current working image with a different base image. Subsequent steps build on top of it.

```yaml
- action: use-base
  from: alpine-base     # source id defined in sources:
```

`use-base` is the declarative equivalent of switching to a new `FROM alpine-base` in the middle of a multi-stage Dockerfile. Before that step, the recipe works on the image passed on the CLI. After `use-base`, the current working image becomes the selected source and later steps write into that image. If you still need files from the original source image, declare it in `sources` with `ref: "{{.SourceImage}}"` and use `from: original` on steps such as `copy-file-from` or `shadow`.

**`shadow`** — Pack a file tree into an encrypted self-extracting binary and replace the original path with that bootstrap binary. It leaves image startup metadata unchanged by default. Set `wrap-entrypoint: true` to wrap the original `ENTRYPOINT`: before starting the original process, `/bin/sh` runs `app.self -cleanup`, which restores the files and removes the bootstrap. The original `ENTRYPOINT` and `CMD` are preserved. By default it reads from the current working image; with `from:` it can read from another named source while writing into the current image. Entry-point wrapping requires `/bin/sh` in the target image.

Do not add `set-entrypoint` after a `shadow` step with `wrap-entrypoint: true`, because it would replace the wrapper. Put a custom `set-entrypoint` before `shadow` instead. The CLI flags `--wrap-entrypoint` and `--no-wrap-entrypoint` override every recipe step and cannot be combined. `--wrap-entrypoint` additionally takes the `Entrypoint` and `Cmd` from the CLI `SOURCE` image before wrapping; this preserves an application's startup command when the recipe switches to a runtime image with `use-base`.

```yaml
- action: shadow
  path: /app
  extract-to: /app
  output: /app/app.self
```

```yaml
- action: shadow
  from: original
  path: /app
  extract-to: /app
  output: /app/app.self
```

```yaml
# Run app.self before the original Entrypoint and Cmd.
- action: shadow
  path: /app
  extract-to: /app
  output: /app/app.self
  wrap-entrypoint: true
```

#### Metadata

**`set-env`** — Set or override an environment variable.

```yaml
- action: set-env
  key: MY_VAR
  value: "hello"
```

**`set-label`** — Set an OCI label.

```yaml
- action: set-label
  key: org.opencontainers.image.version
  value: "2.3.1"
```

**`set-entrypoint`** — Set the image ENTRYPOINT.

```yaml
- action: set-entrypoint
  value_list: ["/usr/local/bin/myapp", "--config", "/etc/myapp.conf"]
```

**`set-cmd`** — Set the image CMD.

```yaml
- action: set-cmd
  value_list: ["--port", "8080"]
```

**`set-user`** — Set the image USER (metadata only — does not create the user).

```yaml
- action: set-user
  value: "1001:1001"
```

**`set-workdir`** — Set the image WORKDIR.

```yaml
- action: set-workdir
  value: /app
```

**`fix-permissions`** — Rebuild a path with updated ownership and/or mode bits. This is useful for OpenShift-style writable application directories such as `/app`.

```yaml
- action: fix-permissions
  path: /app
  owner: "1001:0"
  mode: g=u
  setgid-dirs: true
```

`mode` accepts either an octal string like `"0775"` or the special value `g=u`, which mirrors user permission bits to group bits. When `setgid-dirs: true` is set, directories in the rebuilt tree get the setgid bit.

### Conditions and overwrite behaviour

The table below summarises all available strategies for controlling whether and how a file ends up in the image.

| Goal | How to write it | What happens |
|------|----------------|--------------|
| Always overwrite at `dst` | `add-file` with no `condition` | New layer added on top — old file at `dst` is shadowed. Files at other paths are untouched. |
| Add only if exact path is absent | `condition: if-not-exists: /usr/bin/tool` | Checks the exact path across all layers. Skips if found. |
| Add only if basename is absent in `$PATH` dirs | `condition: if-not-exists-in: [/bin, /usr/bin, /usr/local/bin]` | Searches the listed directories for the basename. Skips if found in **any** of them. Use this to avoid duplicates when you don't know the exact path. |
| Remove every occurrence of a name and place at `dst` | `replace-file` + `find-all: toolname` | Scans all layers, whiteouts every path whose basename matches, then adds the file at `dst`. If nothing is found, just adds. Result: exactly one copy at exactly one path. |

**`add-file` with `if-not-exists`** (exact path):

```yaml
- action: add-file
  src: ./utils/encjson
  dst: /usr/local/bin/encjson
  mode: "0755"
  condition:
    if-not-exists: /usr/local/bin/encjson
```

**`add-file` with `if-not-exists-in`** (basename search in directories):

```yaml
- action: add-file
  src: ./utils/encjson
  dst: /usr/local/bin/encjson
  mode: "0755"
  condition:
    if-not-exists-in: [/bin, /usr/bin, /usr/local/bin]
```

**`replace-file`** — remove all occurrences, add at `dst`:

```yaml
- action: replace-file
  src: ./utils/encjson
  dst: /usr/local/bin/encjson
  mode: "0755"
  find-all: encjson        # basename to find and whiteout across entire image
```

`replace-file` without `find-all` behaves identically to `add-file` without a condition (plain overwrite at `dst`).

### Multi-source recipes

A recipe can declare multiple named source images. The special reference `{{.SourceImage}}` maps to the `--from` image passed on the CLI.

```yaml
sources:
  - id: original
    ref: "{{.SourceImage}}"
  - id: alpine-base
    ref: docker://docker.io/library/alpine:3.21
  - id: tools
    ref: docker://registry.example.com/internal/tools:latest
```

Steps can then reference any of these by `from: <id>`. This is equivalent to Docker multi-stage builds — but declarative, daemonless, and composable across registries.

This also applies to `shadow`: you can take `/app` from the original source image, switch to a different base image with `use-base`, and then inject only `/app/app.self` into the new base.

### `--reduce` and `--reduce-changes`

`--reduce` does more than a classic layer squash. It materializes the effective final filesystem after all recipe steps into a single layer, so paths removed or replaced by whiteouts are not carried forward into the output image.

This is especially useful with `shadow`, where the final image can contain only:

- the new base image contents
- `/app/app.self`
- the minimal bootstrap directory metadata for `/app`

The result is typically close to the original image size plus the `app.self` overhead, not the original `/app` plus `app.self`.

`--reduce-changes` is for a different case: you want to keep the original base image layers for cache/reuse, but you do not want many small recipe layers. The output then contains:

- all base image layers
- one new delta layer with the effective recipe changes

If the recipe does not use `use-base`, the CLI source image is used as the base. If the recipe uses `use-base`, the image from the last `use-base` step is used as the base and the delta is computed against it.

In practice:

```bash
# One output layer containing the whole final filesystem.
oci-toolbox copy --recipe app.yml --reduce SOURCE DEST

# Original/base layers stay intact, recipe changes become one extra layer.
oci-toolbox copy --recipe app.yml --reduce-changes SOURCE DEST
```

---

## Examples

### Inject a binary into an existing image

```yaml
# inject-encjson.yml
name: "inject-encjson"
description: "Injects the encjson utility — only if not already present"

steps:
  - action: add-file
    src: ./utils/encjson
    dst: /usr/local/bin/encjson
    mode: "0755"
    condition:
      if-not-exists: /usr/local/bin/encjson

  - action: set-label
    key: "org.opencontainers.image.patched-by"
    value: "oci-toolbox"
```

```bash
oci-toolbox copy \
  --src-creds user:pass --dest-creds user:pass \
  --recipe inject-encjson.yml \
  docker://registry.example.com/team/gateway:2.3-latest \
  docker://registry.example.com/team/gateway:2026-04-17.01
```

### Rebase a Java app onto a clean Alpine image

```yaml
# rebase-app.yml
name: "rebase-app-on-alpine"
description: "Extract app.jar from source, rebuild on clean Alpine base"

sources:
  - id: original
    ref: "{{.SourceImage}}"
  - id: alpine-base
    ref: docker://docker.io/library/alpine:3.21

steps:
  - action: use-base
    from: alpine-base

  - action: copy-file-from
    from: original
    src: /app/dist/app.jar
    dst: /app/dist/app.jar
    mode: "0644"
    owner: "1001:1001"

  - action: unzip-to
    src: /app/dist/app.jar
    dst: /app/dist/extracted/
    owner: "1001:1001"

  - action: set-user
    value: "1001:1001"

  - action: set-workdir
    value: /app

  - action: set-entrypoint
    value_list: ["java", "-jar", "/app/dist/app.jar"]

  - action: set-label
    key: "org.opencontainers.image.base.name"
    value: "alpine:3.21"
```

```bash
oci-toolbox copy \
  --src-creds user:pass --dest-creds user:pass \
  --recipe rebase-app.yml \
  docker://registry.example.com/myapp:2.3-latest \
  docker://registry.example.com/myapp:2026-04-17.alpine
```

### Shadow `/app` in place

```yaml
# shadow.yml
name: "shadow /app folder"
description: "Replace /app with encrypted self-extracting bootstrap and make it OpenShift-safe"

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
```

```bash
oci-toolbox copy \
  --recipe shadow.yml \
  --reduce \
  docker://registry.example.com/myapp:latest \
  docker://registry.example.com/myapp:shadowed
```

### Shadow `/app` onto a new base image

```yaml
# shadow-from-base.yml
name: "shadow /app onto a new base"

sources:
  - id: original
    ref: "{{.SourceImage}}"
  - id: runtime-base
    ref: docker://registry.example.com/runtime-base:latest

steps:
  - action: use-base
    from: runtime-base

  - action: shadow
    from: original
    path: /app
    extract-to: /app
    output: /app/app.self

  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
    setgid-dirs: true
```

This pattern is useful when you want to preserve the application payload from the source image, but rebuild the runtime image on a smaller or cleaner base.

```bash
oci-toolbox copy \
  --recipe shadow-from-base.yml \
  --wrap-entrypoint \
  --reduce-changes \
  docker://registry.example.com/myapp:latest \
  docker://registry.example.com/myapp:shadowed
```

The CLI flag is important in this case: it wraps the `Entrypoint` and `Cmd` from the CLI source image, not the startup metadata of `runtime-base`.

### OpenShift note

For OpenShift, do not rely on a fixed runtime UID alone. The safer pattern is to make `/app` writable via group `0` and mirror user bits to group bits:

- `owner: "1001:0"`
- `mode: g=u`
- `setgid-dirs: true`

That keeps the bootstrap directory writable for arbitrary non-root UIDs running with group `0`, which is a common OpenShift deployment model.

### Replace skopeo in an existing project

If you use `skopeo` today for `inspect` and `copy`, replace the binary path:

```rust
// before
let service = SkopeoService::new("skopeo".to_string());

// after
let service = SkopeoService::new("/usr/local/bin/oci-toolbox".to_string());
```

All flags (`--src-creds`, `--dest-creds`, `--debug`, `--dest-no-reuse`, `--creds`, `--override-arch`, `--override-os`) are identical.

---

## Running tests

```bash
go test ./...
```

Tests are fully offline — no registry access required. They use in-memory OCI images built from tar layers.

```
ok  github.com/martinmares/oci-toolbox/internal/image    (17 tests)
ok  github.com/martinmares/oci-toolbox/internal/recipe   (11 tests)
```

## Automated binary releases

GitLab CI and GitHub Actions build releases when `VERSION` changes on the
default branch (GitHub: `main`). Both pipelines also support a manual run on
that branch. Set `VERSION` to a semantic version without the `v` prefix;
the release tag is `v<VERSION>`. An existing tag must point to the same commit,
otherwise increment `VERSION` rather than replacing the release.

Each release contains six `.tar.gz` archives: Linux, macOS and Windows,
each for `amd64` and `arm64`, plus `SHA256SUMS`. Archives include the executable,
both READMEs and the MIT license. Embedded extractors are built first, and
tests must pass before publication.

- GitLab uploads binaries directly to the Generic Package Registry and links
  them from the Release. Only small metadata files travel between jobs as
  artifacts, avoiding the previous oversized-artifact upload. Authentication
  uses `CI_JOB_TOKEN`; the project must permit package and release publication.
- GitHub publishes archives as Release assets using the workflow's
  `GITHUB_TOKEN` (`contents: write`), without a separate personal token.
  Uploads go into a draft before publication; versions containing a prerelease
  suffix are marked as prereleases.

The repository must be pushed or mirrored to each hosting service; adding a
workflow does not configure repository mirroring. Release notes are generated
from Git history. `RELEASE_PUSH_TOKEN` remains optional for committing the
generated changelog back to GitLab.

To build packages locally without publishing:

```sh
bash scripts/ci_release_notes.sh
bash scripts/ci_package_release.sh
```

## License

MIT, Copyright (c) 2026 Martin Mareš. See [LICENSE](LICENSE).
The license permits use, modification, redistribution and commercial use.

## Commercial Support

This software is provided under the MIT License and comes without
warranty or free community support.

Commercial support is available from **[DataLite](https://datalite.cz)**, including:

- technical assistance,
- verified releases,
- bug fixes,
- updates,
- deployment assistance,
- troubleshooting,
- long-term maintenance.

Contact [DataLite](https://datalite.cz) for commercial support options.

Commercial support is a separate service, not a requirement for using the
software under the MIT License. Public issues, feature requests and pull
requests do not create an obligation to respond, provide fixes or accept
contributions; upstream changes remain at the maintainer's discretion.
