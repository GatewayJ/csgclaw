#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lark_cli_common.sh
. "$SCRIPT_DIR/lark_cli_common.sh"

lark_cli_init_context

bootstrap="$(bash "$SCRIPT_DIR/lark_cli_bootstrap.sh" 2>/dev/null || true)"
bootstrap_status="$(lark_cli_json_get_string status "$bootstrap")"
bootstrap_reason="$(lark_cli_json_get_string reason "$bootstrap")"

if [ -z "$bootstrap_status" ]; then
  printf '%s\n' "$bootstrap"
  exit 1
fi

if [ "$bootstrap_status" = "not-installed" ] || [ "$bootstrap_status" = "check-failed" ]; then
  printf '%s\n' "$bootstrap"
  exit 1
fi

if [ "$bootstrap_status" = "not-configured" ] && [ "$bootstrap_reason" = "config_missing" ]; then
  bind="$(bash "$SCRIPT_DIR/lark_cli_bind_app.sh" 2>/dev/null || true)"
  bind_status="$(lark_cli_json_get_string status "$bind")"
  if [ "$bind_status" != "bot-ready" ] && [ "$bind_status" != "user-ready" ]; then
    printf '%s\n' "$bind"
    exit 1
  fi

  doctor="$(bash "$SCRIPT_DIR/lark_cli_doctor.sh" 2>/dev/null || true)"
  doctor_status="$(lark_cli_json_get_string status "$doctor")"
  if [ "$doctor_status" = "bot-ready" ] || [ "$doctor_status" = "user-ready" ] || [ "$doctor_status" = "ready" ]; then
    printf '%s\n' "$doctor"
    exit 0
  fi

  printf '%s\n' "$doctor"
  exit 1
fi

if [ "$bootstrap_status" = "ready" ] || [ "$bootstrap_status" = "bot-ready" ] || [ "$bootstrap_status" = "user-ready" ]; then
  printf '%s\n' "$bootstrap"
  exit 0
fi

printf '{"status":"ready-blocked","reason":"unexpected-bootstrap-state","bootstrap_status":"%s","config_dir":"%s","runtime":"%s"}\n' \
  "$(lark_cli_json_escape "$bootstrap_status")" \
  "$(lark_cli_json_escape "$config_dir")" \
  "$(lark_cli_json_escape "$runtime")"
exit 1
