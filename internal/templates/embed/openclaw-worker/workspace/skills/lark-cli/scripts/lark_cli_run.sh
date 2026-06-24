#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lark_cli_common.sh
. "$SCRIPT_DIR/lark_cli_common.sh"

usage() {
  printf '%s\n' "Usage: $0 <lark-cli args...>" >&2
  printf '%s\n' "Example: $0 api GET /open-apis/authen/v1/user_info --as user" >&2
}

if [ "$#" -eq 0 ]; then
  usage
  exit 2
fi

lark_cli_init_context
lark_cli_require_runner || exit 1

if [ "$1" = "api" ] && [ "$#" -ge 3 ]; then
  api_method="$2"
  api_path="$3"
  if [ "${ALLOW_APPROVAL_RAW:-}" != "1" ] && [ "${OPENCLAW_APPROVAL_ALLOW_RAW:-}" != "1" ]; then
    if [ "$api_method" = "POST" ] || [ "$api_method" = "PATCH" ] || [ "$api_method" = "PUT" ]; then
      case "$api_path" in
        /open-apis/approval/v4/instances/cancel|/open-apis/approval/v4/instances/*/cancel|/open-apis/approval/v4/instances/*/comments|/open-apis/approval/v4/instances/*/comments/*|/open-apis/approval/v4/instances/*/tasks/*)
          lark_cli_json_object \
            status blocked \
            reason approval_route_guard \
            hint "Direct approval flow API writes are blocked for this path. In OpenClaw, use the feishu-approval scripts (for example approval_cancel_instance.sh / approval_task_decide.sh / approval_comment_instance.sh) instead of raw lark_cli_run calls."
          exit 2
          ;;
      esac
    fi
  fi
fi

lark_cli_exec "$@"
