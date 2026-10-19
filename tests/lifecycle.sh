#!/usr/bin/env bash
# Run only in an expendable Linux container. This test owns its systemd paths.
set -euo pipefail
[[ "${FNMUSIC_LIFECYCLE_TEST:-}" == isolated-container ]] || { echo 'requires isolated container' >&2; exit 1; }
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export TRIM_APPDEST=/var/apps/fnmusic-go/target
export TRIM_PKGVAR=/var/apps/fnmusic-go/var
export TRIM_PKGETC=/var/apps/fnmusic-go/etc
export TRIM_PKGTMP=/var/apps/fnmusic-go/tmp
export TRIM_TEMP_LOGFILE=/tmp/fnmusic-error
mkdir -p "$TRIM_APPDEST/bin/linux-arm64" "$TRIM_PKGVAR" "$TRIM_PKGETC" /tmp/mockbin /var/lib/fnmusic-go/netease-sessions
# The platform can expose var/etc through symlinks to volume directories.
mv "$TRIM_PKGVAR" /tmp/fnmusic-volume-var
ln -s /tmp/fnmusic-volume-var "$TRIM_PKGVAR"
export PATH="/tmp/mockbin:$PATH"
cat > /tmp/mockbin/systemctl <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> /tmp/systemctl.log
case "$1" in
 is-active) [[ -f /tmp/fnmusic-active ]] ;;
 start) if [[ -f /tmp/fail-start ]]; then echo 'bad unit file setting' >&2; exit 1; fi; touch /tmp/fnmusic-active ;;
 stop) rm -f /tmp/fnmusic-active ;;
 daemon-reload) [[ ! -e /tmp/fail-reload ]] ;;
 disable) exit 0 ;;
 show) printf 'LoadState=bad-setting\nActiveState=inactive\nResult=exit-code\nExecMainStatus=1\n' ;;
 *) echo "unexpected systemctl $*" >&2; exit 1 ;;
esac
MOCK
cat > "$TRIM_APPDEST/bin/linux-arm64/fnmusic-go" <<'MOCK'
#!/usr/bin/env bash
case "$1" in
 health) [[ -f /tmp/fnmusic-active && ! -f /tmp/fail-health ]] ;;
 restore) rm -f "${3}/ownership.json" ;;
 *) exit 1 ;;
