#!/usr/bin/env bash
# Shared release metadata and immutable tag checks. Run from the repository root.
set -euo pipefail

file_version="$(tr -d '\r\n' < VERSION)"
if [[ ! "$file_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "VERSION must contain a semantic version without a v prefix (no build metadata)" >&2
  exit 1
fi
RELEASE_VERSION="${RELEASE_VERSION:-$file_version}"
RELEASE_TAG="${RELEASE_TAG:-v$RELEASE_VERSION}"
RELEASE_COMMIT="${RELEASE_COMMIT:-${CI_COMMIT_SHA:-${GITHUB_SHA:-$(git rev-parse HEAD)}}}"
if [[ "$RELEASE_VERSION" != "$file_version" || "$RELEASE_TAG" != "v$file_version" ]]; then
  echo "Release version/tag does not match VERSION" >&2
  exit 1
fi
if [[ ! "$RELEASE_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Release commit must be a full Git commit SHA" >&2
  exit 1
fi
if git show-ref --verify --quiet "refs/tags/$RELEASE_TAG"; then
  if [[ "$(git rev-parse "$RELEASE_TAG^{commit}")" != "$RELEASE_COMMIT" ]]; then
    echo "Tag $RELEASE_TAG already points to a different commit; bump VERSION" >&2
    exit 1
  fi
fi

release_checksum() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}
