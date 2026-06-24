# Installation And Local State

## Agent-Local Config Directory

Always use an agent-local `LARKSUITE_CLI_CONFIG_DIR`.

Selection order used by the helper scripts:

1. Runtime-provided `LARKSUITE_CLI_CONFIG_DIR`.
2. `$HOME/.picoclaw/workspace/.lark-cli` when the PicoClaw workspace exists.
3. `$HOME/.openclaw/workspace/.lark-cli` when the OpenClaw workspace exists.
4. `$CODEX_HOME/lark-cli` when `CODEX_HOME` is set.
5. `$PWD/.lark-cli` as the final fallback.

The directory is created with mode `0700`. Do not move an agent to global `~/.lark-cli` unless the user explicitly asks for a host-level manual setup.

For PicoClaw/OpenClaw binding, the helper points native `lark-cli` at the `.lark-cli` root because `config bind --source ...` writes source profiles (`lark-channel`, `openclaw`) under that root. After a profile is bound, commands are executed against that profile directory so bot config, OAuth state, and API calls share the same source-profile workspace. Agents should never switch directories manually.

## Bootstrap

Run:

```bash
bash scripts/lark_cli_ready.sh
```

Run it with Bash, not with `sh`.

This is the unified readiness entrypoint used by approval and generic flows. It does one-step initialization:

1. Detect/prepare the agent-local config directory and runner.
2. Install-path check (`lark-cli` binary or `npx` fallback).
3. Validate runtime CLI availability.
4. Run `lark-cli doctor --offline`.
5. Auto-run app binding when config is missing (`not-configured`), then re-run doctor.
6. Return final status (`ready`, `bot-ready`, `user-ready`, or failure state with reasons).

The script is idempotent. It does not overwrite app binding, user OAuth state, or existing `lark-cli` config. It does not require global npm install permissions inside OpenClaw/PicoClaw containers.

## Direct CLI Wrapper

For schema lookup, raw API calls, or other `lark-cli` subcommands, prefer:

```bash
bash scripts/lark_cli_run.sh <lark-cli args...>
```

Examples:

```bash
bash scripts/lark_cli_run.sh schema search drive
bash scripts/lark_cli_run.sh api GET /open-apis/authen/v1/user_info --as user
```

This wrapper preserves `LARKSUITE_CLI_CONFIG_DIR` and uses the same binary/npx fallback as the other scripts.

## Expected JSON States

Ready:

```json
{"status":"ready","config_dir":"...","version":"...","doctor_status":"ok","next":"doctor"}
```

Missing Node tooling:

```json
{"status":"not-installed","reason":"node_or_npm_missing","hint":"Install Node.js/npm/npx or provide lark-cli on PATH"}
```

Missing both `lark-cli` and npx:

```json
{"status":"not-installed","reason":"lark_cli_runner_missing","hint":"Install lark-cli on PATH or provide Node.js/npm/npx"}
```

Missing config after binary check:

```json
{"status":"not-configured","reason":"config_missing","binary_status":"ready","next":"bind"}
```

This is not fatal. The readiness script already attempts binding automatically, but if credentials are unavailable in this runtime, it returns a non-ready status for the operator to act on.

Doctor failure unrelated to missing config:

```json
{"status":"check-failed","reason":"doctor_failed","hint":"Run lark-cli doctor --offline for details"}
```

Keep stdout as JSON. Treat stderr as diagnostics.
