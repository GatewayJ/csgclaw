# Approval API Notes

## Common Identity Choice

Use `--as user` when the task depends on the current user's approval history, details, task inbox, task decision, or approval definition catalog visible to that user.

Use `--as bot` when reading approval definition schemas or creating instances through an app-owned workflow.

Inside OpenClaw workers, prefer `skills/feishu-approval` for native approval history, details, submission, recall/cancel, comments, and task decisions. The generic `lark-cli` skill is a fallback for unsupported approval APIs only.
In OpenClaw, do not call raw approval write paths for recall/cancel, comments, or task decisions through `lark_cli_run.sh` unless you are explicitly in a debugging fallback.

## List Approval Definitions

In OpenClaw workers, prefer `bash skills/feishu-approval/scripts/approval_get_definition.sh --approval-code <code>` when you already know the approval code. Do not list all definitions just to answer a normal submitter workflow.

In OpenClaw workers, prefer `bash skills/feishu-approval/scripts/approval_list_definitions.sh` for the current user's visible approval catalog. If the helper is unavailable and OAuth scope `serviceaccount:approval:approvals:read` is authorized, the raw API shape is:

```bash
bash scripts/lark_cli_run.sh api GET /open-apis/approval/v4/approvals \
  --as user \
  --params '{"page_size":100,"locale":"zh-CN"}' \
  --page-all
```

## Query Instances By Approval Code

```bash
bash scripts/lark_cli_run.sh api POST /open-apis/approval/v4/instances/query \
  --as bot \
  --params '{"page_size":20,"user_id_type":"open_id"}' \
  --data '{"approval_code":"<approval_code>","instance_status":"ALL","locale":"zh-CN"}'
```

## Recovery

Typical missing scope examples:

```json
{"missing_scopes":["approval:instance:read"]}
```

If the scope is user-scoped, start OAuth with that scope. If it is app/admin-scoped, stop retries and ask the user to have an administrator enable the permission in the developer console.

`approval:approval.list:readonly` is broader tenant-side approval query access. Use it only when a specific tenant query API explicitly requires it and the user/admin accepts that broader scope; do not suggest it for normal submitter workflows.

If the API returns Feishu code `99991679` with `permission_violations` such as `approval:approval`, `approval:approval:readonly`, or `approval:definition` and `type: "action_privilege_required"`, do not start another OAuth login. Explain that the current app lacks approved app/tenant approval permissions, share the missing subjects, and ask for Feishu/Lark Open Platform admin approval before retrying. In OpenClaw workers, run `bash skills/feishu-approval/scripts/approval_permission_link.sh --purpose app` to generate the grouped app approval tenant link.

For approval instance creation, always confirm the approval definition, applicant, form payload, and whether submission will notify approvers before running the API. Use the grouped tenant app approval bundle for app-side recovery: `approval:approval:readonly` and `approval:instance`. Use grouped user OAuth for user-scoped history/detail/task operations and for the current user's visible approval definition catalog. Avoid broad `approval:approval` unless the API error explicitly requires it.
