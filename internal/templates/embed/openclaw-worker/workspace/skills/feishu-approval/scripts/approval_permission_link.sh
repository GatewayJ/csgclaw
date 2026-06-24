#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

APP_TENANT_SCOPES="approval:approval:readonly approval:instance"
COMMENT_APP_TENANT_SCOPES="approval:instance.comment"

json_escape() {
  local value="${1-}"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '%s' "$value"
}

extract_app_id_from_file() {
  local file="$1"
  [ -r "$file" ] || return 1
  sed -n \
    -e 's/.*"appId"[[:space:]]*:[[:space:]]*"\(cli_[^"]*\)".*/\1/p' \
    -e 's/.*"app_id"[[:space:]]*:[[:space:]]*"\(cli_[^"]*\)".*/\1/p' \
    "$file" | head -1
}

find_app_id_from_files() {
  local value
  local file
  for file in \
    "${LARKSUITE_CLI_CONFIG_DIR:-}/config.json" \
    "${LARKSUITE_CLI_CONFIG_DIR:-}/openclaw/config.json" \
    "${HOME:-}/.openclaw/workspace/.lark-cli/openclaw/config.json" \
    "${HOME:-}/.openclaw/workspace/.lark-cli/config.json" \
    "${HOME:-}/.openclaw/openclaw.json" \
    "$PWD/.lark-cli/openclaw/config.json" \
    "$PWD/.lark-cli/config.json"
  do
    value="$(extract_app_id_from_file "$file" 2>/dev/null || true)"
    case "$value" in
      cli_*) printf '%s' "$value"; return 0 ;;
    esac
  done

  return 1
}

find_app_id() {
  local value
  for value in \
    "${explicit_app_id:-}"
  do
    case "$value" in
      cli_*) printf '%s' "$value"; return 0 ;;
    esac
  done

  value="$(find_app_id_from_files || true)"
  case "$value" in
    cli_*) printf '%s' "$value"; return 0 ;;
  esac

  approval_bootstrap bot >/dev/null 2>&1 || true
  value="$(find_app_id_from_files || true)"
  case "$value" in
    cli_*) printf '%s' "$value"; return 0 ;;
  esac

  for value in \
    "${FEISHU_APP_ID:-}" \
    "${LARK_APP_ID:-}" \
    "${LARKSUITE_APP_ID:-}"
  do
    case "$value" in
      cli_*) printf '%s' "$value"; return 0 ;;
    esac
  done

  return 1
}

scopes_json() {
  local scopes="$1"
  local first=1
  local scope
  printf '['
  for scope in $scopes; do
    if [ "$first" -eq 0 ]; then
      printf ','
    fi
    first=0
    printf '"%s"' "$(json_escape "$scope")"
  done
  printf ']'
}

scopes_query() {
  local scopes="$1"
  local query=""
  local sep=""
  local scope
  for scope in $scopes; do
    scope="${scope//:/%3A}"
    query="$query$sep$scope"
    sep="%2C"
  done
  printf '%s' "$query"
}

domain="feishu"
scopes="$APP_TENANT_SCOPES"
explicit_app_id=""
token_type="tenant"
purpose="app"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --app-id) explicit_app_id="${2:?}"; shift 2 ;;
    --domain) domain="${2:?}"; shift 2 ;;
    --purpose) purpose="${2:?}"; shift 2 ;;
    --all-minimal) purpose="all"; shift ;;
    -h|--help)
      printf '%s\n' "Usage: $0 [--app-id cli_xxx] [--purpose app|all|definition|submit|comment] [--all-minimal] [--domain feishu|lark]"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

case "$purpose" in
  app|definition|submit) scopes="$APP_TENANT_SCOPES" ;;
  comment) scopes="$COMMENT_APP_TENANT_SCOPES" ;;
  all) scopes="$APP_TENANT_SCOPES $COMMENT_APP_TENANT_SCOPES" ;;
  *) printf 'Unknown purpose: %s\n' "$purpose" >&2; exit 2 ;;
esac

base_url="https://open.feishu.cn"
case "$domain" in
  lark|larksuite) base_url="https://open.larksuite.com" ;;
esac

app_id="$(find_app_id || true)"
query="$(scopes_query "$scopes")"

if [ -n "$app_id" ]; then
  console_url="$base_url/app/$app_id/auth?q=$query&token_type=$token_type"
  printf '{'
  printf '"status":"permission-link",'
  printf '"app_id":"%s",' "$(json_escape "$app_id")"
  printf '"token_type":"%s",' "$(json_escape "$token_type")"
  printf '"purpose":"%s",' "$(json_escape "$purpose")"
  printf '"scopes":%s,' "$(scopes_json "$scopes")"
  printf '"console_url":"%s",' "$(json_escape "$console_url")"
  printf '"hint":"Ask the Feishu/Lark app administrator to approve these app/tenant scopes, then retry after approval completes. User OAuth scopes are requested separately by lark-cli auth links."'
  printf '}\n'
else
  printf '{'
  printf '"status":"app-id-missing",'
  printf '"token_type":"%s",' "$(json_escape "$token_type")"
  printf '"purpose":"%s",' "$(json_escape "$purpose")"
  printf '"scopes":%s,' "$(scopes_json "$scopes")"
  printf '"hint":"Could not detect the current Feishu app_id. Open the Feishu/Lark Open Platform app permission page and apply only these scopes."'
  printf '}\n'
fi
