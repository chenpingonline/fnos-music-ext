#!/usr/bin/env bash
# Build a real fnpack-produced x86 package. Requires official fnpack 1.2.3+.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGE="${ROOT}/dist/fpk-official-x86"
command -v fnpack >/dev/null || { echo 'Please install the official fnpack first' >&2; exit 2; }
mkdir -p "${ROOT}/dist"
rm -rf "${STAGE}"
mkdir -p "${STAGE}/app/bin/linux-amd64" "${STAGE}/cmd" "${STAGE}/config" "${STAGE}/wizard"
(cd "${ROOT}" && GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "${STAGE}/app/bin/linux-amd64/fnmusic-go" ./cmd/fnmusic-go)
cp -a "${ROOT}/packaging/fpk/app/ui" "${STAGE}/app/ui"
cp -a "${ROOT}/packaging/fpk/cmd/." "${STAGE}/cmd/"
cp -a "${ROOT}/packaging/fpk/config/." "${STAGE}/config/"
cp "${ROOT}/packaging/fpk/ICON.PNG" "${ROOT}/packaging/fpk/ICON_256.PNG" "${STAGE}/"
cp "${ROOT}/LICENSE" "${STAGE}/LICENSE"
chmod +x "${STAGE}/cmd/"*
sed -e 's/@VERSION@/0.5.0/' -e 's/^platform[[:space:]]*=.*/platform              = x86/' "${ROOT}/packaging/fpk/manifest.in" > "${STAGE}/manifest"
python3 - "${STAGE}/app/ui/config" <<'PY'
import json,sys
from pathlib import Path
p=Path(sys.argv[1]); data=json.loads(p.read_text())
v=data['.url']['fnmusic-go.main']
v.pop('port',None); v.pop('protocol',None)
p.write_text(json.dumps(data,ensure_ascii=False,indent=2)+'\n')
PY
(cd "${STAGE}" && fnpack build)
echo "Inspect fnpack output under ${STAGE} and validate installation on fnOS."
