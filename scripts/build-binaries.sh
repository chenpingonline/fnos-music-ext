#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "${ROOT}/dist/linux-amd64" "${ROOT}/dist/linux-arm64"
cd "${ROOT}"
for arch in amd64 arm64; do
  echo "[build] linux/${arch}"
  CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" go build -trimpath -ldflags '-s -w' -o "dist/linux-${arch}/fnmusic-go" ./cmd/fnmusic-go
done
sha256sum dist/linux-amd64/fnmusic-go dist/linux-arm64/fnmusic-go > dist/SHA256SUMS
