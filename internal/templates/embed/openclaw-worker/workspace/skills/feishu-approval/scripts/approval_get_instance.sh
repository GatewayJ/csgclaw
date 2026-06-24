#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

instance_code=""
locale="zh-CN"
identity="user"
user_id_type="open_id"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --instance-code) instance_code="${2:?}"; shift 2 ;;
    --locale) locale="${2:?}"; shift 2 ;;
    --as) identity="${2:?}"; shift 2 ;;
    --user-id-type) user_id_type="${2:?}"; shift 2 ;;
    -h|--help)
      printf '%s\n' "Usage: $0 --instance-code <instance_code> [--locale zh-CN] [--as user|bot] [--user-id-type open_id|user_id|union_id]"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [ -z "$instance_code" ]; then
  printf 'Missing --instance-code\n' >&2
  exit 2
fi

case "$identity" in
  user|bot) ;;
  *) printf '{"status":"invalid-identity","hint":"Use --as user or --as bot."}\n'; exit 2 ;;
esac

case "$user_id_type" in
  open_id|user_id|union_id) ;;
  *) printf '{"status":"invalid-user-id-type","hint":"Use open_id, user_id, or union_id."}\n'; exit 2 ;;
esac

if [ "$identity" = "user" ]; then
  approval_run_user_scoped "$(approval_user_oauth_scopes)" approval instances get \
    --as user \
    --instance-code "$instance_code" \
    --locale "$locale" \
    --user-id-type "$user_id_type" \
    --format json
else
  bound_output="$(approval_bootstrap bot 2>&1)" || {
    printf '%s\n' "$bound_output"
    exit 1
  }

  approval_run_lark api GET "/open-apis/approval/v4/instances/$instance_code?locale=$locale&user_id_type=$user_id_type" \
    --as bot \
    --format json
fi
