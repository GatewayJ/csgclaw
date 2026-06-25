#!/usr/bin/env bash

approval_json_get_string() {
  local key="$1"
  local input="$2"
  printf '%s' "$input" | tr '\n' ' ' | sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"
}

approval_json_escape() {
  local value="${1-}"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '%s' "$value"
}

approval_user_oauth_scopes() {
  printf '%s' "approval:instance:read approval:task:read approval:task:write approval:instance:write serviceaccount:approval:approvals:read"
}

approval_skill_dir() {
  CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd
}

approval_lark_skill_dir() {
  local skill_dir
  skill_dir="$(approval_skill_dir)"
  local candidate="$skill_dir/../lark-cli"
  if [ -d "$candidate/scripts" ]; then
    printf '%s' "$candidate"
    return
  fi
  printf '%s' "${LARK_CLI_SKILL_DIR:-$PWD/skills/lark-cli}"
}

approval_lark_script() {
  local name="$1"
  local lark_dir
  lark_dir="$(approval_lark_skill_dir)"
  printf '%s/scripts/%s' "$lark_dir" "$name"
}

approval_run_script() {
  local script="$1"
  shift
  if [ ! -f "$script" ]; then
    printf 'Script not found: %s\n' "$script" >&2
    return 127
  fi
  if [ -x "$script" ]; then
    "$script" "$@"
    return
  fi
  bash "$script" "$@"
}

approval_require_lark_skill() {
  local run_script
  run_script="$(approval_lark_script lark_cli_run.sh)"
  if [ ! -f "$run_script" ]; then
    printf '{"status":"not-installed","reason":"lark_cli_skill_missing","hint":"OpenClaw worker must include skills/lark-cli"}\n'
    return 1
  fi
}

approval_ensure_bound() {
  approval_require_lark_skill || return 1

  local ready_script output status

  ready_script="$(approval_lark_script lark_cli_ready.sh)"
  output="$(approval_run_script "$ready_script" 2>/dev/null || true)"
  status="$(approval_json_get_string status "$output")"

  if [ "$status" = "ready" ] || [ "$status" = "bot-ready" ] || [ "$status" = "user-ready" ]; then
    printf '%s\n' "$output"
    return 0
  fi

  printf '%s\n' "$output"
  return 1
}

approval_bootstrap() {
  local mode="${1:-bot}"
  local scopes="${2:-$(approval_user_oauth_scopes)}"

  case "$mode" in
    bot)
      approval_ensure_bound
      ;;
    user)
      if [ -z "$scopes" ]; then
        scopes="$(approval_user_oauth_scopes)"
      fi
      approval_ensure_user_scope "$scopes"
      ;;
    *)
      printf '{"status":"invalid-mode","reason":"mode-must-be-bot-or-user","mode":"%s"}\n' "$(approval_json_escape "$mode")"
      return 2
      ;;
  esac
}

approval_ensure_user_scope() {
  local scopes="$1"
  local auth status bound

  bound="$(approval_ensure_bound 2>&1)" || {
    printf '%s\n' "$bound"
    return 1
  }

  status="$(approval_json_get_string status "$bound")"
  if [ "$status" = "user-ready" ]; then
    return 0
  fi
  if [ "$status" != "ready" ] && [ "$status" != "bot-ready" ]; then
    printf '%s\n' "$bound"
    return 1
  fi

  auth="$(approval_lark_script lark_cli_auth_start.sh)"
  local args=(--no-wait --json)
  args+=(--scope "$scopes")
  approval_run_script "$auth" "${args[@]}"
  return 2
}

approval_request_user_scope() {
  local scopes="$1"
  local auth
  auth="$(approval_lark_script lark_cli_auth_start.sh)"
  local args=(--no-wait --json)
  args+=(--scope "$scopes")
  approval_run_script "$auth" "${args[@]}"
}

approval_show_permission_link() {
  local link_script
  link_script="$(approval_skill_dir)/scripts/approval_permission_link.sh"
  if [ -f "$link_script" ]; then
    approval_run_script "$link_script" --purpose app || true
  fi
}

approval_run_user_scoped() {
  local scopes="$1"
  shift

  local auth_rc
  set +e
  approval_ensure_user_scope "$scopes"
  auth_rc=$?
  set -e
  if [ "$auth_rc" -ne 0 ]; then
    return "$auth_rc"
  fi

  local output
  local run_rc
  set +e
  output="$(approval_run_lark "$@" 2>&1)"
  run_rc=$?
  set -e

  if [ "$run_rc" -ne 0 ]; then
    case "$output" in
      *"action_privilege_required"*|*"permission_violations"*|*"app_scope_not_applied"*)
        printf '%s\n' "$output"
        approval_show_permission_link
        return "$run_rc"
        ;;
    esac
    if printf '%s' "$output" | grep -Eq 'missing_scope|missing_scopes'; then
      approval_request_user_scope "$scopes"
      return 2
    fi
    local scope
    for scope in $scopes; do
      if printf '%s' "$output" | grep -Fq "$scope"; then
        approval_request_user_scope "$scopes"
        return 2
      fi
    done
    printf '%s\n' "$output"
    return "$run_rc"
  fi

  printf '%s\n' "$output"
}

approval_run_lark() {
  local run_script
  run_script="$(approval_lark_script lark_cli_run.sh)"
  OPENCLAW_APPROVAL_ALLOW_RAW=1 approval_run_script "$run_script" "$@"
}
