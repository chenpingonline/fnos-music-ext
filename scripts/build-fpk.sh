#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
"${ROOT}/scripts/build-binaries.sh"
STAGE="${ROOT}/dist/fpk-stage"
rm -rf "${STAGE}"
mkdir -p "${STAGE}/app/bin" "${STAGE}/app/ui" "${STAGE}/cmd" "${STAGE}/config" "${STAGE}/wizard"
cp -a "${ROOT}/dist/linux-amd64" "${ROOT}/dist/linux-arm64" "${STAGE}/app/bin/"
cp -a "${ROOT}/packaging/fpk/app/ui/." "${STAGE}/app/ui/"
cp -a "${ROOT}/packaging/fpk/cmd/." "${STAGE}/cmd/"
cp -a "${ROOT}/packaging/fpk/config/." "${STAGE}/config/"
for file in "${STAGE}/cmd/"*; do chmod +x "$file"; done
sed 's/@VERSION@/0.3.0/' "${ROOT}/packaging/fpk/manifest.in" > "${STAGE}/manifest"
cp "${ROOT}/packaging/fpk/ICON.PNG" "${ROOT}/packaging/fpk/ICON_256.PNG" "${STAGE}/"
echo "[stage] ${STAGE}"
if [[ "${1:-}" == "--stage-only" ]]; then exit 0; fi
if ! command -v fnpack >/dev/null 2>&1; then
  echo 'fnpack is not installed; staging prepared, but no .fpk was produced.' >&2
  exit 2
fi
(cd "${ROOT}/dist" && fnpack build --directory "${STAGE}")
