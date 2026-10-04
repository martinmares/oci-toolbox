#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/ci_release_common.sh"

version="$RELEASE_VERSION"
commit="${RELEASE_COMMIT:0:12}"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
package="oci-toolbox"
binary="oci-toolbox"
release_dir="dist/release"

if [[ -z "${version}" ]]; then
  echo "VERSION is empty" >&2
  exit 1
fi

bash scripts/build_embedded_extractors.sh

mkdir -p "${release_dir}" .tmp/go-build-cache
package_work_dir="$(mktemp -d "${TMPDIR:-/tmp}/oci-toolbox-release.XXXXXX")"
trap 'rm -rf "$package_work_dir"' EXIT

ldflags="-s -w -X github.com/martinmares/oci-toolbox/cmd.version=${version} -X github.com/martinmares/oci-toolbox/cmd.commit=${commit} -X github.com/martinmares/oci-toolbox/cmd.date=${date}"

targets=(
  "linux/amd64"
  "linux/arm64"
  "darwin/arm64"
  "darwin/amd64"
  "windows/amd64"
  "windows/arm64"
)

for target in "${targets[@]}"; do
  os="${target%/*}"
  arch="${target#*/}"

  archive_base="${package}-${version}-${os}-${arch}"
  work_dir="${package_work_dir}/${archive_base}"
  output_name="${binary}"
  if [[ "${os}" == "windows" ]]; then
    output_name="${binary}.exe"
  fi
  mkdir -p "${work_dir}"

  echo "Building ${archive_base}"
  GOCACHE="${PWD}/.tmp/go-build-cache" \
  CGO_ENABLED=0 \
  GOOS="${os}" \
  GOARCH="${arch}" \
  go build -trimpath -ldflags "${ldflags}" -o "${work_dir}/${output_name}" .

  cp README.md README.cs.md LICENSE "${work_dir}/"
  tar -C "${package_work_dir}" -czf "${release_dir}/${archive_base}.tar.gz" "${archive_base}"
done

(
  cd "${release_dir}"
  release_checksum -- "${package}-${version}-"*.tar.gz > SHA256SUMS
)

echo
ls -lh "${release_dir}"/*.tar.gz "${release_dir}/SHA256SUMS"
