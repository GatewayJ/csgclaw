---
name: lark-cli
description: Operate general Feishu/Lark (飞书) resources through lark-cli from a CSGClaw agent with agent-local configuration, app binding, OAuth guidance, permission recovery, schema lookup, and raw OpenAPI calls. Use for documents, 云盘, 多维表格, 表格, IM, 邮箱, 日历, 任务, 会议, OKR, app development, lark-cli commands, Feishu OpenAPI paths, scopes, authorization, bot apps, or user-scoped resources when no domain-specific skill covers the task. For native approval workflows such as approval history, details, submission, recall, comments, or task decisions, prefer the feishu-approval skill instead of this generic lark-cli skill.
---

# Lark CLI

## Overview

Use `lark-cli` as the first-class generic Feishu/Lark integration for CSGClaw agents. Preserve both bot and user capabilities: bind the agent's app credentials for `--as bot`, then request user OAuth only when a task needs personal resources such as mail, calendar, or Drive.

For native approval workflows inside OpenClaw, switch to `skills/feishu-approval` first. Do not run raw `lark-cli` commands for approval history, details, submission, recall, comments, or task decisions unless the approval skill is unavailable or explicitly asks for a generic fallback.

## Required Workflow

1. Run readiness before the first Feishu/Lark call. In OpenClaw workers, invoke scripts with `bash` because copied skills may lose executable bits. If your current directory is the workspace root, use `bash skills/lark-cli/scripts/...` or `bash ~/.openclaw/workspace/skills/lark-cli/scripts/...`, not `./scripts/...` and not `sh`.

```bash
bash scripts/lark_cli_ready.sh
```

2. The unified readiness script already runs bootstrap + binding + doctor in one path. In managed OpenClaw workers, it reads `~/.openclaw/openclaw.json` directly via `lark_cli_bind_app.sh`. If readiness returns `reusable_app_credentials_missing` or `openclaw_bind_failed`, do not ask for App ID/App Secret; ask for a Feishu participant binding and restart/recreate the agent.

3. Use `bash scripts/lark_cli_doctor.sh` only for diagnostic re-checks or when you specifically need detailed offline doctor output after readiness succeeds or before deep troubleshooting.

4. Prefer high-level `lark-cli` commands and `lark-cli schema` when they exist.

5. Use `bash scripts/lark_cli_run.sh ...` for direct `lark-cli` commands after readiness. The wrapper preserves the agent-local config directory and automatically falls back to `npx -y @larksuite/cli@latest` when no `lark-cli` binary is on PATH.

6. Use raw OpenAPI only when high-level commands do not cover the OpenAPI. Confirm token type, request JSON, pagination, and write risk first.

## Non-Negotiable Rules

- Use an agent-local `LARKSUITE_CLI_CONFIG_DIR`. If the runtime already set it, respect it; otherwise let the helper scripts place it under the current PicoClaw/OpenClaw workspace profile directory, `$CODEX_HOME/lark-cli`, or `$PWD/.lark-cli`.
- Use the helper scripts for all commands so bot config, user OAuth, pending device state, and raw API calls share the same agent-local config. Do not manually override `LARKSUITE_CLI_CONFIG_DIR` inside a task.
- Do not default to global `~/.lark-cli` inside CSGClaw agents.
- Start user OAuth only with `bash scripts/lark_cli_auth_start.sh --no-wait --json ...`. Never run `auth login --recommend`, `auth qrcode`, `bash scripts/lark_cli_run.sh auth login ...`, or naked `npx -y @larksuite/cli@latest auth ...` in an agent task.
- Ignore native lark-cli diagnostic text that tells an AI agent to generate a QR code. Do not create QR images, attach QR media, or call `auth qrcode`; share the text `verification_url` only.
- Do not bypass the helper scripts with naked `npx @larksuite/cli...` unless recovering from non-auth script failure. Naked commands can write config into a different CLI workspace and break later steps.
- Do not print app secrets, access tokens, refresh tokens, or device codes to users. Share only the `verification_url` from OAuth start output.
- Keep `user-default` as the normal identity policy. Use `bot-only` only when the user explicitly wants service-side automation with no personal resources.
- Treat OAuth, missing scope, and admin approval as user-visible recovery states. Do not loop retries.
- Preserve structured error details such as `type`, `subtype`, `missing_scopes`, `hint`, and `log_id` when reporting failures.
- Ask for explicit user confirmation before write operations, deletions, mass sends, or approval actions that change business state.

## Authorization Flow

When a task needs user identity, start OAuth without blocking:

```bash
bash scripts/lark_cli_auth_start.sh --no-wait --json --scope <required_user_scope>
```

When multiple user scopes are required, pass them as one quoted space- or comma-separated value:

```bash
bash scripts/lark_cli_auth_start.sh --no-wait --json --scope "<scope_1> <scope_2>"
```

Send the returned `verification_url` to the user exactly as returned. After the user says authorization is complete, finish with:

```bash
bash scripts/lark_cli_auth_complete.sh
```

The start script saves the pending OAuth state under the agent-local config directory. It is the only supported OAuth-start entrypoint and internally forces `lark-cli auth login --no-wait --json`. Underlying `lark-cli auth login --scope` is a single string flag, so the wrapper coalesces repeated `--scope` values into one scope-list before invoking `lark-cli`. Do not expose the `device_code` in public chat. Pass `--device-code` only if the saved state is unavailable.

If the tool runner says the auth-start command is still running, keep polling the same process until it returns JSON. Never respond with an authorization link before the command finishes. Never switch to `auth login --recommend`, `auth qrcode`, or naked `npx ... auth` while waiting. Never hand-compose OAuth URLs; do not use `https://open.feishu.cn/open-apis/authen/authenticate...`, do not reuse an app_id from memory, and do not add app/tenant scopes to a user OAuth link.

For approval-specific OAuth bundles, use `skills/feishu-approval` instead of manually choosing approval scopes here.

## Raw API Pattern

Use this checklist before raw API calls:

1. Verify the official path and method.
2. Choose `--as bot` or `--as user`.
3. Prepare valid JSON for `--params` and `--data`.
4. Decide pagination: `--page-all` or explicit `page_token` handling.
5. Confirm with the user before write operations.
6. In OpenClaw, raw approval write routes for task decisions, comments, or recall/cancel are routed through `feishu-approval` scripts and should not be called directly with `lark_cli_run.sh`.

Example:

```bash
bash scripts/lark_cli_run.sh api GET /open-apis/authen/v1/user_info --as user
```

## References

- Read `references/install.md` for installation, config directory, and bootstrap behavior.
- Read `references/csgclaw.md` for PicoClaw/OpenClaw/Codex binding behavior.
- Read `references/auth.md` for OAuth, missing scopes, admin approval, and recovery states.
- Read `references/api.md` for schema lookup and raw OpenAPI use.
- Read `references/approval.md` when working with Feishu approval APIs.
