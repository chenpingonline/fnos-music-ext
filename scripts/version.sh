#!/usr/bin/env bash
# Sourced by build scripts. manifest.in is the only editable version source.
VERSION="$(awk -F= '/^version[[:space:]]*=/{gsub(/[[:space:]]/,"",$2); print $2}' "${ROOT}/packaging/fpk/manifest.in")"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'invalid manifest version' >&2; exit 1; }
BUILD_FLAGS="-s -w -X github.com/chenpingonline/fnos-music-ext/internal/buildinfo.Version=${VERSION}"
