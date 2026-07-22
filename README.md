# oci-patch

**OCI image transfer and in-flight patching tool — no Docker daemon required.**

`oci-patch` copies container images between registries (like `skopeo`) and additionally applies declarative *recipes* that modify the image during transfer — injecting files, rewriting metadata, rebasing onto a different base image, and more. Everything runs as a single static binary with zero runtime dependencies.

---

## Table of Contents

- [Why oci-patch](#why-oci-patch)
- [Comparison with skopeo](#comparison-with-skopeo)
- [Installation](#installation)
- [Commands](#commands)
  - [copy](#copy)
  - [inspect](#inspect)
  - [cat](#cat)
  - [ls](#ls)
  - [tree](#tree)
  - [examples](#examples-command)
- [Credentials](#credentials)
- [Recipes](#recipes)
  - [Recipe structure](#recipe-structure)
  - [Actions reference](#actions-reference)
  - [Conditions](#conditions)
  - [Multi-source recipes](#multi-source-recipes)
- [Examples](#examples)
- [Running tests](#running-tests)

---

## Why oci-patch

The typical workflow for patching a container image looks like this:

```
docker pull image
docker run --entrypoint ... image   # extract something
docker build -f Dockerfile.patch .  # build patched image
docker push patched-image
```

This requires a running Docker daemon, a Dockerfile, and intermediate containers. `oci-patch` collapses the entire workflow into a single command with a YAML recipe — no daemon, no Dockerfile, no intermediate containers, no `docker` binary.

---

## Comparison with skopeo

`oci-patch` is **intentionally CLI-compatible** with `skopeo` for the commands it implements (`copy`, `inspect`, `--version`). You can replace the `skopeo` binary with `oci-patch` in existing scripts without changing arguments.

| Feature | skopeo | oci-patch |
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

`skopeo copy` is a pure transfer tool — it moves bits from A to B. `oci-patch copy` modifies the image as it flows through: it pulls layers, applies recipe steps (each step appends a new OCI layer or rewrites image config), then pushes the result. The original image is never mutated.

**2. No `RUN` support — intentionally**

Running arbitrary commands inside a container during build (like `apk upgrade`, `adduser`) requires a container runtime (runc, crun, or buildkit). `oci-patch` deliberately excludes this to remain a zero-dependency static binary. Everything it does is pure layer and config manipulation in memory.

**3. Multi-source composition**

A recipe can declare multiple named source images. Steps can extract files from any of them and compose them into a new image — similar to Docker multi-stage builds, but declarative and without a daemon.

**4. Conditional steps**

Steps can be guarded with `condition: if-not-exists: /path` — the step is skipped if the file already exists in the current image state. This is useful for idempotent patching: inject a tool only if it is not already present.

**5. skopeo CLI compatibility**

All flags used in the most common `skopeo` workflows are supported with identical names:

```bash
# skopeo — works unchanged as oci-patch
skopeo inspect --creds user:pass docker://registry.example.com/app:latest
skopeo copy --src-creds u:p --dest-creds u:p docker://src/img:tag docker://dst/img:tag
skopeo copy --debug --dest-no-reuse docker://src docker://dst
skopeo --version
```

---

## Installation

```bash
git clone https://github.com/martinmares/oci-patch
cd oci-patch
go build -o oci-patch .
```

Or cross-compile for Linux amd64:

```bash
GOOS=linux GOARCH=amd64 go build -o oci-patch-linux-amd64 .
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
oci-patch copy [flags] SOURCE DESTINATION
```

Copies an image from SOURCE to DESTINATION, applying any specified recipes in order.

**Flags:**

| Flag | Description |
|------|-------------|
| `--src-creds user:pass` | Source registry credentials |
| `--dest-creds user:pass` | Destination registry credentials |
| `--recipe path/to/recipe.yml` | Recipe file to apply (repeatable) |
| `--debug` | Enable verbose output |
| `--batch-file path/to/images.yml` | Run multiple copy jobs from a YAML file |
| `--continue-on-error` | Continue batch processing after a copy fails |
| `--no-wrap-entrypoint` | Disable the automatic entrypoint wrapper for every `shadow` step in the supplied recipes |
| `--dest-tag tag` | Add an extra destination tag in the same repository without copying layers again (repeatable) |
| `--dest-no-reuse` | Disable cross-repository blob mount/reuse at destination (retry fallback) |
| `--reduce` | Materialize the final filesystem into a single minimal layer |
| `--reduce-changes` | Preserve base layers and reduce recipe changes into one delta layer |
| `--progress` | Render terminal progress bars |
| `--progress-json` | Emit machine-readable progress events as JSONL |

**Examples:**

```bash
# Simple copy (no recipe)
oci-patch copy docker://registry.example.com/app:1.0 docker://registry.example.com/app:1.0-copy

# Copy with credentials
oci-patch copy \
  --src-creds srcuser:srcpass \
  --dest-creds dstuser:dstpass \
  docker://src.registry.com/app:2.3-latest \
  docker://dst.registry.com/app:2026.04.17.01

# Copy once and publish additional destination tags for the same manifest
oci-patch copy \
  --dest-creds dstuser:dstpass \
  --dest-tag latest \
  --dest-tag stable \
  docker://src.registry.com/app:2.3-latest \
  docker://dst.registry.com/app:2026.04.17.01

# Copy with a recipe (inject a tool)
oci-patch copy \
  --src-creds user:pass \
  --dest-creds user:pass \
  --recipe inject-tool.yml \
  docker://registry.example.com/app:latest \
  docker://registry.example.com/app:patched

# Copy with multiple recipes applied in sequence
oci-patch copy \
  --recipe recipe-1.yml \
  --recipe recipe-2.yml \
  docker://src/app:tag \
  docker://dst/app:tag

# Digest-based source
oci-patch copy \
  --src-creds user:pass \
  docker://registry.example.com/app@sha256:abc123... \
  docker://registry.example.com/app:stable

# Retry fallback with --dest-no-reuse
oci-patch copy --debug --dest-no-reuse \
  --src-creds user:pass --dest-creds user:pass \
  docker://src/app:tag docker://dst/app:tag

# Machine-readable progress for integrations
oci-patch copy --progress-json \
  docker://src/app:tag docker://dst/app:tag
```

### Batch Copy

Use `--batch-file` when you need to copy multiple images with the same CLI options. The shared flags are applied to every entry in the file.

```yaml
# images.yml
copy:
  - src: registry.example.com/team/config:release-1.0
    dest: registry.example.com/team/config:test-latest
    dest_tags:
      - latest
      - stable
  - src: registry.example.com/team/ticket:release-1.0
    dest: registry.example.com/team/ticket:test-latest
```

```bash
oci-patch copy \
  --batch-file images.yml \
  --src-insecure \
  --dest-insecure \
  --progress
```

Batch jobs run sequentially. Without `--continue-on-error`, the first failed copy stops the batch. With `--continue-on-error`, later entries still run, but the command returns a non-zero exit code at the end if any entry failed and prints a failure summary.

The primary batch keys are `src` and `dest`, matching the `--src-*` and `--dest-*` CLI flags. `source` and `destination` are also accepted as aliases. `dest_tags` adds extra tags in the same destination repository after the image is copied once, equivalent to repeating `--dest-tag` for a single copy command. Duplicate destination images are rejected during validation.

### inspect

```
oci-patch inspect [flags] IMAGE
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
oci-patch inspect docker://docker.io/library/alpine:3.21

# With credentials
oci-patch inspect --creds user:pass docker://registry.example.com/app:latest

# Registry with a non-standard/self-signed certificate
oci-patch inspect --src-insecure docker://registry.example.com/app:latest

# Select specific platform from multi-arch index
oci-patch inspect \
  --override-os linux \
  --override-arch arm64 \
  docker://docker.io/library/alpine:3.21

# Fast inspect without tag list
oci-patch inspect --no-tags docker://registry.example.com/app:latest
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
oci-patch cat [flags] IMAGE PATH
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
oci-patch cat \
  --src-insecure \
  docker://registry.example.com/team/gateway:2.2-test-latest \
  /app/resources/git.properties

# Missing files are silent unless --debug is used
oci-patch cat --debug docker://registry.example.com/app:latest /missing/file
```

### ls

```
oci-patch ls [flags] IMAGE PATH
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
oci-patch ls --src-insecure docker://registry.example.com/app:latest /app/resources
```

### tree

```
oci-patch tree [flags] IMAGE PATH
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
oci-patch tree --src-insecure docker://registry.example.com/app:latest /app
```

### examples command

```
oci-patch examples
```

Shows a command cookbook directly in the terminal: ordinary image copy, batch copy with the complete `images.yml` structure, and complete recipe examples for a Java application, tool and configuration patching, rebasing, artifact extraction, unpacking, `shadow`, permissions, metadata, and layer reduction.

```bash
oci-patch examples

# Equivalent Cobra help form:
oci-patch help examples
```

When stdout is a terminal, commands, YAML keys, comments, and section headings are colorized. Piping or redirecting the output produces plain text, so this is safe for `oci-patch examples | less` and `oci-patch examples > examples.txt`.

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

**`shadow`** — Pack a file tree into an encrypted self-extracting binary and replace the original path with that bootstrap binary. It also wraps the original `ENTRYPOINT`: before starting the original process, `/bin/sh` runs `app.self -cleanup`, which restores the files and removes the bootstrap. The original `ENTRYPOINT` and `CMD` are preserved. By default it reads from the current working image; with `from:` it can read from another named source while writing into the current image. The target image must provide `/bin/sh`.

Do not add `set-entrypoint` after `shadow`, because it would replace this wrapper. Put a custom `set-entrypoint` before `shadow` instead.

Set `wrap-entrypoint: false` to leave the image startup metadata unchanged. In that case, the deployment must run `app.self -cleanup` itself before starting the application.

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
# Leave Entrypoint and Cmd unchanged; start app.self from the deployment instead.
- action: shadow
  path: /app
  extract-to: /app
  output: /app/app.self
  wrap-entrypoint: false
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
oci-patch copy --recipe app.yml --reduce SOURCE DEST

# Original/base layers stay intact, recipe changes become one extra layer.
oci-patch copy --recipe app.yml --reduce-changes SOURCE DEST
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
    value: "oci-patch"
```

```bash
oci-patch copy \
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
oci-patch copy \
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

  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
    setgid-dirs: true
```

```bash
oci-patch copy \
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
let service = SkopeoService::new("/usr/local/bin/oci-patch".to_string());
```

All flags (`--src-creds`, `--dest-creds`, `--debug`, `--dest-no-reuse`, `--creds`, `--override-arch`, `--override-os`) are identical.

---

## Running tests

```bash
go test ./...
```

Tests are fully offline — no registry access required. They use in-memory OCI images built from tar layers.

```
ok  github.com/martinmares/oci-patch/internal/image    (17 tests)
ok  github.com/martinmares/oci-patch/internal/recipe   (11 tests)
```
