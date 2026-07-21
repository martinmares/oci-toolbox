set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

version := `tr -d '[:space:]' < VERSION`
commit := `git rev-parse --short HEAD 2>/dev/null || printf unknown`
date := `date -u +%Y-%m-%dT%H:%M:%SZ`

# Upgrade all Go modules, including the nested embedded extractor module.
# This also refreshes checksums, runs tests, and rebuilds the embedded binary.
upgrade-modules:
    go get -u ./...
    go mod tidy
    cd internal/shadow/extractor && go get -u ./... && go mod tidy
    go test ./...
    cd internal/shadow/extractor && go test ./...
    just build-macos

# Build a static Linux amd64 binary in dist/.
build-linux: build-embedded-extractor
    mkdir -p dist
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w -X github.com/martinmares/oci-patch/cmd.version={{version}} -X github.com/martinmares/oci-patch/cmd.commit={{commit}} -X github.com/martinmares/oci-patch/cmd.date={{date}}' -o dist/oci-patch-linux-amd64 .

# Build a static macOS arm64 binary in dist/.
build-macos: build-embedded-extractor
    mkdir -p dist
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '-s -w -X github.com/martinmares/oci-patch/cmd.version={{version}} -X github.com/martinmares/oci-patch/cmd.commit={{commit}} -X github.com/martinmares/oci-patch/cmd.date={{date}}' -o dist/oci-patch-darwin-arm64 .

# Build a static Windows amd64 binary in dist/.
build-windows: build-embedded-extractor
    mkdir -p dist
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w -X github.com/martinmares/oci-patch/cmd.version={{version}} -X github.com/martinmares/oci-patch/cmd.commit={{commit}} -X github.com/martinmares/oci-patch/cmd.date={{date}}' -o dist/oci-patch-windows-amd64.exe .

# Build the Linux amd64 extractor embedded into every oci-patch binary.
build-embedded-extractor:
    cd internal/shadow/extractor && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o ../embedded-extractor-linux-amd64 .
