#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/ci_release_common.sh"

version="$RELEASE_VERSION"
tag="$RELEASE_TAG"
notes_file="dist/release_notes.md"
assets_file="dist/release_assets.txt"
package_name="oci-toolbox"

if [[ -z "${version}" ]]; then
  echo "VERSION is empty" >&2
  exit 1
fi
if [[ ! -f "${notes_file}" ]]; then
  echo "Missing ${notes_file}" >&2
  exit 1
fi
if [[ ! -s "${assets_file}" ]]; then
  echo "Missing or empty ${assets_file}" >&2
  exit 1
fi
if [[ -z "${CI_API_V4_URL:-}" || -z "${CI_PROJECT_ID:-}" || -z "${CI_JOB_TOKEN:-}" ]]; then
  echo "GitLab CI variables are missing" >&2
  exit 1
fi

api="${CI_API_V4_URL}/projects/${CI_PROJECT_ID}"
package_base_url="${api}/packages/generic/${package_name}/${version}"
tag_path="$(jq -nr --arg tag "$tag" '$tag | @uri')"
response="$(mktemp)"
trap 'rm -f "$response"' EXIT

get_optional() {
  local status
  status="$(curl --silent --show-error --output "$response" --write-out '%{http_code}' \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" "$1")"
  case "$status" in
    200) return 0 ;;
    404) return 1 ;;
    *) echo "GitLab API returned HTTP $status" >&2; exit 1 ;;
  esac
}

if get_optional "$api/repository/tags/$tag_path"; then
  if [[ "$(jq -r '.commit.id' "$response")" != "$RELEASE_COMMIT" ]]; then
    echo "Release tag points to a different commit; bump VERSION" >&2
    exit 1
  fi
fi

if get_optional "$api/releases/$tag_path"; then
  if [[ "$(jq -r '.commit.id' "$response")" != "$RELEASE_COMMIT" ]]; then
    echo "Existing release belongs to a different commit" >&2
    exit 1
  fi
  curl --silent --show-error --fail \
    --request PUT \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --form "name=oci-toolbox ${tag}" \
    --form "description=<${notes_file}" \
    "${api}/releases/${tag_path}" >/dev/null
else
  curl --silent --show-error --fail \
    --request POST \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --form "tag_name=${tag}" \
    --form "ref=${RELEASE_COMMIT}" \
    --form "name=oci-toolbox ${tag}" \
    --form "description=<${notes_file}" \
    "${api}/releases" >/dev/null
fi
existing_links="$(curl --silent --show-error --fail --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
  "$api/releases/$tag_path/assets/links?per_page=100")"

link_asset() {
  local file_name="$1"
  local asset_name package_url id method endpoint
  asset_name="${file_name%.tar.gz}"
  package_url="${package_base_url}/${file_name}"
  id="$(jq -r --arg name "$asset_name" '.[] | select(.name == $name) | .id' <<< "$existing_links")"
  method=POST
  endpoint="$api/releases/$tag_path/assets/links"
  if [[ -n "$id" ]]; then method=PUT; endpoint="$endpoint/$id"; fi

  echo "Linking ${file_name} to GitLab release ${tag}"
  curl --silent --show-error --fail \
    --request "$method" \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --form "name=${asset_name}" \
    --form "url=${package_url}" \
    --form "link_type=package" \
    "$endpoint" >/dev/null
}

while IFS= read -r file_name; do
  [[ -n "${file_name}" ]] || continue
  if [[ "${file_name}" == */* || "${file_name}" == "." || "${file_name}" == ".." ]]; then
    echo "Invalid release asset name in ${assets_file}: ${file_name}" >&2
    exit 1
  fi
  link_asset "${file_name}"
done < "${assets_file}"

if [[ -n "${RELEASE_PUSH_TOKEN:-}" && -f dist/CHANGELOG.generated.md && -n "${CI_COMMIT_BRANCH:-}" ]]; then
  git config user.name "${GITLAB_USER_NAME:-GitLab CI}"
  git config user.email "${GITLAB_USER_EMAIL:-gitlab-ci@${CI_SERVER_HOST}}"
  git checkout -B "${CI_COMMIT_BRANCH}" "${CI_COMMIT_SHA}"
  cp dist/CHANGELOG.generated.md CHANGELOG.md
  if ! git diff --quiet -- CHANGELOG.md; then
    git add CHANGELOG.md
    git commit -m "docs: update changelog for ${tag} [skip ci]"
    git push "https://oauth2:${RELEASE_PUSH_TOKEN}@${CI_SERVER_HOST}/${CI_PROJECT_PATH}.git" "HEAD:${CI_COMMIT_BRANCH}"
  fi
else
  echo "Skipping CHANGELOG.md push (set RELEASE_PUSH_TOKEN to enable)"
fi

echo "Published GitLab release ${tag}"
