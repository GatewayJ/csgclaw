#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

definition_code=""
locale="zh-CN"
page_size="20"
page_limit="5"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --definition-code) definition_code="${2:?}"; shift 2 ;;
    --locale) locale="${2:?}"; shift 2 ;;
    --page-size) page_size="${2:?}"; shift 2 ;;
    --page-limit) page_limit="${2:?}"; shift 2 ;;
    -h|--help)
      printf '%s\n' "Usage: $0 [--definition-code <code>] [--locale zh-CN] [--page-size 20] [--page-limit 5]"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

cmd=(approval instances initiated --as user --locale "$locale" --user-id-type open_id --page-size "$page_size" --page-all --page-limit "$page_limit" --format json)
if [ -n "$definition_code" ]; then
  cmd+=(--definition-code "$definition_code")
fi

set +e
output="$(approval_run_user_scoped "$(approval_user_oauth_scopes)" "${cmd[@]}")"
run_rc=$?
set -e

if [ "$run_rc" -ne 0 ]; then
  printf '%s\n' "$output"
  exit "$run_rc"
fi

printf '%s\n' "$output" | python3 "$SCRIPT_DIR/format_initiated.py"
