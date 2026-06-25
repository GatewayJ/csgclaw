#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

approval_code=""
instance_code=""
open_id=""
yes="false"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --approval-code|--definition-code) approval_code="${2:?}"; shift 2 ;;
    --instance-code) instance_code="${2:?}"; shift 2 ;;
    --open-id) open_id="${2:?}"; shift 2 ;;
    --yes) yes="true"; shift ;;
    -h|--help)
      printf '%s\n' "Usage: $0 --approval-code <approval_code> --instance-code <instance_code> [--open-id ou_xxx] --yes"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [ -z "$approval_code" ] || [ -z "$instance_code" ]; then
  printf '{"status":"invalid-arguments","hint":"Missing --approval-code or --instance-code."}\n'
  exit 2
fi

if [ "$yes" != "true" ]; then
  printf '{"status":"confirmation-required","hint":"Ask the user to confirm the exact approval instance to recall, then rerun with --yes."}\n'
  exit 2
fi

if [ -z "$open_id" ]; then
  set +e
  auth_output="$(approval_bootstrap user "$(approval_user_oauth_scopes)" 2>&1)"
  auth_rc=$?
  set -e
  if [ "$auth_rc" -ne 0 ]; then
    printf '%s\n' "$auth_output"
    exit "$auth_rc"
  fi

  auth_status="$(approval_run_lark auth status --json 2>/dev/null || true)"
  open_id="$(approval_json_get_string openId "$auth_status")"
  if [ -z "$open_id" ]; then
    open_id="$(approval_json_get_string open_id "$auth_status")"
  fi
fi

if [ -z "$open_id" ]; then
  printf '{"status":"missing-user-open-id","hint":"User OAuth is required, or pass the current user open_id with --open-id."}\n'
  exit 2
fi

case "$open_id" in
  ou_*) ;;
  *)
    printf '{"status":"invalid-open-id","hint":"Pass the current user open_id in ou_... format. Do not pass user_id, union_id, or an arbitrary initiator ID."}\n'
    exit 2
    ;;
esac

bound_output="$(approval_bootstrap bot 2>&1)" || {
  printf '%s\n' "$bound_output"
  exit 1
}

data='{"approval_code":"'"$(approval_json_escape "$approval_code")"'","instance_code":"'"$(approval_json_escape "$instance_code")"'","user_id":"'"$(approval_json_escape "$open_id")"'"}'
params='{"user_id_type":"open_id"}'

set +e
output="$(approval_run_lark api POST /open-apis/approval/v4/instances/cancel \
  --as bot \
  --params "$params" \
  --data "$data" \
  --format json 2>&1)"
rc=$?
set -e

printf '%s\n' "$output"
if [ "$rc" -ne 0 ]; then
  case "$output" in
    *"99991672"*|*"99991679"*|*"missing_scopes"*|*"permission_violations"*|*"app_scope_not_applied"*|*"authorization"*)
      approval_show_permission_link
      ;;
  esac
fi
exit "$rc"
