#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lark_cli_common.sh
. "$SCRIPT_DIR/lark_cli_common.sh"

usage() {
  printf '%s\n' "Usage: $0 [--no-wait] [--json] [--domain <domain>] [--scope <scope-list>]... [-- <extra lark-cli args>]" >&2
  printf '%s\n' "Note: lark-cli auth login --scope is a single string; repeated wrapper --scope values are merged before invoking lark-cli." >&2
}

lark_cli_init_context

auth_scope_signature() {
  local scopes="$1"
  if [ -z "$scopes" ]; then
    printf ''
    return 0
  fi
  auth_scope_list "$scopes" | sort -u | tr '\n' ' ' | sed 's/[[:space:]]*$//'
}

auth_scope_list() {
  local scopes="$1"
  printf '%s\n' "$scopes" | awk '{ gsub(/,/, " "); for (i = 1; i <= NF; i++) print $i }'
}

auth_scope_argument() {
  local scopes="$1"
  auth_scope_list "$scopes" | tr '\n' ' ' | sed 's/[[:space:]]*$//'
}

auth_generate_session_id() {
  local runtime_ctx="${1-}"
  local config_dir_ctx="${2-}"
  local domain_ctx="${3-}"
  local scope_signature="${4-}"
  local source="${runtime_ctx}|${config_dir_ctx}|${domain_ctx}|${scope_signature}"

  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s' "$source" | sha256sum | awk '{print $1}'
    return 0
  fi

  if command -v md5sum >/dev/null 2>&1; then
    printf '%s' "$source" | md5sum | awk '{print $1}'
    return 0
  fi

  printf '%s' "$source"
}

reuse_oauth_pending_if_valid() {
  local pending_file="$config_dir/oauth-pending.json"
  REUSE_VERIFICATION_URL=""
  REUSE_EXPIRES_IN=""
  REUSE_SESSION=""
  if [ ! -f "$pending_file" ]; then
    return 1
  fi

  local pending_output verification_url expires_in now created_at age pending_session pending_runtime pending_config_dir pending_domain pending_scope_signature
  pending_output="$(cat "$pending_file" 2>/dev/null || true)"
  verification_url="$(lark_cli_json_get_string verification_url "$pending_output")"
  if [ -z "$verification_url" ]; then
    rm -f "$pending_file" 2>/dev/null || true
    return 1
  fi

  pending_runtime="$(lark_cli_json_get_string runtime "$pending_output")"
  pending_config_dir="$(lark_cli_json_get_string config_dir "$pending_output")"
  pending_session="$(lark_cli_json_get_string session "$pending_output")"
  pending_scope_signature="$(lark_cli_json_get_string scope_signature "$pending_output")"
  pending_domain="$(lark_cli_json_get_string domain "$pending_output")"

  local current_scope_signature
  current_scope_signature="$(auth_scope_signature "$AUTH_SCOPES")"
  local current_session
  current_session="$(auth_generate_session_id "$runtime" "$config_dir" "${AUTH_DOMAIN}" "$current_scope_signature")"

  if [ -z "$pending_session" ] \
    || [ "$pending_session" != "$current_session" ] \
    || [ "$pending_runtime" != "$runtime" ] \
    || [ "$pending_config_dir" != "$config_dir" ]; then
    rm -f "$pending_file" 2>/dev/null || true
    return 1
  fi

  if [ -n "$pending_scope_signature" ] && [ "$pending_scope_signature" != "$current_scope_signature" ]; then
    rm -f "$pending_file" 2>/dev/null || true
    return 1
  fi

  if [ -n "$pending_domain" ] && [ -n "$AUTH_DOMAIN" ] && [ "$pending_domain" != "$AUTH_DOMAIN" ]; then
    rm -f "$pending_file" 2>/dev/null || true
    return 1
  fi

  expires_in="$(lark_cli_json_get_number expires_in "$pending_output")"
  if [ -z "$expires_in" ]; then
    expires_in="600"
  fi

  now="$(date +%s 2>/dev/null || true)"
  if [ -n "$now" ] && command -v date >/dev/null 2>&1; then
    created_at="$(date -r "$pending_file" +%s 2>/dev/null || echo 0)"
    age=$((now - created_at))
    if [ "$age" -lt 0 ] || [ "$age" -ge "$expires_in" ]; then
      return 1
    fi
  fi

  REUSE_VERIFICATION_URL="$verification_url"
  REUSE_EXPIRES_IN="$expires_in"
  REUSE_SESSION="$pending_session"
  REUSE_SCOPE_SIGNATURE="$pending_scope_signature"
  return 0
}

lark_cli_require_runner || exit 1
runner="$(lark_cli_runner_kind)"
runner_command="$(lark_cli_runner_display)"

cmd=(auth login --no-wait --json)
AUTH_DOMAIN=""
AUTH_SCOPES=""
AUTH_SCOPE_ARG=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --no-wait|--json)
      # The wrapper always enforces these lark-cli flags. Accept them so
      # agents can use an explicit, stable OAuth-start command shape.
      shift
      ;;
    --domain)
      if [ "$#" -lt 2 ]; then usage; exit 2; fi
      cmd+=(--domain "$2")
      AUTH_DOMAIN="$2"
      shift 2
      ;;
    --scope)
      if [ "$#" -lt 2 ]; then usage; exit 2; fi
      if [ -n "$AUTH_SCOPES" ]; then
        AUTH_SCOPES="$AUTH_SCOPES $2"
      else
        AUTH_SCOPES="$2"
      fi
      shift 2
      ;;
    --)
      shift
      while [ "$#" -gt 0 ]; do
        cmd+=("$1")
        shift
      done
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

