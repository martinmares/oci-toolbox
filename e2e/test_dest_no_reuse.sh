#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
COMPOSE_FILE="$ROOT_DIR/e2e/docker-compose.yml"
COMPOSE_PROJECT="oci-patch-e2e"
BIN_PATH="$ROOT_DIR/.tmp/oci-patch-e2e"
CERT_DIR="$ROOT_DIR/e2e/certs"
REGISTRY="127.0.0.1:5443"
SRC_REF="$REGISTRY/src/app:latest"
DST_DEFAULT_REF="$REGISTRY/dst-default/app:latest"
DST_NO_REUSE_REF="$REGISTRY/dst-no-reuse/app:latest"
PROXY_LOG="$ROOT_DIR/.tmp/e2e-proxy.log"
DEFAULT_MOUNT_PATTERN='POST /v2/dst-default/app/blobs/uploads/?from=src%2Fapp&mount='
NO_REUSE_MOUNT_PATTERN='POST /v2/dst-no-reuse/app/blobs/uploads/?from=src%2Fapp&mount='

cleanup() {
  docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

mkdir -p "$ROOT_DIR/.tmp" "$CERT_DIR"
rm -f "$CERT_DIR/tls.crt" "$CERT_DIR/tls.key"
openssl req -x509 -nodes -newkey rsa:2048 -days 1 \
  -keyout "$CERT_DIR/tls.key" \
  -out "$CERT_DIR/tls.crt" \
  -subj "/CN=127.0.0.1" >/dev/null 2>&1

docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" up -d --wait

go build -o "$BIN_PATH" .
go run ./e2e/seed --ref "$SRC_REF"

"$BIN_PATH" copy \
  --src-insecure \
  --dest-insecure \
  "docker://$SRC_REF" \
  "docker://$DST_DEFAULT_REF"

"$BIN_PATH" copy \
  --src-insecure \
  --dest-insecure \
  --dest-no-reuse \
  "docker://$SRC_REF" \
  "docker://$DST_NO_REUSE_REF"

"$BIN_PATH" inspect --insecure "docker://$DST_DEFAULT_REF" >/dev/null
"$BIN_PATH" inspect --insecure "docker://$DST_NO_REUSE_REF" >/dev/null

docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" logs --no-color proxy > "$PROXY_LOG"

if ! grep -Fq "$DEFAULT_MOUNT_PATTERN" "$PROXY_LOG"; then
  echo "expected default copy to attempt a cross-repo mount" >&2
  exit 1
fi

if grep -Fq "$NO_REUSE_MOUNT_PATTERN" "$PROXY_LOG"; then
  echo "expected --dest-no-reuse copy to avoid cross-repo mount attempts" >&2
  exit 1
fi

echo "E2E OK: default copy attempted cross-repo mount; --dest-no-reuse avoided it."
