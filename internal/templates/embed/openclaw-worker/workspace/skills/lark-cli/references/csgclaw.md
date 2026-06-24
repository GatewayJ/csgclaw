# CSGClaw Runtime Binding

## Scope

This bundled skill is installed by the OpenClaw worker template. Use the OpenClaw binding path first.

PicoClaw and Codex notes below are only fallback guidance for copied or manual setups. Do not assume this PR installs the skill into PicoClaw or Codex templates.

## Runtime Matrix

| Runtime | Primary action | Fallback |
| --- | --- | --- |
| OpenClaw | Use `bash scripts/lark_cli_ready.sh`, which performs bind through `config bind --source openclaw --identity user-default --force` with the agent-local config directory | Ask the user to bind a Feishu bot participant or run native `bash scripts/lark_cli_run.sh config init` |
| PicoClaw manual/copy fallback | Read `PICOCLAW_CHANNELS_FEISHU_APP_ID` and `PICOCLAW_CHANNELS_FEISHU_APP_SECRET`, write an agent-local `LARK_CHANNEL_CONFIG`, then bind through `bash scripts/lark_cli_bind_app.sh` | Ask the user to bind a Feishu bot participant or run native `bash scripts/lark_cli_run.sh config init` |
| Codex or other manual fallback | Do not assume CSGClaw can reveal the app secret | Use native `bash scripts/lark_cli_run.sh config init` or `bash scripts/lark_cli_run.sh config init --new` |

Run:

```bash
bash scripts/lark_cli_ready.sh
```

After binding, normal commands use the source profile directory:

- PicoClaw: `$HOME/.picoclaw/workspace/.lark-cli/lark-channel` (source profile inside `.lark-cli`)
- OpenClaw: `$HOME/.openclaw/workspace/.lark-cli/openclaw` (source profile inside `.lark-cli`)

Do not run raw `npx @larksuite/cli ...` with a different config directory. Use the bundled helper scripts so app binding, OAuth completion, and raw API calls all use the same source profile directory.

## PicoClaw Manual Fallback

PicoClaw app credentials come from the current Feishu participant binding. Prefer environment variables over private config files:

```bash
PICOCLAW_CHANNELS_FEISHU_ENABLED=true
PICOCLAW_CHANNELS_FEISHU_APP_ID=cli_xxx
PICOCLAW_CHANNELS_FEISHU_APP_SECRET=...
```

The helper writes `csgclaw-lark-channel.json` under `LARKSUITE_CLI_CONFIG_DIR` with a secret reference:

```json
{
  "accounts": {
    "app": {
      "id": "cli_xxx",
      "secret": "${PICOCLAW_CHANNELS_FEISHU_APP_SECRET}",
      "tenant": "feishu"
    }
  }
}
```

Do not expand the secret into logs or user-visible messages.

## OpenClaw Rules

OpenClaw uses agent-local `openclaw.json`. Let `lark-cli` read it directly:

```bash
bash scripts/lark_cli_ready.sh
```

Do not generate a PicoClaw-style projection for OpenClaw.

If this returns `reason=openclaw_bind_failed` or `reason=reusable_app_credentials_missing`, do not ask for App ID or App Secret inside the agent. The OpenClaw runtime should provide the Feishu app through `~/.openclaw/openclaw.json` (`channels.feishu.accounts`). Ask the user to bind a Feishu participant to the agent in CSGClaw and recreate or restart the agent so the runtime config is regenerated.

## Participant Binding Context

CSGClaw stores the real Feishu app secret in participant runtime state. Public API responses redact it as `present`, so an agent must not try to recover secrets through CSGClaw HTTP APIs.

If no reusable app credentials are present, tell the user to bind a Feishu bot app, for example:

```bash
printf '%s' "$APP_SECRET" | csgclaw-cli participant bind \
  --channel feishu \
  --feishu-kind bot \
  --agent u-manager \
  --app-id cli_xxx \
  --app-secret-stdin \
  --restart
```

If the user cannot bind a CSGClaw Feishu participant, use native `lark-cli` initialization in the same agent-local config directory:

```bash
bash scripts/lark_cli_run.sh config init --new
```

Or with an existing app:

```bash
printf '%s' "$APP_SECRET" | bash scripts/lark_cli_run.sh config init \
  --app-id "$APP_ID" \
  --app-secret-stdin \
  --brand feishu
```

Do not print `APP_SECRET`. Prefer CSGClaw participant binding inside managed agents because it keeps the app credential source consistent with the runtime.
