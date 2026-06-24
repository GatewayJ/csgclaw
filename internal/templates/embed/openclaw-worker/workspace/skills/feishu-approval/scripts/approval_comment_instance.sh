#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

instance_code=""
comment=""
open_id=""
yes="false"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --instance-code) instance_code="${2:?}"; shift 2 ;;
    --comment) comment="${2:?}"; shift 2 ;;
    --open-id) open_id="${2:?}"; shift 2 ;;
    --yes) yes="true"; shift ;;
    -h|--help)
      printf '%s\n' "Usage: $0 --instance-code <instance_code> --comment <text> [--open-id ou_xxx] --yes"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [ -z "$instance_code" ] || [ -z "$comment" ]; then
  printf '{"status":"invalid-arguments","hint":"Missing --instance-code or --comment."}\n'
  exit 2
fi

if [ "$yes" != "true" ]; then
  printf '{"status":"confirmation-required","hint":"Ask the user to confirm the exact approval instance and comment text, then rerun with --yes."}\n'
  exit 2
fi

if [ -z "$open_id" ]; then
  bound_output="$(approval_bootstrap user 2>&1)" || {
    printf '%s\n' "$bound_output"
    exit 1
  }

  auth_status="$(approval_run_lark auth status --json 2>/dev/null || true)"
  open_id="$(approval_json_get_string openId "$auth_status")"

  if [ -z "$open_id" ]; then
    printf '{"status":"missing-commenter-open-id","hint":"Pass the current comment operator open_id with --open-id, or complete user OAuth so the helper can detect the current user open_id."}\n'
    exit 2
  fi

  bound_output="$(approval_bootstrap bot 2>&1)" || {
    printf '%s\n' "$bound_output"
    exit 1
  }
else
  bound_output="$(approval_bootstrap bot 2>&1)" || {
    printf '%s\n' "$bound_output"
    exit 1
  }
fi

content='{"text":"'"$(approval_json_escape "$comment")"'"}'
data='{"content":"'"$(approval_json_escape "$content")"'"}'
params='{"user_id_type":"open_id","user_id":"'"$(approval_json_escape "$open_id")"'"}'

set +e
output="$(approval_run_lark api POST "/open-apis/approval/v4/instances/$instance_code/comments" \
  --as bot \
  --params "$params" \
  --data "$data" \
  --format json 2>&1)"
rc=$?
set -e

printf '%s\n' "$output"

if [ "$rc" -ne 0 ]; then
  case "$output" in
    *"contact:user.employee_id:readonly"*)
      printf '%s\n' '{"status":"comment-user-id-type-error","hint":"Do not use user_id_type=user_id for approval comments unless the app has contact:user.employee_id:readonly. Retry with open_id."}'
      ;;
    *"99991672"*|*"99991679"*|*"missing_scopes"*|*"permission_violations"*|*"app_scope_not_applied"*|*"approval:instance.comment"*|*"authorization"*)
      approval_run_script "$SCRIPT_DIR/approval_permission_link.sh" --purpose comment || true
      ;;
  esac
  exit "$rc"
fi
