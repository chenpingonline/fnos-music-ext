#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
. "${ROOT}/scripts/version.sh"
STAGE_ONLY=false
ARCH=all
while (($#)); do
  case "$1" in
    --stage-only) STAGE_ONLY=true; shift ;;
    --arch) [[ $# -ge 2 ]] || exit 1; ARCH="$2"; shift 2 ;;
    *) echo 'usage: build-fpk.sh [--stage-only] [--arch all|x86|arm]' >&2; exit 1 ;;
  esac
done
case "$ARCH" in all) platforms=(x86 arm);; x86|arm) platforms=("$ARCH");; *) echo 'invalid architecture' >&2; exit 1;; esac
if ! "$STAGE_ONLY"; then command -v fnpack >/dev/null || { echo 'official fnpack is required' >&2; exit 2; }; fi
"${ROOT}/scripts/build-binaries.sh" "$ARCH"
for platform in "${platforms[@]}"; do
  arch=amd64; [[ "$platform" != arm ]] || arch=arm64
  STAGE="${ROOT}/dist/fpk-stage/${platform}"
  rm -rf "$STAGE"
  mkdir -p "$STAGE/app/bin" "$STAGE/cmd" "$STAGE/config" "$STAGE/wizard"
  cp -a "${ROOT}/dist/linux-${arch}" "$STAGE/app/bin/"
  cp -a "${ROOT}/packaging/fpk/app/ui" "$STAGE/app/ui"
  cp -a "${ROOT}/packaging/fpk/cmd/." "$STAGE/cmd/"
  cp -a "${ROOT}/packaging/fpk/config/." "$STAGE/config/"
  chmod +x "$STAGE/cmd/"*
  sed "s/@PLATFORM@/${platform}/" "${ROOT}/packaging/fpk/manifest.in" > "$STAGE/manifest"
  cp "${ROOT}/packaging/fpk/ICON.PNG" "${ROOT}/packaging/fpk/ICON_256.PNG" "${ROOT}/LICENSE" "$STAGE/"
  echo "[stage] $STAGE"
  if ! "$STAGE_ONLY"; then
    (cd "$STAGE" && fnpack build)
    mv "$STAGE/fnmusic-go.fpk" "${ROOT}/dist/fnmusic-go-${VERSION}-${platform}.fpk"
  fi
done
if ! "$STAGE_ONLY"; then
  python3 - "$ROOT" "$VERSION" "${platforms[@]}" <<'PY'
import hashlib,sys
from pathlib import Path
root,version,*platforms=sys.argv[1:]
files=[Path(root)/'dist'/f'fnmusic-go-{version}-{p}.fpk' for p in platforms]
(Path(root)/'dist/FPK-SHA256SUMS').write_text(''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n' for p in files))
PY
fi
