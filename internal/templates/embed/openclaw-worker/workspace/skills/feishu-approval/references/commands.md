# Commands

This file is the single source for `feishu-approval` script command shapes. For `oauth-pending`, still-running commands, and OpenClaw binding failures, follow `oauth-recovery.md`.

## Preflight

Approval scripts already include readiness checks. If a command is first run after restart, optionally warm-start one mode before the first call:

```bash
bash skills/feishu-approval/scripts/approval_bootstrap.sh --mode bot
# or
bash skills/feishu-approval/scripts/approval_bootstrap.sh --mode user
```

Use `--mode user` for user-token flows (initiated/list/detail/tasks/cancel/approve/reject) and `--mode bot` for bot-token flows (definitions/schema/create/comment).

## List Approvals Initiated By Current User

Uses the grouped user approval OAuth bundle when auth is needed; do not generate an app/admin permission link for this flow.

```bash
bash skills/feishu-approval/scripts/approval_list_initiated.sh
```

Optional filters:

```bash
bash skills/feishu-approval/scripts/approval_list_initiated.sh \
  --definition-code <definition_code> \
  --page-size 20 \
  --page-limit 5
```

Summarize the result by approval name, group, status, key summaries, instance code, and timestamps when available. The script normalizes list output to `items[]` and includes `status_label`; do not display raw numeric status codes when a label is present.

## Get Approval Instance Details

Uses the grouped user approval OAuth bundle when auth is needed.

```bash
bash skills/feishu-approval/scripts/approval_get_instance.sh --instance-code <instance_code>
```

Use details to inspect the original form payload before preparing a resubmission.

For resubmission identity fields, use bot identity and internal user IDs:

```bash
bash skills/feishu-approval/scripts/approval_get_instance.sh --instance-code <instance_code> --as bot --user-id-type user_id
```

Use the returned `user_id`, `open_id`, and `department_id` in the create payload. Do not use an `od-...` open_department_id as `department_id`.

## List Current User Approval Tasks

Uses the grouped user approval OAuth bundle when auth is needed.

```bash
bash skills/feishu-approval/scripts/approval_list_tasks.sh --topic todo
```

Topics: `todo`, `done`, `initiated`, `unread-cc`, `read-cc`.

Use task list fields `task_id`, `instance_code`, `title`, `initiator_name`, `summaries`, and `support_api_operate` to identify the target task before any write action.

## List Approval Definitions Visible To Current User

Use this only for full visible approval catalog requests. It uses the grouped user approval OAuth bundle, which includes `serviceaccount:approval:approvals:read`:

```bash
bash skills/feishu-approval/scripts/approval_list_definitions.sh
```

Do not generate an app/admin permission link for this flow.

## Approve Or Reject A Task

Uses the grouped user approval OAuth bundle when auth is needed.

Ask the user to confirm the exact task and action first, then run one of:

```bash
bash skills/feishu-approval/scripts/approval_task_decide.sh \
  --action approve \
  --instance-code <instance_code> \
  --task-id <task_id> \
  --comment "同意" \
  --yes

bash skills/feishu-approval/scripts/approval_task_decide.sh \
  --action reject \
  --instance-code <instance_code> \
  --task-id <task_id> \
  --comment "拒绝原因" \
  --yes
```

Do not approve or reject without `--yes`; the script intentionally returns `confirmation-required`.

## Recall Or Cancel An Approval Instance

Uses the grouped user approval OAuth bundle when auth is needed.

Ask the user to confirm the exact instance first, then run:

```bash
bash skills/feishu-approval/scripts/approval_cancel_instance.sh \
  --approval-code <approval_code> \
  --instance-code <instance_code> \
  --yes
```

Pass `--open-id <ou_xxx>` only when it is the current user's open_id. Do not call raw `/approval/v4/instances/cancel`; the script supplies `user_id_type=open_id` and the required body shape.

## Add Approval Instance Comment

Uses app/bot identity with app/tenant scope `approval:instance.comment`.

If you already have the current comment operator `open_id`, pass `--open-id` to skip user OAuth discovery. Otherwise the helper will request user auth to obtain it. Ask the user to confirm the exact instance and comment text first, then run:

```bash
bash skills/feishu-approval/scripts/approval_comment_instance.sh \
  --instance-code <instance_code> \
  --comment "测试使用，不用处理" \
  --yes
```

Pass `--open-id <ou_xxx>` only when it is the current comment operator's open_id. The script sends `user_id_type=open_id` and wraps the comment as Feishu's required `content` JSON string. Do not reuse an arbitrary initiator open_id, and do not use `user_id_type=user_id` for comments unless an API error explicitly requires it, because that path needs `contact:user.employee_id:readonly`.

## Get Approval Definition Schema

Use this before constructing a brand-new payload, when form options may have changed, or after Feishu returns parameter errors such as `1390001` or `1395001`.

Uses app/tenant scope `approval:approval:readonly`. If missing, the helper returns a `token_type=tenant` link for the grouped app approval bundle: `approval:approval:readonly` and `approval:instance`.

```bash
bash skills/feishu-approval/scripts/approval_get_definition.sh --approval-code <approval_code>
```

Read the returned `form` and `node_list`; do not guess widget IDs, option labels, or self-selected approver node keys.

If this command returns a permission link, stop and send it to the user/admin before creating or submitting a payload. Do not fall back to old instance data for changed select/radio/checkbox fields.

For amount controls, use a JSON number and check recent instance detail `ext.minValue`/`ext.maxValue` when present. Out-of-range amounts can surface as a generic Feishu `1395001`.

## Submit A Prepared Approval Payload

Uses app/tenant scope `approval:instance`. Schema-safe payload construction also needs app/tenant `approval:approval:readonly`; permission recovery uses the grouped app approval bundle.

Create a relative payload file under `tmp/`, ask the user to confirm every business field, then run:

```bash
bash skills/feishu-approval/scripts/approval_submit_payload.sh --payload tmp/create_approval.json --yes
```

Prefer internal submitter `user_id`, `open_id`, and `department_id` from `approval_get_instance.sh --as bot --user-id-type user_id`. Keep `open_id` for preview, but do not place an `ou_...` value into `user_id`. The script runs a preview request before creating the instance.

If the command returns missing app scopes, stop and show the missing scopes. Do not request user OAuth. For approval submission or schema-safe payload construction, use the permission-link helper instead of asking for app permissions one by one. Avoid broad `approval:approval` unless the API explicitly requires it.

If the command returns `invalid-payload` or `form-parameter-error`, stop after reporting the reason and log ID. Do not rewrite the payload repeatedly, do not call raw `lark_cli_run.sh api POST`, and do not web search for form examples. For generic `1395001` on payloads with amount controls, check the amount min/max range before changing value formats.

## Generate App/Tenant Permission Links

Use these commands only for app/admin permission recovery. User OAuth scopes are requested separately by auth links.

```bash
bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose app
bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose comment
bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose all
```

- `app`: grouped app approval bundle for definition reads and submissions.
- `comment`: approval comment permission only.
- `all`: app bundle plus comment permission; use only when the user explicitly wants all app-side approval actions approved together.

Send the generated `console_url` and scope list to the user/admin. These links request tenant app scopes with `token_type=tenant`.
