#!/usr/bin/env bash

: "${LARK_CLI_NPX_PACKAGE:=@larksuite/cli@latest}"

lark_cli_json_escape() {
  local value="${1-}"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '%s' "$value"
}

lark_cli_json_object() {
  local first=1
  local key
  local value

  printf '{'
  while [ "$#" -gt 0 ]; do
    key="$1"
    value="$2"
    shift 2
    if [ "$first" -eq 0 ]; then
      printf ','
    fi
    first=0
    printf '"%s":"%s"' "$key" "$(lark_cli_json_escape "$value")"
  done
  printf '}\n'
}

lark_cli_json_get_string() {
  local key="$1"
  local input="$2"
  printf '%s' "$input" | tr '\n' ' ' | sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"
}

lark_cli_json_get_number() {
  local key="$1"
  local input="$2"
  printf '%s' "$input" | tr '\n' ' ' | sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p"
}

lark_cli_openclaw_dir() {
  if [ -n "${OPENCLAW_HOME:-}" ]; then
    local explicit_home
    explicit_home="${OPENCLAW_HOME%/}"
    if [ -d "$explicit_home/.openclaw" ]; then
      printf '%s' "$explicit_home/.openclaw"
      return
    fi
    printf '%s' "$explicit_home"
    return
  fi

  if [ -d "${HOME:-}/.openclaw" ]; then
    printf '%s' "$HOME/.openclaw"
    return
  fi

  case "$PWD" in
    */.openclaw/workspace|*/.openclaw/workspace/*)
      local prefix
      prefix="${PWD%%/.openclaw/workspace*}"
      if [ -n "$prefix" ] && [ -d "$prefix/.openclaw" ]; then
        printf '%s' "$prefix/.openclaw"
        return
      fi
      ;;
  esac
}

lark_cli_native_openclaw_home() {
  local openclaw_dir="${1:-$(lark_cli_openclaw_dir)}"
  case "$openclaw_dir" in
    */.openclaw) printf '%s' "${openclaw_dir%/.openclaw}" ;;
    *) printf '%s' "$openclaw_dir" ;;
  esac
}

lark_cli_runtime() {
  if [ -n "${PICOCLAW_CHANNELS_FEISHU_ENABLED:-}" ] || [ -d "${HOME:-}/.picoclaw" ]; then
    printf 'picoclaw'
  elif [ -n "$(lark_cli_openclaw_dir)" ]; then
    printf 'openclaw'
  elif [ -n "${CODEX_HOME:-}" ]; then
    printf 'codex'
  else
    printf 'other'
  fi
}

lark_cli_runtime_profile() {
  local rt="${1:-$(lark_cli_runtime)}"
  case "$rt" in
    picoclaw) printf 'lark-channel' ;;
    openclaw) printf 'openclaw' ;;
  esac
}

lark_cli_runtime_profile_dir() {
  local rt="${1:-$(lark_cli_runtime)}"
  local root="${2:-}"

  if [ -z "$root" ]; then
    printf '%s' "$LARKSUITE_CLI_CONFIG_DIR"
    return
  fi

  case "$rt" in
    openclaw|picoclaw)
      local profile
      profile="$(lark_cli_runtime_profile "$rt")"
      if [ -n "$profile" ]; then
        if [ "$(basename "$root")" = "$profile" ]; then
          while :; do
            local parent="${root%/$profile}"
            parent="${parent%/}"
            if [ -z "$parent" ] || [ "$parent" = "$root" ] || [ "$(basename "$parent")" != "$profile" ]; then
              break
            fi
            root="$parent"
          done
        fi

        if [ -f "$root/config.json" ]; then
          printf '%s' "$root"
          return
        fi

        if [ -f "$root/$profile/config.json" ]; then
          printf '%s' "$root/$profile"
          return
        fi
      fi
      ;;
  esac

  printf '%s' "$root"
}

lark_cli_apply_openclaw_hardcoded_context() {
  local rt="${1:-}"
  if [ "$rt" != "openclaw" ]; then
    return 0
  fi

  # OpenClaw worker in our current deployment container uses a stable path.
  # Force the same runtime context for all OpenClaw approval flows to avoid
  # implicit path drift between shell invocations.
  local hardcoded_openclaw_home="/home/node"
  local hardcoded_lark_cli_config_dir="/home/node/.openclaw/workspace/.lark-cli"

  if [ ! -d "${hardcoded_openclaw_home}/.openclaw" ] && [ -d "${HOME:-}/.openclaw" ]; then
    hardcoded_openclaw_home="${HOME%/}"
    hardcoded_lark_cli_config_dir="${HOME%/}/.openclaw/workspace/.lark-cli"
  fi

  OPENCLAW_HOME="$hardcoded_openclaw_home"
  export OPENCLAW_HOME
  LARKSUITE_CLI_CONFIG_DIR="$hardcoded_lark_cli_config_dir"
  LARKSUITE_CLI_CONFIG_DIR="${LARKSUITE_CLI_CONFIG_DIR%/}"
  export LARKSUITE_CLI_CONFIG_DIR
}

