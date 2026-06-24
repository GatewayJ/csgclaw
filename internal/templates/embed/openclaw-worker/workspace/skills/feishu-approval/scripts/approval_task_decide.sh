#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

action=""
instance_code=""
task_id=""
comment=""
form=""
yes="false"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --action) action="${2:?}"; shift 2 ;;
    --instance-code) instance_code="${2:?}"; shift 2 ;;
    --task-id) task_id="${2:?}"; shift 2 ;;
    --comment) comment="${2:?}"; shift 2 ;;
    --form) form="${2:?}"; shift 2 ;;
    --yes) yes="true"; shift ;;
    -h|--help)
      printf '%s\n' "Usage: $0 --action approve|reject --instance-code <code> --task-id <task_id> [--comment text] [--form json-string] --yes"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

case "$action" in
  approve|pass) action="approve" ;;
  reject|refuse) action="reject" ;;
  *) printf 'Missing or unsupported --action; use approve or reject\n' >&2; exit 2 ;;
esac

if [ -z "$instance_code" ] || [ -z "$task_id" ]; then
  printf 'Missing --instance-code or --task-id\n' >&2
  exit 2
fi

if [ "$yes" != "true" ]; then
  printf '{"status":"confirmation-required","hint":"Ask the user to confirm the approval task, action, and comment, then rerun with --yes"}\n'
  exit 2
fi

data='{"instance_code":"'"$(approval_json_escape "$instance_code")"'","task_id":"'"$(approval_json_escape "$task_id")"'"'
if [ -n "$comment" ]; then
  data="$data"',"comment":"'"$(approval_json_escape "$comment")"'"'
fi
if [ -n "$form" ]; then
  data="$data"',"form":"'"$(approval_json_escape "$form")"'"'
fi
data="$data"'}'

approval_run_user_scoped "$(approval_user_oauth_scopes)" approval tasks "$action" \
  --as user \
  --data "$data" \
  --yes \
  --format json
