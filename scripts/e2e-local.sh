#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
ORIG_PID="";PROXY_PID=""
cleanup(){
  if [[ -n "$PROXY_PID" ]]; then kill -TERM "$PROXY_PID" 2>/dev/null || true; wait "$PROXY_PID" 2>/dev/null || true; fi
  if [[ -n "$ORIG_PID" ]]; then kill -TERM "$ORIG_PID" 2>/dev/null || true; wait "$ORIG_PID" 2>/dev/null || true; fi
  rm -rf "$TMP"
}
trap cleanup EXIT
cd "$ROOT"
CGO_ENABLED=0 go build -o "$TMP/trim-music" ./tests/fakeofficial
CGO_ENABLED=0 go build -o "$TMP/fnmusic-go" ./cmd/fnmusic-go
ORIG="$TMP/official.sock";UP="$TMP/original_upstream.sock";ADMIN="$TMP/admin.sock"
"$TMP/trim-music" "$ORIG" >"$TMP/orig.log" 2>&1 & ORIG_PID=$!
for i in $(seq 1 30); do [[ -S "$ORIG" ]] && break; sleep .1; done
"$TMP/fnmusic-go" serve --target "$ORIG" --upstream "$UP" --state-dir "$TMP/state" --official-exe trim-music --admin-socket "$ADMIN" --gateway-uid "$(id -u)" --account-state "$TMP/accounts.json" >"$TMP/proxy.log" 2>&1 & PROXY_PID=$!
for i in $(seq 1 60); do [[ -S "$UP" && -S "$ADMIN" ]] && break; sleep .1; done
[[ -S "$UP" && -S "$ADMIN" ]] || { cat "$TMP/proxy.log"; exit 1; }
HEALTH="$(curl -fsS --unix-socket "$ORIG" http://local/_ext/healthz)"
echo "$HEALTH" | grep -q '"ok":true'
"$TMP/fnmusic-go" health --admin-socket "$ADMIN"
RANGE="$(curl -fsS -H 'Range: bytes=0-3' --unix-socket "$ORIG" http://local/music/api/v1/track/stream)"
[[ "$RANGE" == 'demo' ]] || { echo "bad range response: $RANGE";exit 1; }
HTTP="$(curl -s -o /dev/null -w '%{http_code}' --unix-socket "$ADMIN" http://local/app/fnmusic-go/api/status)"
[[ "$HTTP" == '403' ]] || { echo "missing admin denial: $HTTP";exit 1; }
HTTP="$(curl -s -o /dev/null -w '%{http_code}' -H 'X-Trim-Isadmin: true' --unix-socket "$ADMIN" http://local/app/fnmusic-go/api/status)"
[[ "$HTTP" == '200' ]] || { echo "admin status: $HTTP";exit 1; }
kill -TERM "$PROXY_PID";wait "$PROXY_PID";PROXY_PID=""
[[ ! -e "$UP" ]] || { echo 'upstream not cleaned';exit 1; }
[[ -S "$ORIG" ]] || { echo 'official socket not restored';exit 1; }
[[ ! -e "$TMP/state/ownership.json" ]] || { echo 'journal not cleared';exit 1; }
curl -fsS --unix-socket "$ORIG" http://local/music/api/v1/search/track | grep -q '"code":0'
# Test unclean process loss: journal and Socket remain for explicit verified restore.
"$TMP/fnmusic-go" serve --target "$ORIG" --upstream "$UP" --state-dir "$TMP/state" --official-exe trim-music --admin-socket "$ADMIN" --gateway-uid "$(id -u)" --account-state "$TMP/accounts.json" >"$TMP/proxy2.log" 2>&1 & PROXY_PID=$!
for i in $(seq 1 60); do [[ -S "$UP" && -f "$TMP/state/ownership.json" ]] && break; sleep .1; done
[[ -f "$TMP/state/ownership.json" ]] || { cat "$TMP/proxy2.log"; exit 1; }
kill -KILL "$PROXY_PID";wait "$PROXY_PID" 2>/dev/null || true;PROXY_PID=""
[[ -f "$TMP/state/ownership.json" ]] || { echo 'missing crash journal';exit 1; }
"$TMP/fnmusic-go" restore --target "$ORIG" --upstream "$UP" --state-dir "$TMP/state" --official-exe trim-music
[[ ! -e "$UP" && -S "$ORIG" ]] || { echo 'crash recovery failed';exit 1; }
# Crashed management socket is stale; the new service must safely reclaim it.
"$TMP/fnmusic-go" serve --target "$ORIG" --upstream "$UP" --state-dir "$TMP/state" --official-exe trim-music --admin-socket "$ADMIN" --gateway-uid "$(id -u)" --account-state "$TMP/accounts.json" >"$TMP/proxy3.log" 2>&1 & PROXY_PID=$!
for i in $(seq 1 60); do [[ -f "$TMP/state/ownership.json" ]] && break; sleep .1; done
[[ -f "$TMP/state/ownership.json" ]] || { cat "$TMP/proxy3.log"; exit 1; }
kill -TERM "$PROXY_PID";wait "$PROXY_PID";PROXY_PID=""
[[ ! -e "$UP" && -S "$ORIG" ]] || { echo 'recovered service failed final restore';exit 1; }
"$TMP/fnmusic-go" restore-if-needed --state-dir "$TMP/state"
echo 'E2E PASS: takeover, Range, admin guard, graceful recovery, SIGKILL recovery, restart' 
