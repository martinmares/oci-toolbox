#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/ci_release_common.sh"

version="$RELEASE_VERSION"
if [[ -z "${version}" ]]; then
  echo "VERSION is empty" >&2
  exit 1
fi

tag="$RELEASE_TAG"
mkdir -p dist

previous_tag=""
while IFS= read -r candidate; do
  if [[ "$candidate" != "$tag" ]]; then previous_tag="$candidate"; break; fi
done < <(git tag --merged "$RELEASE_COMMIT" --list 'v[0-9]*' --sort=-version:refname)

range="$RELEASE_COMMIT"
if [[ -n "${previous_tag}" ]]; then
  range="${previous_tag}..${RELEASE_COMMIT}"
fi

release_date="$(date -u +%Y-%m-%d)"

{
  echo "## ${tag} - ${release_date}"
  echo
  if [[ -n "${previous_tag}" ]]; then
    echo "Changes since ${previous_tag}:"
  else
    echo "Initial release:"
  fi
  echo
  git log --no-merges --pretty=format:'- %s (%h)' "${range}" || true
  echo
} > dist/release_notes.md

{
  echo "# Changelog"
  echo
  cat dist/release_notes.md
  echo
  if [[ -f CHANGELOG.md ]]; then
    sed '1{/^# Changelog$/d;}' CHANGELOG.md
  fi
} > dist/CHANGELOG.generated.md

{
  echo "RELEASE_VERSION=${version}"
  echo "RELEASE_TAG=${tag}"
  echo "RELEASE_COMMIT=${RELEASE_COMMIT}"
  echo "PREVIOUS_RELEASE_TAG=${previous_tag}"
} > dist/release.env

cat dist/release_notes.md