AUTH_SCOPE_ARG="$(auth_scope_argument "$AUTH_SCOPES")"
if [ -n "$AUTH_SCOPES" ] && [ -z "$AUTH_SCOPE_ARG" ]; then
  printf 'Empty --scope value\n' >&2
  usage
  exit 2
fi
if [ -n "$AUTH_SCOPE_ARG" ]; then
  AUTH_SCOPES="$AUTH_SCOPE_ARG"
  cmd+=(--scope "$AUTH_SCOPE_ARG")
fi

if [ -z "${AUTH_DOMAIN}" ]; then
  AUTH_DOMAIN="feishu"
fi

if reuse_oauth_pending_if_valid; then
  expires_in="$REUSE_EXPIRES_IN"
  lark_cli_json_object \
    status oauth-pending \
    runtime "$runtime" \
    config_dir "$config_dir" \
    runner "$runner" \
    runner_command "$runner_command" \
    verification_url "$REUSE_VERIFICATION_URL" \
    expires_in "$expires_in" \
    session "$REUSE_SESSION" \
    state_file "$config_dir/oauth-pending.json" \
    scope_signature "$REUSE_SCOPE_SIGNATURE" \
    hint "Use this existing pending authorization link; do not start a new OAuth flow. Share only verification_url and wait for completion."
  exit 0
fi

err_file="$(mktemp)"
trap 'rm -f "$err_file"' EXIT

if output="$(LARKSUITE_CLI_CONFIG_DIR="$config_dir" lark_cli_exec "${cmd[@]}" 2>"$err_file")"; then
  verification_url="$(lark_cli_json_get_string verification_url "$output")"
  device_code="$(lark_cli_json_get_string device_code "$output")"
  expires_in="$(lark_cli_json_get_number expires_in "$output")"

  if [ -z "$verification_url" ] || [ -z "$device_code" ]; then
    unexpected_file="$config_dir/oauth-unexpected.json"
    umask 077
    printf '%s\n' "$output" >"$unexpected_file"
    chmod 600 "$unexpected_file" 2>/dev/null || true
    lark_cli_json_object \
      status oauth-start-failed \
      reason unexpected_auth_response \
      runtime "$runtime" \
      config_dir "$config_dir" \
      runner "$runner" \
      runner_command "$runner_command" \
      state_file "$unexpected_file" \
      hint "lark-cli did not return both verification_url and device_code; inspect state_file without sharing secrets"
    exit 1
  fi

  pending_file="$config_dir/oauth-pending.json"
  AUTH_SCOPE_SIGNATURE="$(auth_scope_signature "$AUTH_SCOPES")"
  if [ -z "$AUTH_DOMAIN" ]; then
    AUTH_DOMAIN="feishu"
  fi
  AUTH_SESSION="$(auth_generate_session_id "$runtime" "$config_dir" "$AUTH_DOMAIN" "$AUTH_SCOPE_SIGNATURE")"
  if ! [ -n "${expires_in}" ] || ! [[ "$expires_in" =~ ^[0-9]+$ ]]; then
    expires_in="600"
  fi
  created_at="$(date +%s 2>/dev/null || echo 0)"
  umask 077
  {
    printf '{\n'
    printf '  "runtime":"%s",\n' "$(lark_cli_json_escape "$runtime")"
    printf '  "config_dir":"%s",\n' "$(lark_cli_json_escape "$config_dir")"
    printf '  "domain":"%s",\n' "$(lark_cli_json_escape "$AUTH_DOMAIN")"
    printf '  "scopes":"%s",\n' "$(lark_cli_json_escape "$AUTH_SCOPES")"
    printf '  "scope_signature":"%s",\n' "$(lark_cli_json_escape "$AUTH_SCOPE_SIGNATURE")"
    printf '  "session":"%s",\n' "$(lark_cli_json_escape "$AUTH_SESSION")"
    printf '  "verification_url":"%s",\n' "$(lark_cli_json_escape "$verification_url")"
    printf '  "device_code":"%s",\n' "$(lark_cli_json_escape "$device_code")"
    printf '  "expires_in":%s,\n' "$(lark_cli_json_escape "$expires_in")"
    printf '  "created_at":%s\n' "$(lark_cli_json_escape "$created_at")"
    printf '}\n'
  } >"$pending_file"
  chmod 600 "$pending_file" 2>/dev/null || true

  REUSE_SESSION="$AUTH_SESSION"
  REUSE_SCOPE_SIGNATURE="$AUTH_SCOPE_SIGNATURE"
  lark_cli_json_object \
    status oauth-pending \
    runtime "$runtime" \
    config_dir "$config_dir" \
    runner "$runner" \
    runner_command "$runner_command" \
    verification_url "$verification_url" \
    expires_in "$expires_in" \
    session "$REUSE_SESSION" \
    scope_signature "$REUSE_SCOPE_SIGNATURE" \
    state_file "$pending_file" \
    hint "Share only this verification_url with the user; never run auth login --recommend, auth qrcode, or compose open.feishu.cn/open-apis/authen/authenticate URLs. After authorization run bash scripts/lark_cli_auth_complete.sh"
  printf '%s\n' "Pending OAuth state saved to $pending_file. Do not share device_code with the user." >&2
  exit 0
fi

cat "$err_file" >&2
lark_cli_json_object \
  status oauth-start-failed \
  runtime "$runtime" \
  config_dir "$config_dir" \
  runner "$runner" \
  runner_command "$runner_command" \
  hint "Inspect lark-cli auth login diagnostics, but do not switch to auth login --recommend or auth qrcode. Retry this wrapper or resolve missing app/user permissions"
exit 1
