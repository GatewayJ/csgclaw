#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

usage() {
  printf '%s\n' "Usage: $0 --mode bot|user [--scope <additional-scope-list>]"
  printf '%s\n' "Note: user mode requests the grouped approval OAuth bundle; pass any additional scopes as one space- or comma-separated value."
  printf '%s\n' "Examples:"
  printf '%s\n' "  $0 --mode bot"
  printf '%s\n' "  $0 --mode user"
}

mode="bot"
scopes="$(approval_user_oauth_scopes)"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --mode)
      if [ "$#" -lt 2 ]; then
        usage
        exit 2
      fi
      mode="$2"
      shift 2
      ;;
    --scope)
      if [ "$#" -lt 2 ]; then
        usage
        exit 2
      fi
      if [ -z "$scopes" ]; then
        scopes="$2"
      else
        scopes="$scopes $2"
      fi
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown argument: %s\n' "$1" >&2
      usage
      exit 2
      ;;
  esac
done

if [ "$mode" != "bot" ] && [ "$mode" != "user" ]; then
  printf '{"status":"invalid-mode","reason":"mode-must-be-bot-or-user","mode":"%s"}\n' "$(approval_json_escape "$mode")"
  exit 2
fi

if [ "$mode" = "user" ] && [ -z "$scopes" ]; then
  scopes="$(approval_user_oauth_scopes)"
fi

set +e
output="$(approval_bootstrap "$mode" "$scopes" 2>&1)"
rc=$?
set -e

if [ -n "$output" ]; then
  printf '%s\n' "$output"
fi

exit "$rc"
