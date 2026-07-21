#!/usr/bin/env bash
set -euo pipefail

root_dir=$(cd "$(dirname "$0")/.." && pwd)
extractor_dir="$root_dir/internal/shadow/extractor"

targets=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
  "windows/arm64"
)

for target in "${targets[@]}"; do
  os="${target%/*}"
  arch="${target#*/}"
  output="$root_dir/internal/shadow/embedded-extractor-${os}-${arch}"

  echo "Building embedded extractor ${os}/${arch}"
  (
    cd "$extractor_dir"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -trimpath -buildvcs=false -ldflags '-s -w' -o "$output" .
  )
done
