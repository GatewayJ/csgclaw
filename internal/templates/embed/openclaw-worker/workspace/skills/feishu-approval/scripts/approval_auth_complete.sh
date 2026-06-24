#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

timeout_seconds="${APPROVAL_AUTH_COMPLETE_TIMEOUT:-30}"

complete="$(approval_lark_script lark_cli_auth_complete.sh)"
if [ ! -f "$complete" ]; then
  printf '{"status":"not-installed","reason":"lark_cli_auth_complete_missing","hint":"OpenClaw worker must include skills/lark-cli/scripts/lark_cli_auth_complete.sh"}\n'
  exit 1
fi

set +e
if command -v timeout >/dev/null 2>&1; then
  output="$(timeout "$timeout_seconds" bash "$complete" 2>/dev/null)"
  rc=$?
else
  output="$(bash "$complete" 2>/dev/null)"
  rc=$?
fi
set -e

if [ "$rc" -eq 124 ]; then
  printf '{"status":"oauth-pending","reason":"auth_complete_timeout","hint":"Ask the user to finish browser authorization, then rerun approval_auth_complete.sh. If the link expired, rerun the original approval command to generate a fresh verification_url."}\n'
  exit 2
fi

printf '%s\n' "$output"
exit "$rc"
