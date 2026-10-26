# ImageRelease v2

`ImageRelease` is the native release inventory used by oci-toolbox and
kube-build-app. It is not an OCI image manifest or descriptor.

```yaml
apiVersion: oci-toolbox/v2
kind: ImageRelease
release_id: customer-release-1
created_at: 2026-10-06T07:00:00Z
images:
  - id: config
    app_name: config-form
    container_name: main
    repository: registry.example.com/product/config
    tag: 2.4.11.2
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

Every repository is fully qualified and excludes a tag or digest. `digest`
identifies the referenced image manifest or image index, not Docker's local
ImageID. The pull reference is `repository@digest`. `tag` is optional and need
not equal `release_id`. `id` is a logical inventory entry ID, preserved when
repository locations change.

Different entries can use different registries. Optional `repository_prefix`
records a genuinely shared repository prefix, including the registry host.
It is not a registry credential or a required registry identity. The current
`copy --release-manifest` operation still requires one common source prefix,
provided by this field or `--src-base`; multi-registry inventory support does
not remove that operation's restriction.

Optional `source` describes the previous location of a copy, using
`repository`, `tag`, and `digest`. It is not a permanent supplier provenance
chain. `bundle`, when present, describes an actual legacy bundle. External product manifests and import provenance remain integration concerns;
this native inventory does not define a finished KRF product model.

`platform`, when known, describes the referenced platform-specific image as
`os/arch[/variant]`. Unknown platforms and image indexes omit this field;
v2 output never writes the legacy `multi-platform` sentinel. Optional global
`platform`, `extra_tags`, image `platform` and `extra_tags`, and bundle metadata
remain supported. `created_at` records creation of the release document.

## Stable default and scope

Ordinary copy, bundle publish and release reconstruct commands continue to emit
v1, including when their input is v2. `release convert` is the explicit opt-in
to v2. No migration or completed KRF implementation is required. These are local
native format capabilities; they do not imply completion of the KRF product flow.
The default output contract is deliberately not switched in the 1.x release.

## Compatibility and conversion

| v1 field | v2 field |
| --- | --- |
| `images[].image` | `images[].repository` |
| `images[].source.image` | `images[].source.repository` |
| `registry_base` | `repository_prefix` |

Current oci-toolbox readers accept explicit v1, explicit v2, and historical
unversioned v1 documents. Unknown versions, incomplete headers, unknown
fields, duplicate selectors/IDs and multiple YAML documents are rejected.
YAML aliases and merges are resolved before version-specific field checks.
The field names must match the declared version; mixed v1/v2 schemas are not
accepted. Only explicit `release convert` output uses v2; ordinary release output
stays v1. Existing v1 files do not require an in-place migration. Older binaries
cannot consume v2; check every downstream consumer before opting into new-format
manifests.

```bash
oci-toolbox release convert releases/old.yml --output releases/new.yml
oci-toolbox release convert releases/old.yml > /tmp/new.yml
oci-toolbox release compare releases/old.yml releases/new.yml --mode full --fail-on-change
```

Conversion is offline: it does not resolve tags, copy images, or access a
registry. It preserves release ID, creation time, repository locations,
digests, selectors, tags, bundle metadata and extra tags. Only field names,
version header and the legacy `multi-platform` sentinel are normalized.
The source is untouched; `--output` rejects existing files. The default output
is stdout. V2 input may also be validated and rewritten as canonical v2.

A release ID is a logical release identifier. Docker tag validation applies
when a particular operation uses that ID as a destination tag, rather than
being a blanket restriction on reading an inventory.
