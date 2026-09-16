set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

version := `tr -d '[:space:]' < VERSION`
commit := `git rev-parse --short HEAD 2>/dev/null || printf unknown`
date := `date -u +%Y-%m-%dT%H:%M:%SZ`

# Upgrade all Go modules, including the nested embedded extractor module.
# This also refreshes checksums, runs tests, and rebuilds the embedded binary.
upgrade-modules:
    go get -u ./...
    # kube-openapi has no coordinated release tags; use the exact revision required by the selected Kubernetes modules.
    k8s_version="$(go list -m k8s.io/apimachinery | awk '{print $2}')"; kube_openapi_version="$(awk '$1 == "k8s.io/kube-openapi" { print $2 }' "$(go env GOMODCACHE)/cache/download/k8s.io/apimachinery/@v/${k8s_version}.mod")"; test -n "${kube_openapi_version}"; go get "k8s.io/kube-openapi@${kube_openapi_version}"
    go mod tidy
    cd internal/shadow/extractor && go get -u ./... && go mod tidy
    go test ./...
    cd internal/shadow/extractor && go test ./...
    just build-macos

# Build a static Linux amd64 binary in dist/.
build-linux: build-embedded-extractors
    mkdir -p dist
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w -X github.com/martinmares/oci-toolbox/cmd.version={{version}} -X github.com/martinmares/oci-toolbox/cmd.commit={{commit}} -X github.com/martinmares/oci-toolbox/cmd.date={{date}}' -o dist/oci-toolbox-linux-amd64 .

# Build a static macOS arm64 binary in dist/.
build-macos: build-embedded-extractors
    mkdir -p dist
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '-s -w -X github.com/martinmares/oci-toolbox/cmd.version={{version}} -X github.com/martinmares/oci-toolbox/cmd.commit={{commit}} -X github.com/martinmares/oci-toolbox/cmd.date={{date}}' -o dist/oci-toolbox-darwin-arm64 .

# Build a static Windows amd64 binary in dist/.
build-windows: build-embedded-extractors
    mkdir -p dist
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w -X github.com/martinmares/oci-toolbox/cmd.version={{version}} -X github.com/martinmares/oci-toolbox/cmd.commit={{commit}} -X github.com/martinmares/oci-toolbox/cmd.date={{date}}' -o dist/oci-toolbox-windows-amd64.exe .

# Build all platform-specific extractors embedded into every oci-toolbox binary.
build-embedded-extractors:
    bash scripts/build_embedded_extractors.sh
