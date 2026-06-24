#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

approval_code=""
locale="zh-CN"
with_option="true"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --approval-code|--definition-code) approval_code="${2:?}"; shift 2 ;;
    --locale) locale="${2:?}"; shift 2 ;;
    --with-option) with_option="${2:?}"; shift 2 ;;
    -h|--help)
      printf '%s\n' "Usage: $0 --approval-code <approval_code> [--locale zh-CN] [--with-option true|false]"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [ -z "$approval_code" ]; then
  printf 'Missing --approval-code\n' >&2
  exit 2
fi

bound_output="$(approval_bootstrap bot 2>&1)" || {
  printf '%s\n' "$bound_output"
  exit 1
}

params='{"locale":"'"$(approval_json_escape "$locale")"'","user_id_type":"open_id","with_option":'"$with_option"'}'

set +e
output="$(approval_run_lark api GET "/open-apis/approval/v4/approvals/$approval_code" \
  --as bot \
  --params "$params" \
  --format json 2>&1)"
rc=$?
set -e

printf '%s\n' "$output"

if [ "$rc" -ne 0 ]; then
  case "$output" in
    *"99991672"*|*"99991679"*|*"missing_scopes"*|*"permission_violations"*|*"app_scope_not_applied"*|*"authorization"*)
      approval_run_script "$SCRIPT_DIR/approval_permission_link.sh" --purpose app || true
      ;;
  esac
  exit "$rc"
fi
