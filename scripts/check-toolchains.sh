#!/usr/bin/env bash
# Reject toolchains older than the October 2026 security fixes.
set -euo pipefail
python3 - "$(go env GOVERSION)" "$(node --version)" <<'CHECK'
import re
import sys

def version(value, prefix):
    match = re.fullmatch(re.escape(prefix) + r'(\d+)\.(\d+)\.(\d+)', value)
    return tuple(map(int, match.groups())) if match else (0, 0, 0)

go = version(sys.argv[1], 'go')
node = version(sys.argv[2], 'v')
errors = []
if not (go >= (1, 27, 3) or (1, 26, 10) <= go < (1, 27, 0)):
    errors.append('Go requires 1.27.3+ or 1.26.10+ (CVE-2026-111943)')
minimum = {22: (22, 23, 4), 24: (24, 22, 1), 26: (26, 12, 1)}
if node[0] not in minimum or node < minimum[node[0]]:
    errors.append('Node.js requires 22.23.4+, 24.22.1+, or 26.12.1+ in a supported release line (CVE-2026-111497)')
if errors:
    sys.exit('\n'.join(errors))
print(f'[toolchains] {sys.argv[1]}, {sys.argv[2]}: security version checks passed')
CHECK
