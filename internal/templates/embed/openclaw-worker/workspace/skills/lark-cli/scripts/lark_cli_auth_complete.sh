#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lark_cli_common.sh
. "$SCRIPT_DIR/lark_cli_common.sh"

usage() {
  printf '%s\n' "Usage: $0 [--device-code <device_code>]" >&2
}

device_code=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --device-code)
      if [ "$#" -lt 2 ]; then usage; exit 2; fi
      device_code="$2"
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

lark_cli_init_context

lark_cli_require_runner || exit 1
runner="$(lark_cli_runner_kind)"
runner_command="$(lark_cli_runner_display)"

if [ -z "$device_code" ]; then
  pending_file="$config_dir/oauth-pending.json"
  if [ -f "$pending_file" ]; then
    pending_json="$(cat "$pending_file")"
    device_code="$(lark_cli_json_get_string device_code "$pending_json")"
  fi

  if [ -z "$device_code" ]; then
    lark_cli_json_object \
      status oauth-pending \
      reason device_code_missing \
      runtime "$runtime" \
      config_dir "$config_dir" \
      hint "Run bash scripts/lark_cli_auth_start.sh --no-wait --json again, then ask the user to open verification_url"
    exit 2
  fi
fi

if LARKSUITE_CLI_CONFIG_DIR="$config_dir" lark_cli_exec auth login --device-code "$device_code" >&2; then
  rm -f "$config_dir/oauth-pending.json" 2>/dev/null || true
  lark_cli_json_object \
    status user-ready \
    runtime "$runtime" \
    config_dir "$config_dir" \
    runner "$runner" \
    runner_command "$runner_command" \
    next retry-original-command
else
  lark_cli_json_object \
    status oauth-pending \
    reason auth_complete_failed \
    runtime "$runtime" \
    config_dir "$config_dir" \
    runner "$runner" \
    runner_command "$runner_command" \
    hint "Ask the user to finish browser authorization before retrying, or restart auth when expired"
  exit 1
fi
