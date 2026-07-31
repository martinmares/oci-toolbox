# Spring Boot Configuration Discovery

## Status

Implementation and handoff notes for the static Spring Boot configuration
discovery feature.

Last updated: 2026-07-31.

Current implementation state:

- [x] The CLI shape and analysis boundary were agreed.
- [x] `oci-toolbox spring-config discover` was implemented.
- [x] The OCI image, JIB classpath, and JAR scanner was implemented.
- [x] The code was formatted and compiled.
- [x] Scanner edge cases and effective OCI filesystem semantics were reviewed.
- [x] Unit and command tests were added.
- [x] README.md, README.cs.md, and `oci-toolbox examples` were updated.
- [x] The command was tested against a real JIB image.
- [x] Optional phase reporting was added through `--progress`.
- [x] A searchable `manifest.svg` classpath/configuration map was added.

The feature is complete within the static-analysis boundary described below.

## Goal

Add a read-only analysis command that discovers Spring Boot configuration
packaged in a JIB-built OCI image. This is not a recipe action and must not
modify or create an OCI image.

Command:

```bash
oci-toolbox spring-config discover \
  docker://registry.example.com/team/application:2.4 \
  --output ./spring-config-analysis \
  --progress \
  --src-insecure
```

The command should:

1. Read `/app/jib-classpath-file`.
2. Read `/app/jib-main-class-file`.
3. Preserve classpath order.
4. Scan classpath directories for Spring configuration candidates.
5. Open classpath JAR/ZIP files and scan them without executing application
   code.
6. Export every matching file without merging or changing it.
7. Preserve duplicate filenames by grouping output by classpath entry.
8. Write a deterministic `manifest.yml` with image, classpath, source,
   checksum, size, and extraction metadata.
9. Write `manifest.svg` as a visual map of the same classpath/configuration
   relationships.

## Analysis Boundary

This command provides the static OCI-image portion of a larger Spring
configuration analysis. It must not claim to reconstruct the final runtime
Spring Environment.

The complete runtime picture also needs:

- Kubernetes command, args, environment variables, ConfigMaps, Secrets, and
  mounted files.
- JVM `-D` properties.
- `SPRING_APPLICATION_JSON`.
- Spring Cloud Config sources.
- Active profiles.
- Actuator `/actuator/env` and preferably `/actuator/configprops`.

The command must not merge YAML or infer the winning value of a property.

## Important Finding

A file named `application-commons.yml` is not necessarily activated by a
profile named `commons`. A real application inspected during the design phase
loaded this file explicitly:

```kotlin
@PropertySource(
    value = [
        "classpath:application-commons.yml",
        "classpath:database-copy-defaults.yml"
    ],
    factory = YamlPropertySourceFactory::class
)
```

Actuator confirmed that `application-commons.yml` was a property source even
though the active profile was unrelated.

Therefore the manifest may report a filename-derived `profile_hint`, but its
activation must remain `unknown` until bytecode or runtime evidence proves the
mechanism.

## Default Candidate Names

The implementation discovers:

```text
application.yml
application.yaml
application.properties
application-*.yml
application-*.yaml
application-*.properties
bootstrap.yml
bootstrap.yaml
bootstrap.properties
bootstrap-*.yml
bootstrap-*.yaml
bootstrap-*.properties
```

It also discovers every root-level `.yml`, `.yaml`, and `.properties` file in
each classpath directory or archive. This includes custom property sources such
as `application-commons.yml` and `database-copy-defaults.yml`. Additional
archive path or basename globs can be supplied with repeated `--include` flags.

## Output Layout

Expected output:

```text
spring-config-analysis/
|-- manifest.yml
|-- manifest.svg
|-- classpath.txt
`-- sources/
    |-- 0000-resources/
    |   |-- application.yml
    |   `-- application-kubernetes.yml
    `-- 0047-application-commons.jar/
        `-- application-commons.yml
```

Output is created through a temporary sibling directory and renamed only after
successful analysis. Existing output directories must not be overwritten.
Directories use mode `0700`; extracted configuration and manifest files use
mode `0600` because configuration can contain secrets.

`manifest.svg` groups configurations below their classpath entry. Classpath
entries without discoveries are omitted from the diagram and counted in its
footer. The YAML manifest remains the complete machine-readable source.

## Current Files

### `cmd/spring_config.go`

Cobra commands:

```text
spring-config
`-- discover IMAGE
```

Flags:

- `--output`, `-o` (required by runtime validation)
- `--classpath-file`
- `--main-class-file`
- `--include` (repeatable)
- `--creds`
- `--insecure` / `--src-insecure`
- `--override-os`
- `--override-arch`
- `--override-variant`
- `--progress`
- `--debug`

The command uses the existing `loadImage` helper and prints a short completion
summary.

### `internal/springconfig/discover.go`

Implementation includes:

- classpath parsing;
- relative classpath resolution against the image working directory;
- explicit JAR and directory entries;
- wildcard JAR discovery;
- one-pass OCI layer scanning with whiteout handling;
- temporary staging of JAR files instead of keeping all JARs in memory;
- ZIP/JAR traversal protection;
- per-file and total extraction size limits;
- SHA-256 and size recording;
- collision-resistant output paths;
- atomic output directory publication;
- YAML manifest encoding.

## Manifest

The manifest records:

```yaml
image:
  reference: docker://registry.example.com/team/application:2.4
  digest: sha256:...
  platform: linux/amd64
  workdir: /app

jib:
  classpath_file: /app/jib-classpath-file
  main_class_file: /app/jib-main-class-file
  main_class: com.example.Application

classpath:
  - index: 0
    path: /app/resources
    kind: directory
    status: found

configs:
  - id: config-0001
    classpath_index: 0
    classpath_path: /app/resources
    source: /app/resources/application.yml
    output: sources/0000-resources/application.yml
    name_kind: application
    activation: unknown
    sha256: ...
    size: 1234
```

## Verification

Completed checks:

1. `gofmt` on the new and modified Go files.
2. `go test ./...`.
3. `git diff --check`.
4. Real registry test against a JIB-built Spring Boot application image.

The real-image test found 44 configuration files across 289 classpath entries.
It found the expected application files in `/app/resources`, the embedded
`application-commons.yml`, and `database-copy-defaults.yml` in separate JARs.
The manifest preserved their classpath indices and archive origins. There were
no scanner warnings. The output directory used mode `0700`, files used mode
`0600`, and temporary `.work` data was removed.

## Known Limits

- This is static file discovery, not Spring property resolution.
- It does not inspect bytecode to prove `@PropertySource` activation.
- It does not evaluate `spring.config.import` or profile expressions.
- It does not read Kubernetes environment variables, ConfigMaps, Secrets, or
  Spring Cloud Config.
- JIB classpath parsing currently follows Linux `:` separators. The CLI itself
  can be built for Windows, but the analyzed application image is expected to
  be Linux-based.
- Missing classpath entries are warnings. Malformed archives, unsafe paths, and
  configured extraction limits are hard failures.

## Possible Follow-up

No follow-up is required for the agreed scope. A future higher-level analysis
could combine this manifest with Kubernetes deployment inputs and Actuator
snapshots. That should remain a separate feature so this command stays
deterministic, read-only, and usable without starting the application.
