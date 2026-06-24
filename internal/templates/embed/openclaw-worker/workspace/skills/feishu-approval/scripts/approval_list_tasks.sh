#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

topic="1"
definition_code=""
locale="zh-CN"
page_size="20"
page_limit="5"

topic_code() {
  case "$1" in
    todo|pending|待办) printf '1' ;;
    done|finished|已办) printf '2' ;;
    initiated|started|已发起) printf '3' ;;
    unread-cc|unread_cc|unread|未读知会) printf '17' ;;
    read-cc|read_cc|read|已读知会) printf '18' ;;
    1|2|3|17|18) printf '%s' "$1" ;;
    *) return 1 ;;
  esac
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --topic) topic="$(topic_code "${2:?}")" || { printf 'Unsupported topic: %s\n' "$2" >&2; exit 2; }; shift 2 ;;
    --definition-code) definition_code="${2:?}"; shift 2 ;;
    --locale) locale="${2:?}"; shift 2 ;;
    --page-size) page_size="${2:?}"; shift 2 ;;
    --page-limit) page_limit="${2:?}"; shift 2 ;;
    -h|--help)
      printf '%s\n' "Usage: $0 [--topic todo|done|initiated|unread-cc|read-cc] [--definition-code <code>] [--locale zh-CN] [--page-size 20] [--page-limit 5]"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

params='{"topic":"'"$(approval_json_escape "$topic")"'","locale":"'"$(approval_json_escape "$locale")"'","user_id_type":"open_id","page_size":'"$page_size"
if [ -n "$definition_code" ]; then
  params="$params"',"definition_code":"'"$(approval_json_escape "$definition_code")"'"'
fi
params="$params"'}'

approval_run_user_scoped "$(approval_user_oauth_scopes)" approval tasks query \
  --as user \
  --params "$params" \
  --page-all \
  --page-limit "$page_limit" \
  --format json
