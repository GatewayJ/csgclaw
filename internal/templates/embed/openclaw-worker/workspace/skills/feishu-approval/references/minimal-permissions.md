# Minimal Permissions

## Preferred Paths

| User intent | Command/API | Identity | Minimal permission |
| --- | --- | --- | --- |
| "我曾经发起过哪些审批" | `approval instances initiated` | user OAuth | `approval:instance:read` |
| "看这个审批详情" | `approval instances get` | user OAuth | `approval:instance:read` |
| "我有哪些待审批任务" | `approval tasks query` | user OAuth | `approval:task:read` |
| "同意/拒绝" | `approval tasks approve/reject` | user OAuth | `approval:task:write` |
| "加签/转交/退回" | approval task write command | user OAuth | `approval:task:write` |
| "撤回我的审批" | `approval instances cancel` | user OAuth | `approval:instance:write` |
| "查询我可见/可发起的审批定义列表" | `GET /open-apis/approval/v4/approvals` | user OAuth | `serviceaccount:approval:approvals:read` |
| "查看审批定义/表单字段/节点" | `GET /open-apis/approval/v4/approvals/:approval_code` | app/tenant | `approval:approval:readonly` |
| "提交一个新的审批实例" | `POST /open-apis/approval/v4/instances` | app/tenant | `approval:instance` |
| "给审批添加评论" | `POST /open-apis/approval/v4/instances/:instance_id/comments` | app/tenant | `approval:instance.comment` |

## Grouped Permission Requests

For user-identity approval operations, request one user OAuth bundle when OAuth is needed:

```text
approval:instance:read approval:task:read approval:task:write approval:instance:write
serviceaccount:approval:approvals:read
```

This bundle covers initiated/history/detail reads, current user's task list, task approve/reject, recall/cancel operations, and the current user's visible approval definition catalog. It is requested through the `lark-cli` OAuth URL, not through the Open Platform app permission page.

For app-identity approval operations, generate one Feishu Open Platform tenant link for the app approval bundle:

```bash
bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose app
```

The app/tenant bundle is:

- `approval:approval:readonly`: read approval definition form fields and nodes.
- `approval:instance`: create native approval instances with the app/bot token.

Approval comments use a separate app/tenant scope:

```bash
bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose comment
```

The comment helper uses `user_id_type=open_id` for the comment operator. Do not request `contact:user.employee_id:readonly` just to add approval comments.

The generated `console_url` includes `token_type=tenant`; this is required for bot/app calls that use `tenant_access_token`.

The script also accepts aliases for common app-side requests:

```bash
bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose definition
bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose submit
bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose all
```

`definition` and `submit` map to the app/tenant bundle (`approval:approval:readonly`, `approval:instance`). `all` requests that bundle plus `approval:instance.comment`; use it only when the user explicitly wants submission/schema and comments covered in one admin request.

For user-visible approval definition catalogs, use the same grouped user OAuth bundle:

```bash
bash skills/feishu-approval/scripts/approval_list_definitions.sh
```

The catalog API uses `serviceaccount:approval:approvals:read`. Despite the `serviceaccount:` prefix, this scope is part of the grouped user OAuth flow for listing approval definitions visible to the current user.

## What Not To Do

- Do not call `/open-apis/approval/v4/approvals` when a known previous instance is enough to identify the target approval for resubmission. Use it when the user asks for the full visible approval catalog.
- Do not ask for `approval:approval`, `approval:approval:readonly`, or `approval:definition` when the user only asks for previously initiated instances.
- Do not ask for broad `approval:approval` for normal approval submission. Use the grouped app/tenant link instead of asking for permissions one by one.
- Do not add `approval:instance.comment` unless the user is adding comments or explicitly wants all app-side approval actions approved together.
- Do not ask for `approval:definition` when tenant `approval:approval:readonly` is enough to read the approval definition. If Feishu returns a console URL containing multiple alternative scopes, keep the grouped app approval bundle and do not add broad `approval:approval`.
- Do not ask for `approval:approval.list:readonly` for normal submitter workflows; it is broader tenant-side approval query access.
- Do not retry OAuth after `app_scope_not_applied`. That is an app/admin approval problem.
- Do not retry approval creation with user identity after `user access token not support`.

## Practical Discovery Strategy

When the user asks to resubmit or continue from a known previous approval, first use their previously initiated approvals:

```bash
bash skills/feishu-approval/scripts/approval_list_initiated.sh
```

This reveals approval names and `definition_code` values that the user has actually used. If OAuth is needed, the script requests the grouped user approval OAuth bundle once.

If the user asks for a full catalog of visible approval definitions, use `approval_list_definitions.sh`. If OAuth is needed, request the grouped user approval OAuth bundle; do not generate an app/admin permission link for this catalog query.

If the user has already asked to query their approval records, showing the grouped user approval OAuth authorization URL is enough. Do not add a separate "同意授权" round before generating the URL.

Only show the OAuth URL after a script returns `oauth-pending`. If the command is still running, keep polling it. Approval user OAuth must start through `lark_cli_auth_start.sh --no-wait --json`, normally via the approval scripts. Do not run `auth login --recommend`, `auth qrcode`, or naked `npx -y @larksuite/cli@latest auth ...`. Do not hand-compose `open.feishu.cn/open-apis/authen/authenticate` URLs, do not reuse an app_id from memory, and do not put app/tenant scopes such as `approval:approval:readonly` into the user OAuth link.

After the user says "已授权", complete the saved device flow with:

```bash
bash skills/feishu-approval/scripts/approval_auth_complete.sh
```

Only after this returns `user-ready` should the agent rerun the original approval command.
