#!/usr/bin/env bash
# Official x86 fnpack build, with no Python or Docker dependency.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGE="$ROOT/dist/fpk-x86-stage"
rm -rf "$STAGE"
mkdir -p "$STAGE/app/bin/linux-amd64" "$STAGE/cmd" "$STAGE/config" "$STAGE/wizard"
(cd "$ROOT" && GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 GOPROXY=off GOSUMDB=off go build -trimpath -ldflags='-s -w' -o "$STAGE/app/bin/linux-amd64/fnmusic-go" ./cmd/fnmusic-go)
cp -a "$ROOT/packaging/fpk/app/ui" "$STAGE/app/ui"
jq '.[".url"]["fnmusic-go.main"] |= (del(.protocol,.port) | .desc="fnMusic Go 0.5.0 x86")' "$STAGE/app/ui/config" > "$STAGE/app/ui/config.tmp"
mv "$STAGE/app/ui/config.tmp" "$STAGE/app/ui/config"
cp -a "$ROOT/packaging/fpk/cmd/." "$STAGE/cmd/"
cp -a "$ROOT/packaging/fpk/config/." "$STAGE/config/"
chmod 755 "$STAGE/cmd/"*
cp "$ROOT/packaging/fpk/ICON.PNG" "$ROOT/packaging/fpk/ICON_256.PNG" "$STAGE/"
cp "$ROOT/LICENSE" "$STAGE/LICENSE"
sed -e 's/@VERSION@/0.5.0/g' -e 's/^platform[[:space:]]*=.*/platform              = x86/' "$ROOT/packaging/fpk/manifest.in" > "$STAGE/manifest"
for item in app/ui/config cmd/main config/privilege config/resource manifest ICON.PNG ICON_256.PNG; do test -f "$STAGE/$item"; done
jq -e . "$STAGE/config/privilege" >/dev/null
jq -e . "$STAGE/config/resource" >/dev/null
jq -e . "$STAGE/app/ui/config" >/dev/null
if test "$#" -gt 0 && test "$1" = "--stage-only"; then exit 0; fi
command -v fnpack >/dev/null || { echo "official fnpack is required" >&2; exit 2; }
fnpack build --directory "$STAGE"
mkdir -p "$ROOT/dist/release"
LATEST="$(find "$ROOT/dist" -type f -name '*.fpk' -printf '%T@ %p\n' | sort -nr | head -1 | cut -d' ' -f2-)"
test -n "$LATEST"
OUT="$ROOT/dist/release/fnmusic-go-0.5.0-x86-fnpack.fpk"
cp "$LATEST" "$OUT"
EXPECTED="$(tar xOzf "$OUT" manifest | sed -nE 's/^checksum[[:space:]]*=[[:space:]]*([0-9a-fA-F]{32}).*/\1/p' | head -1)"
ACTUAL="$(tar xOzf "$OUT" app.tgz | md5sum | cut -d' ' -f1)"
test -n "$EXPECTED" && test "$EXPECTED" = "$ACTUAL"
sha256sum "$OUT"