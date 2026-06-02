#!/usr/bin/env bash
set -euo pipefail

version="${RELEASE_VERSION:-$(tr -d '[:space:]' < VERSION)}"
commit="${CI_COMMIT_SHORT_SHA:-$(git rev-parse --short HEAD 2>/dev/null || printf unknown)}"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
package="oci-patch"
binary="oci-patch"
release_dir="dist/release"

if [[ -z "${version}" ]]; then
  echo "VERSION is empty" >&2
  exit 1
fi

rm -rf "${release_dir}"
mkdir -p "${release_dir}" .tmp/go-build-cache

ldflags="-s -w -X github.com/martinmares/oci-patch/cmd.version=${version} -X github.com/martinmares/oci-patch/cmd.commit=${commit} -X github.com/martinmares/oci-patch/cmd.date=${date}"

targets=(
  "linux/amd64"
  "linux/arm64"
  "darwin/arm64"
  "darwin/amd64"
)

for target in "${targets[@]}"; do
  os="${target%/*}"
  arch="${target#*/}"

  archive_base="${package}-${version}-${os}-${arch}"
  work_dir="${release_dir}/${archive_base}"
  mkdir -p "${work_dir}"

  echo "Building ${archive_base}"
  GOCACHE="${PWD}/.tmp/go-build-cache" \
  CGO_ENABLED=0 \
  GOOS="${os}" \
  GOARCH="${arch}" \
  go build -trimpath -ldflags "${ldflags}" -o "${work_dir}/${binary}" .

  cp README.md README.cs.md "${work_dir}/"
  tar -C "${release_dir}" -czf "${release_dir}/${archive_base}.tar.gz" "${archive_base}"
  rm -rf "${work_dir}"
done

(
  cd "${release_dir}"
  sha256sum *.tar.gz > SHA256SUMS
)

echo
ls -lh "${release_dir}"/*.tar.gz "${release_dir}/SHA256SUMS"
