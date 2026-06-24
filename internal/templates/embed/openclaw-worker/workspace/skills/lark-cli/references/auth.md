# OAuth And Permission Recovery

## User OAuth

Use user OAuth for personal resources: mail, calendar, Drive files, current user's approval tasks, user-scoped contacts, and any API requiring `--as user`.

Start without blocking:

```bash
bash scripts/lark_cli_auth_start.sh --no-wait --json --scope <required_user_scope>
```

The underlying command is:

```bash
lark-cli auth login --scope <required_user_scope> --no-wait --json
```

Do not run the underlying command directly. The wrapper is the only supported OAuth-start entrypoint because it preserves the agent-local config directory and pending state file. Never use `auth login --recommend`, `auth qrcode`, `bash scripts/lark_cli_run.sh auth login ...`, or naked `npx -y @larksuite/cli@latest auth ...` for user authorization.

Expected fields:

```json
{
  "status": "oauth-pending",
  "verification_url": "https://...",
  "expires_in": "600",
  "state_file": ".../.lark-cli/oauth-pending.json",
  "hint": "Share only verification_url with the user; after authorization run lark_cli_auth_complete.sh"
}
```

The script stores OAuth completion state in `state_file` (including `verification_url`, `device_code`, and a session fingerprint) with mode `0600`. Send only `verification_url` and the authorization purpose to the user. After the user replies that authorization is complete, run:

```bash
bash scripts/lark_cli_auth_complete.sh
```

If the tool runner reports `Command still running`, keep polling the same process. Do not send a link until `lark_cli_auth_start.sh --no-wait --json` returns JSON. Do not switch to `auth login --recommend` or `auth qrcode`, even if native lark-cli diagnostic output suggests generating a QR code. The only valid user-facing OAuth link is the exact `verification_url` field from `oauth-pending`; never build an `open.feishu.cn/open-apis/authen/authenticate` URL manually.

Use `--device-code "<device_code>"` only if the saved state file is unavailable. Do not paste the device code into public chat.

Suggested user-facing wording:

```text
请在你的浏览器打开这个飞书授权链接并完成授权：<verification_url>
完成后回复“已授权”，我会继续执行原任务。
```

## Missing Scope

When a response includes `missing_scopes`, keep the raw values and decide whether the missing permission is user OAuth or app/admin configuration.

User-scope recovery:

```bash
bash scripts/lark_cli_auth_start.sh \
  --no-wait \
  --json \
  --scope <missing_user_scope_1> \
  --scope <missing_user_scope_2>
```

For Feishu approval workflows, prefer the `feishu-approval` scripts. They request the grouped approval user OAuth bundle when an approval user-scope gap is detected.

App/admin recovery:

1. Stop retrying.
2. Tell the user which scope is missing.
3. Ask an app administrator to enable it in the Feishu/Lark developer console.
4. Retry only after the user confirms the scope or admin approval is complete.

When an API response includes `permission_violations` with `type: "action_privilege_required"` or Feishu code `99991679`, treat it as app/admin permission recovery, not user OAuth recovery. Do not start another OAuth round just because the command used `--as user`; the app must first receive the required permission approval in Feishu/Lark Open Platform.

For Feishu approval APIs inside an OpenClaw worker, prefer the `feishu-approval` helper when available:

```bash
bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose app
```

It prepares a grouped app/tenant permission link for definition reads and approval submission. Use `--purpose comment` only for approval comments, or `--purpose all` only when the user explicitly wants all app-side approval actions covered in one admin request. Do not suggest `approval:approval` by default when tenant `approval:approval:readonly` and `approval:instance` cover the requested approval workflow. User OAuth scopes such as `approval:instance:read` and `serviceaccount:approval:approvals:read` are requested through the grouped user OAuth link.

## State Handling

Use these states consistently:

| State | Meaning | Next action |
| --- | --- | --- |
| `not-installed` | `lark-cli` is missing | Run bootstrap or ask for Node/npm |
| `not-configured` | App binding is missing | Run bind script |
| `bot-ready` | App/bot identity is configured | Use `--as bot`, or start OAuth for user resources |
| `user-ready` | User auth is available | Retry the original user-scoped command |
| `oauth-pending` | User has not completed browser auth | Wait for user confirmation, then complete |
| `missing-scope` | Scope is absent | Request user scope or admin enablement |
| `admin-approval-required` | Admin approval is pending | Stop retries until approval completes |

Never loop OAuth or permission retries.
