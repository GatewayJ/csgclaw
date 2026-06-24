#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

locale="zh-CN"
page_size="100"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --locale) locale="${2:?}"; shift 2 ;;
    --page-size) page_size="${2:?}"; shift 2 ;;
    -h|--help)
      printf '%s\n' "Usage: $0 [--locale zh-CN] [--page-size 100]"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

case "$page_size" in
  ''|*[!0-9]*)
    printf 'Invalid --page-size: %s\n' "$page_size" >&2
    exit 2
    ;;
esac

params="$(printf '{"page_size":%s,"locale":"%s"}' "$page_size" "$(approval_json_escape "$locale")")"

approval_run_user_scoped "$(approval_user_oauth_scopes)" api GET /open-apis/approval/v4/approvals \
  --as user \
  --params "$params" \
  --page-all
