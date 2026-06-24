#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lark_cli_common.sh
. "$SCRIPT_DIR/lark_cli_common.sh"

lark_cli_init_context

if ! command -v lark-cli >/dev/null 2>&1; then
  if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1 || ! command -v npx >/dev/null 2>&1; then
    lark_cli_json_object \
      status not-installed \
      reason node_or_npm_missing \
      config_dir "$config_dir" \
      runtime "$runtime" \
      hint "Install Node.js/npm/npx or provide lark-cli on PATH"
    exit 1
  fi

  printf '%s\n' "lark-cli is not on PATH; warming npx fallback with npx -y $LARK_CLI_NPX_PACKAGE --version" >&2
  if ! npx -y "$LARK_CLI_NPX_PACKAGE" --version >&2; then
    lark_cli_json_object \
      status not-installed \
      reason npx_runner_failed \
      config_dir "$config_dir" \
      runtime "$runtime" \
      hint "Check npm registry access or install lark-cli on PATH, then rerun bootstrap"
    exit 1
  fi
  hash -r 2>/dev/null || true
fi

lark_cli_require_runner || exit 1

runner="$(lark_cli_runner_kind)"
runner_command="$(lark_cli_runner_display)"
version="$(lark_cli_exec --version 2>/dev/null | head -n 1 || true)"

doctor_output="$(lark_cli_exec doctor --offline 2>&1)" || {
  case "$doctor_output" in
    *"not configured"*|*"config_file"*)
      lark_cli_json_object \
        status not-configured \
        reason config_missing \
        config_dir "$config_dir" \
        runtime "$runtime" \
        version "$version" \
        runner "$runner" \
        runner_command "$runner_command" \
        binary_status ready \
        next bind
      exit 0
      ;;
  esac

  printf '%s\n' "$doctor_output" >&2
  lark_cli_json_object \
    status check-failed \
    reason doctor_failed \
    config_dir "$config_dir" \
    runtime "$runtime" \
    version "$version" \
    runner "$runner" \
    runner_command "$runner_command" \
    hint "Run lark-cli doctor --offline for diagnostics"
  exit 1
}
printf '%s\n' "$doctor_output" >&2

doctor="$(bash "$SCRIPT_DIR/lark_cli_doctor.sh" 2>/dev/null || true)"
doctor_status="$(lark_cli_json_get_string status "$doctor")"

case "$doctor_status" in
  user-ready|bot-ready|ready)
    printf '%s\n' "$doctor"
    exit 0
    ;;
esac

if [ -n "$doctor" ]; then
  printf '%s\n' "$doctor"
  exit 0
fi

lark_cli_json_object \
  status ready \
  config_dir "$config_dir" \
  runtime "$runtime" \
  version "$version" \
  runner "$runner" \
  runner_command "$runner_command" \
  doctor_status ok \
  next doctor
