#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lark_cli_common.sh
. "$SCRIPT_DIR/lark_cli_common.sh"

lark_cli_init_context

lark_cli_require_runner || exit 1

runner="$(lark_cli_runner_kind)"
runner_command="$(lark_cli_runner_display)"
version="$(lark_cli_exec --version 2>/dev/null | head -n 1 || true)"

if ! lark_cli_exec config show >/dev/null 2>&1; then
  lark_cli_json_object \
    status not-configured \
    reason config_missing \
    config_dir "$config_dir" \
    runtime "$runtime" \
    version "$version" \
    runner "$runner" \
    runner_command "$runner_command" \
    next bind
  exit 1
fi

if ! lark_cli_exec doctor --offline >&2; then
  lark_cli_json_object \
    status check-failed \
    reason doctor_failed \
    config_dir "$config_dir" \
    runtime "$runtime" \
    version "$version" \
    runner "$runner" \
    runner_command "$runner_command" \
    hint "Run bash scripts/lark_cli_ready.sh or inspect lark-cli doctor output"
  exit 1
fi

auth_output="$(lark_cli_exec auth status --json 2>/dev/null || true)"
if [ -z "$auth_output" ]; then
  auth_output="$(lark_cli_exec auth status 2>/dev/null || true)"
fi

if printf '%s' "$auth_output" | tr '\n' ' ' | grep -Eq '"user"[[:space:]]*:[[:space:]]*\{[^}]*"status"[[:space:]]*:[[:space:]]*"ready"'; then
  lark_cli_json_object \
    status user-ready \
    config_dir "$config_dir" \
    runtime "$runtime" \
    version "$version" \
    runner "$runner" \
    runner_command "$runner_command" \
    next api
else
  lark_cli_json_object \
    status bot-ready \
    config_dir "$config_dir" \
    runtime "$runtime" \
    version "$version" \
    runner "$runner" \
    runner_command "$runner_command" \
    next auth-start
fi
