#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lark_cli_common.sh
. "$SCRIPT_DIR/lark_cli_common.sh"

write_lark_channel_config() {
  local file="$1"
  local app_id="$2"

  umask 077
  {
    printf '{\n'
    printf '  "accounts": {\n'
    printf '    "app": {\n'
    printf '      "id": "%s",\n' "$(lark_cli_json_escape "$app_id")"
    printf '      "secret": "${PICOCLAW_CHANNELS_FEISHU_APP_SECRET}",\n'
    printf '      "tenant": "feishu"\n'
    printf '    }\n'
    printf '  }\n'
    printf '}\n'
  } >"$file"
  chmod 600 "$file" 2>/dev/null || true
}

lark_cli_init_context
identity="${LARK_CLI_IDENTITY:-user-default}"

lark_cli_require_runner || exit 1
runner="$(lark_cli_runner_kind)"
runner_command="$(lark_cli_runner_display)"
bind_log="$(mktemp)"
trap 'rm -f "$bind_log"' EXIT

if [ -n "${PICOCLAW_CHANNELS_FEISHU_APP_ID:-}" ] || [ -n "${PICOCLAW_CHANNELS_FEISHU_APP_SECRET:-}" ]; then
  if [ -z "${PICOCLAW_CHANNELS_FEISHU_APP_ID:-}" ] || [ -z "${PICOCLAW_CHANNELS_FEISHU_APP_SECRET:-}" ]; then
    lark_cli_json_object \
      status not-configured \
      reason picoclaw_feishu_app_credentials_incomplete \
      runtime picoclaw \
      config_dir "$config_dir" \
      hint "Both PICOCLAW_CHANNELS_FEISHU_APP_ID and PICOCLAW_CHANNELS_FEISHU_APP_SECRET are required; rebind the Feishu participant or restart the agent"
    exit 1
  fi
fi

if [ -n "${PICOCLAW_CHANNELS_FEISHU_APP_ID:-}" ] && [ -n "${PICOCLAW_CHANNELS_FEISHU_APP_SECRET:-}" ]; then
  export LARK_CHANNEL_CONFIG="$config_root/csgclaw-lark-channel.json"
  write_lark_channel_config "$LARK_CHANNEL_CONFIG" "$PICOCLAW_CHANNELS_FEISHU_APP_ID"

  if LARKSUITE_CLI_CONFIG_DIR="$config_root" lark_cli_exec config bind --source lark-channel --identity "$identity" --force >"$bind_log" 2>&1; then
    lark_cli_json_object \
      status bot-ready \
      runtime picoclaw \
      config_dir "$config_dir" \
      identity "$identity" \
      runner "$runner" \
      runner_command "$runner_command" \
      source lark-channel \
      next auth-start
    exit 0
  fi
  cat "$bind_log" >&2

  lark_cli_json_object \
    status not-configured \
    reason bind_failed \
    runtime picoclaw \
    config_dir "$config_dir" \
    runner "$runner" \
    runner_command "$runner_command" \
    source lark-channel \
    hint "Inspect lark-cli config bind diagnostics and verify the Feishu participant app"
  exit 1
fi

openclaw_dir="$(lark_cli_openclaw_dir)"
if [ -n "$openclaw_dir" ]; then
  native_openclaw_home="$(lark_cli_native_openclaw_home "$openclaw_dir")"

  bind_openclaw_home() {
    local openclaw_home="$1"
    OPENCLAW_HOME="$openclaw_home" LARKSUITE_CLI_CONFIG_DIR="$config_root" \
      lark_cli_exec config bind --source openclaw --identity "$identity" --force >"$bind_log" 2>&1
  }

  if bind_openclaw_home "$openclaw_dir" || bind_openclaw_home "$native_openclaw_home"; then
    lark_cli_json_object \
      status bot-ready \
      runtime openclaw \
      config_dir "$config_dir" \
      openclaw_home "$native_openclaw_home" \
      openclaw_dir "$openclaw_dir" \
      identity "$identity" \
      runner "$runner" \
      runner_command "$runner_command" \
      source openclaw \
      next auth-start
    exit 0
  fi
  cat "$bind_log" >&2

  lark_cli_json_object \
    status not-configured \
    reason openclaw_bind_failed \
    runtime openclaw \
    config_dir "$config_dir" \
    openclaw_home "$native_openclaw_home" \
    openclaw_dir "$openclaw_dir" \
    attempted_openclaw_home "$openclaw_dir $native_openclaw_home" \
    runner "$runner" \
    runner_command "$runner_command" \
    source openclaw \
    hint "OpenClaw Feishu app binding should be reused automatically. Do not ask the user for App ID or App Secret. Ensure this OpenClaw agent has a Feishu participant binding and openclaw.json contains channels.feishu.accounts, then recreate or restart the agent before retrying."
  exit 1
fi

lark_cli_json_object \
  status not-configured \
  reason reusable_app_credentials_missing \
  runtime "$runtime" \
  config_dir "$config_dir" \
  hint "No reusable CSGClaw/OpenClaw Feishu app binding was found. In managed OpenClaw workers, do not ask for App ID or App Secret; bind a Feishu participant to this agent in CSGClaw, then recreate or restart the agent." \
  next bind-feishu-participant