esac
MOCK
chmod +x /tmp/mockbin/systemctl "$TRIM_APPDEST/bin/linux-arm64/fnmusic-go"
# No real account credentials are used.
printf '{"test":"legacy"}\n' > /var/lib/fnmusic-go/accounts.json
printf '{"test":"session"}\n' > /var/lib/fnmusic-go/netease-sessions/demo.json
printf 'FNMUSIC_GO_NETEASE_URL=\n' > /var/lib/fnmusic-go/provider.env
bash "$ROOT/packaging/fpk/cmd/install_callback"
command -v systemd-analyze >/dev/null || { echo 'systemd-analyze is required for lifecycle validation' >&2; exit 1; }
verify_unit(){
  local diagnostics
  diagnostics="$(systemd-analyze verify /etc/systemd/system/fnmusic-go.service 2>&1)" || { printf '%s\n' "$diagnostics"; return 1; }
  [[ -z "$diagnostics" ]] || { printf '%s\n' "$diagnostics"; return 1; }
}
verify_unit
cmp /var/lib/fnmusic-go/accounts.json "$TRIM_PKGVAR/accounts.json"
cmp /var/lib/fnmusic-go/provider.env "$TRIM_PKGETC/provider.env"
[[ "$(stat -c %a "$TRIM_PKGVAR/netease-sessions/demo.json")" == 600 ]]
! grep -Eq '^(start|enable)' /tmp/systemctl.log
! grep -q '\[Install\]' /etc/systemd/system/fnmusic-go.service
# Repeating callbacks is safe and must not reimport modified/deleted credentials.
printf '{"test":"new"}\n' > "$TRIM_PKGVAR/accounts.json"
bash "$ROOT/packaging/fpk/cmd/install_callback"
grep -q new "$TRIM_PKGVAR/accounts.json"
"$ROOT/dist/audit-tests/trim-music" /var/run/trim_music.socket & official=$!
trap 'kill "$official" 2>/dev/null || true' EXIT
for i in {1..30}; do [[ ! -S /var/run/trim_music.socket ]] || break; sleep .1; done
bash "$ROOT/packaging/fpk/cmd/main" start
bash "$ROOT/packaging/fpk/cmd/main" status
bash "$ROOT/packaging/fpk/cmd/main" stop
if bash "$ROOT/packaging/fpk/cmd/main" status; then exit 1; else [[ $? == 3 ]]; fi
# A systemd start rejection must expose the real reason, not just a line number.
touch /tmp/fail-start
if bash "$ROOT/packaging/fpk/cmd/main" start; then exit 1; fi
grep -q 'bad unit file setting' "$TRIM_TEMP_LOGFILE"
grep -q 'LoadState=bad-setting' "$TRIM_TEMP_LOGFILE"
rm /tmp/fail-start
# Readiness failure must stop the service and expose a user-readable error.
touch /tmp/fail-health
if bash "$ROOT/packaging/fpk/cmd/main" start; then exit 1; fi
[[ ! -f /tmp/fnmusic-active && -s "$TRIM_TEMP_LOGFILE" ]]
rm /tmp/fail-health
# Conflicting migration targets are never overwritten.
rm "$TRIM_PKGVAR/.legacy-migrated"
if bash "$ROOT/packaging/fpk/cmd/install_callback"; then exit 1; fi
grep -q new "$TRIM_PKGVAR/accounts.json"
touch "$TRIM_PKGVAR/.legacy-migrated"
# Convert a legacy enabled service and its exact known drop-in during upgrade.
mkdir -p /etc/systemd/system/fnmusic-go.service.d
printf '[Service]\nEnvironmentFile=-/var/lib/fnmusic-go/provider.env\n' > /etc/systemd/system/fnmusic-go.service.d/20-fnmusic-provider.conf
bash "$ROOT/packaging/fpk/cmd/upgrade_init"
bash "$ROOT/packaging/fpk/cmd/upgrade_callback"
verify_unit
[[ ! -e /etc/systemd/system/fnmusic-go.service.d/20-fnmusic-provider.conf ]]
# Failed configuration reload restores the previous unit.
cp /etc/systemd/system/fnmusic-go.service /tmp/unit-backup
touch /tmp/fail-reload
if bash "$ROOT/packaging/fpk/cmd/upgrade_callback"; then exit 1; fi
cmp /tmp/unit-backup /etc/systemd/system/fnmusic-go.service
[[ -s "$TRIM_TEMP_LOGFILE" ]]
rm /tmp/fail-reload
# Foreign unit is refused before any stop/disable or removal.
cp /etc/systemd/system/fnmusic-go.service /tmp/owned-unit
mkdir -p /tmp/foreign
printf 'WorkingDirectory=/tmp/foreign\n' > /etc/systemd/system/fnmusic-go.service
cp /tmp/systemctl.log /tmp/log-before
if bash "$ROOT/packaging/fpk/cmd/uninstall_init"; then exit 1; fi
cmp /tmp/log-before /tmp/systemctl.log
mv /tmp/owned-unit /etc/systemd/system/fnmusic-go.service
bash "$ROOT/packaging/fpk/cmd/uninstall_init"
[[ ! -e /etc/systemd/system/fnmusic-go.service ]]
[[ -f "$TRIM_PKGVAR/accounts.json" && -f "$TRIM_PKGETC/provider.env" ]]
echo 'PASS: lifecycle, migration, repeat install, app-center control, upgrade, rollback, ownership guard, uninstall'
