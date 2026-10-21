#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
. "${ROOT}/scripts/version.sh"
case "${1:-all}" in
  all) architectures=(amd64 arm64) ;;
  x86) architectures=(amd64) ;;
  arm) architectures=(arm64) ;;
  *) echo 'usage: build-binaries.sh [all|x86|arm]' >&2; exit 1 ;;
esac
cd "$ROOT"
bash "${ROOT}/scripts/check-toolchains.sh"
npm ci --prefix web
npm run build --prefix web
for arch in "${architectures[@]}"; do
  mkdir -p "dist/linux-${arch}"
  echo "[build] linux/${arch} ${VERSION}"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOAMD64=v1 go build -trimpath -ldflags "$BUILD_FLAGS" -o "dist/linux-${arch}/fnmusic-go" ./cmd/fnmusic-go
done
python3 - "${architectures[@]}" <<'PY'
import hashlib,sys
from pathlib import Path
files=[Path(f'dist/linux-{arch}/fnmusic-go') for arch in sys.argv[1:]]
Path('dist/SHA256SUMS').write_text(''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p}\n' for p in files))
PY
