#!/usr/bin/env bash
set -euo pipefail

version="${RELEASE_VERSION:-$(tr -d '[:space:]' < VERSION)}"
package_name="oci-toolbox"
release_dir="dist/release"
assets_file="dist/release_assets.txt"

if [[ -z "${version}" ]]; then
  echo "VERSION is empty" >&2
  exit 1
fi
if [[ -z "${CI_API_V4_URL:-}" || -z "${CI_PROJECT_ID:-}" || -z "${CI_JOB_TOKEN:-}" ]]; then
  echo "GitLab CI variables are missing" >&2
  exit 1
fi

api="${CI_API_V4_URL}/projects/${CI_PROJECT_ID}"
package_base_url="${api}/packages/generic/${package_name}/${version}"
package_files=("${release_dir}/${package_name}-${version}-"*.tar.gz)

if [[ ! -f "${package_files[0]}" ]]; then
  echo "No release archives found for ${package_name} ${version}" >&2
  exit 1
fi
if [[ ! -f "${release_dir}/SHA256SUMS" ]]; then
  echo "Missing ${release_dir}/SHA256SUMS" >&2
  exit 1
fi

: > "${assets_file}"
for file_path in "${package_files[@]}" "${release_dir}/SHA256SUMS"; do
  file_name="$(basename "${file_path}")"
  echo "Uploading ${file_name} to GitLab Generic Package Registry"
  curl --silent --show-error --fail \
    --request PUT \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --upload-file "${file_path}" \
    "${package_base_url}/${file_name}" >/dev/null
  printf '%s\n' "${file_name}" >> "${assets_file}"
done

echo "Uploaded $(wc -l < "${assets_file}" | tr -d '[:space:]') release assets"
