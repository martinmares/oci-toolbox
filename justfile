set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

version := `tr -d '[:space:]' < VERSION`
commit := `git rev-parse --short HEAD 2>/dev/null || printf unknown`
date := `date -u +%Y-%m-%dT%H:%M:%SZ`

# Build a static Linux amd64 binary in dist/.
build-linux:
    mkdir -p dist
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w -X github.com/martinmares/oci-patch/cmd.version={{version}} -X github.com/martinmares/oci-patch/cmd.commit={{commit}} -X github.com/martinmares/oci-patch/cmd.date={{date}}' -o dist/oci-patch-linux-amd64 .

# Build a static macOS arm64 binary in dist/.
build-macos:
    mkdir -p dist
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '-s -w -X github.com/martinmares/oci-patch/cmd.version={{version}} -X github.com/martinmares/oci-patch/cmd.commit={{commit}} -X github.com/martinmares/oci-patch/cmd.date={{date}}' -o dist/oci-patch-darwin-arm64 .

# Build a static Windows amd64 binary in dist/.
build-windows:
    mkdir -p dist
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w -X github.com/martinmares/oci-patch/cmd.version={{version}} -X github.com/martinmares/oci-patch/cmd.commit={{commit}} -X github.com/martinmares/oci-patch/cmd.date={{date}}' -o dist/oci-patch-windows-amd64.exe .