lark_cli_default_config_root() {
  local rt="${1:-$(lark_cli_runtime)}"
  case "$rt" in
    picoclaw)
      if [ -d "${HOME:-}/.picoclaw/workspace" ]; then
        printf '%s' "$HOME/.picoclaw/workspace/.lark-cli"
        return
      fi
      ;;
    openclaw)
      printf '%s' '/home/node/.openclaw/workspace/.lark-cli'
      return
      ;;
  esac

  if [ -n "${CODEX_HOME:-}" ]; then
    printf '%s' "$CODEX_HOME/lark-cli"
  else
    printf '%s' "$PWD/.lark-cli"
  fi
}

lark_cli_prepare_config_dir() {
  if [ -z "${LARKSUITE_CLI_CONFIG_DIR:-}" ]; then
    local rt
    local root
    rt="$(lark_cli_runtime)"
    root="$(lark_cli_default_config_root "$rt")"
    export LARKSUITE_CLI_CONFIG_DIR="$root"
  else
    LARKSUITE_CLI_CONFIG_DIR="${LARKSUITE_CLI_CONFIG_DIR%/}"
    export LARKSUITE_CLI_CONFIG_DIR
  fi

  mkdir -p "$LARKSUITE_CLI_CONFIG_DIR"
  chmod 700 "$LARKSUITE_CLI_CONFIG_DIR" 2>/dev/null || true
  printf '%s' "$LARKSUITE_CLI_CONFIG_DIR"
}

lark_cli_config_bind_root() {
  local rt="${1:-$(lark_cli_runtime)}"
  local dir="${2:-${LARKSUITE_CLI_CONFIG_DIR:-}}"
  local profile
  profile="$(lark_cli_runtime_profile "$rt")"

  if [ -n "${LARKSUITE_CLI_CONFIG_ROOT:-}" ]; then
    printf '%s' "$LARKSUITE_CLI_CONFIG_ROOT"
    return
  fi

  if [ -n "$profile" ] && [ "${dir%/$profile}" != "$dir" ]; then
    printf '%s' "${dir%/$profile}"
  else
    printf '%s' "$dir"
  fi
}

lark_cli_init_context() {
  runtime="$(lark_cli_runtime)"
  lark_cli_apply_openclaw_hardcoded_context "$runtime"
  config_root="$(lark_cli_prepare_config_dir)"
  if [ "$runtime" = "openclaw" ]; then
    config_dir="$config_root"
  else
    config_dir="$(lark_cli_runtime_profile_dir "$runtime" "$config_root")"
  fi
  export LARKSUITE_CLI_CONFIG_DIR="$config_dir"
  config_profile="$(lark_cli_runtime_profile "$runtime")"
  mkdir -p "$config_root"
  chmod 700 "$config_root" 2>/dev/null || true
  if [ "$config_root" != "$config_dir" ]; then
    mkdir -p "$config_dir"
    chmod 700 "$config_dir" 2>/dev/null || true
  fi
}

lark_cli_runner_kind() {
  if command -v lark-cli >/dev/null 2>&1; then
    printf 'binary'
  elif command -v npx >/dev/null 2>&1; then
    printf 'npx'
  else
    printf 'missing'
  fi
}

lark_cli_runner_display() {
  case "$(lark_cli_runner_kind)" in
    binary) command -v lark-cli ;;
    npx) printf 'npx -y %s' "$LARK_CLI_NPX_PACKAGE" ;;
    *) printf 'missing' ;;
  esac
}

lark_cli_exec() {
  if command -v lark-cli >/dev/null 2>&1; then
    command lark-cli "$@"
  elif command -v npx >/dev/null 2>&1; then
    command npx -y "$LARK_CLI_NPX_PACKAGE" "$@"
  else
    printf '%s\n' "lark-cli is not on PATH and npx is unavailable" >&2
    return 127
  fi
}

lark_cli_require_runner() {
  if [ "$(lark_cli_runner_kind)" != "missing" ]; then
    return 0
  fi

  local current_config_dir="${LARKSUITE_CLI_CONFIG_DIR:-}"
  local current_runtime
  current_runtime="$(lark_cli_runtime)"
  lark_cli_json_object \
    status not-installed \
    reason lark_cli_runner_missing \
    runtime "$current_runtime" \
    config_dir "$current_config_dir" \
    hint "Install lark-cli on PATH or provide Node.js/npm/npx so scripts can run npx -y @larksuite/cli@latest"
  return 1
}

lark_cli_require_binary() {
  lark_cli_require_runner
}
