# Submission Workflow

## Resubmit From A Previous Approval

1. Get a previous initiated instance for business fields:

```bash
bash skills/feishu-approval/scripts/approval_get_instance.sh --instance-code <instance_code>
```

For submission identity fields, fetch the same instance with bot identity and internal user IDs:

```bash
bash skills/feishu-approval/scripts/approval_get_instance.sh --instance-code <instance_code> --as bot --user-id-type user_id
```

If the user is creating a brand-new approval rather than resubmitting an existing one, or if the old instance form no longer matches the current approval definition, fetch the definition first:

```bash
bash skills/feishu-approval/scripts/approval_get_definition.sh --approval-code <approval_code>
```

For all submissions, fetch the current definition before building the payload. If definition fetch returns `permission-link` or missing app/admin schema scopes, stop and ask for that approval first. Do not continue with stale option values from the old instance.

2. Reuse only these fields from the previous detail:

- `definition_code`
- business values from the previous `form`
- internal submitter `user_id`, submitter `open_id`, and `department_id` from the bot detail call with `--user-id-type user_id`

Keep both `user_id` and `open_id` in the payload when you have them: the submit script uses `open_id` for preview and internal `user_id` for create. Do not put an `ou_...` value into `user_id`. If the previous detail only has `open_id` and an `od-...` department, fetch bot detail with `--user-id-type user_id` before creating the payload.

Do not reuse an `od-...` department ID from user-scoped previous details. Create requires department_id type, for example the value returned by bot instance detail with `--user-id-type user_id`.

3. Modify only the fields the user explicitly requested. Do not assume a fixed business schema; use the field names and control types from the current approval definition.

Validate each field against the returned current definition `form`:

- use current widget IDs, not labels from memory;
- use current option values/keys for radio and checkbox controls;
- for `checkboxV2` and `radioV2`, use the option `value` from the definition in the submitted `value`; do not copy display text from returned instance details;
- for `amount`, include numeric `value` and include `currency` when the user specified a currency;
- validate amount ranges from recent instance detail `ext.minValue` and `ext.maxValue` when available. Do not change numeric amounts into strings to work around range errors.
- include self-selected approver or CC node keys from `node_list` only when `need_approver` or the node configuration requires them;
- do not submit if required fields are missing or unsupported controls are present.

If the approval contains select, radio, or checkbox controls and the current definition is unavailable, do not change those fields. Ask for app/admin schema permission instead of guessing current option keys or labels.

4. Write the payload to a relative path under `tmp/`. Do not use absolute paths because `lark-cli` rejects unsafe file paths.

5. Before submission, show the user a concise confirmation summary:

```text
将提交：
审批：...
发起人：...
字段：
<字段名>：<值>
<字段名>：<值>

回复“确认提交”后我再发起。
```

6. Submit after explicit confirmation:

```bash
bash skills/feishu-approval/scripts/approval_submit_payload.sh --payload tmp/create_approval.json --yes
```

The submit script first previews through `/approval/v4/instances/preview` using `open_id` when available. Only after preview succeeds does it create the real instance through `/approval/v4/instances` without adding a `user_id_type` query parameter.

To validate a payload without creating an instance, use `--preview-only` with the same command.

If the submit script returns `invalid-payload` or `form-parameter-error`, report the reason and stop. Do not rewrite the payload more than once in the same turn, do not call raw `lark_cli_run.sh`, and do not web search for alternate payload examples. The next step is current definition/schema access or human admin confirmation, not trial-and-error API calls.

## Important Boundary

Feishu approval instance creation is a bot/app API. In observed OpenClaw runs:

- `--as user` returned `99991668 user access token not support`.
- `--as bot` requires app/admin approval. Use the grouped app approval bundle: `approval:approval:readonly` and `approval:instance`.

User-scoped record reads use the grouped user approval OAuth bundle, but creating a new approval cannot be completed with only user OAuth. On app/admin permission errors, run:

```bash
bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose app
```

Use the generated link to request the app approval bundle in one pass. Avoid broad `approval:approval` unless the Feishu API error explicitly requires it and the app administrator approves that wider scope.
