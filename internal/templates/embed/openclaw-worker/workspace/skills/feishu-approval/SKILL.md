---
name: feishu-approval
description: Query Feishu/Lark approval records, details, user task lists, approval definitions, and prepare or execute guarded approval submissions, recalls/cancellations, comments, and approve/reject task actions from an OpenClaw worker with identity-aware permission recovery. Use for 查询我发起过的审批, 已发起审批记录, 审批详情, 待审批任务, 同意审批, 拒绝审批, 撤回审批, 取消审批, 审批评论, 重新提交审批, 准备或确认后发起审批, approval instances, approval tasks, approval comments, and deciding whether an action needs user OAuth or app/admin approval.
---

# Feishu Approval

## Goal

Use this skill for Feishu/Lark approval workflows. Keep identity clear, avoid unnecessary authorization rounds, and route every operation through the bundled scripts.

This skill depends on the local `lark-cli` skill at `skills/lark-cli`.

## Quick Decision Path

Always invoke bundled scripts with `bash ...` in OpenClaw workers. Do not run script paths directly because copied skills may lose executable bits. Do not manually override `LARKSUITE_CLI_CONFIG_DIR`; the `lark-cli` helper normalizes OpenClaw config automatically.

For exact command syntax, read `references/commands.md`. For user OAuth recovery, read `references/oauth-recovery.md`. For permission boundaries, read `references/minimal-permissions.md`. For new submissions and resubmissions, read `references/submission.md` before building payloads.

```text
approval user intent
        |
        v
choose the matching bundled approval script
        |
        +-- result JSON ---------------------> summarize, preview, or continue
        |
        +-- already user-ready -------------> run original command; do not run auth_complete
        |
        +-- oauth-pending / still running ---> follow oauth-recovery.md; do not start raw auth
        |
        +-- permission-link -----------------> send console_url and scopes -> wait admin approval -> rerun original
        |
        +-- not-installed -------------------> report missing lark-cli skill, lark-cli runner, or Node/npm/npx
        |
        +-- not-configured ------------------> follow OpenClaw binding recovery; do not ask for App ID/App Secret
```

| User intent | Path | Identity |
| --- | --- | --- |
| Query approvals the user initiated | List initiated approvals | user OAuth |
| View approval instance details | Get instance details | user OAuth for read-only user requests |
| Query current user's approval tasks | List tasks | user OAuth |
| Approve or reject a task | Identify task, confirm action, decide task | user OAuth |
| Recall/cancel the user's own approval | Identify instance, confirm action, cancel instance | user OAuth |
| Add an approval comment | Identify instance, confirm comment, comment instance | app/bot with current operator `open_id` |
| List approvals visible/launchable to the user | List definitions catalog only when explicitly requested | user OAuth |
| Prepare a new approval or resubmission | Read current definition schema, build payload, preview, submit | app/bot |
| Recover missing app/tenant approval permission | Generate grouped Open Platform tenant permission link | app/admin approval |

## Identity Rules

- User identity operations use the grouped `lark-cli` OAuth link: initiated/detail reads, task list, approve/reject, recall/cancel, and the current user's visible approval definition catalog.
- App identity operations use Open Platform console links with `token_type=tenant`: definition schema reads, new approval instance creation, and approval comments.
- New approval submission is an app/bot action. User OAuth cannot submit `POST /approval/v4/instances`.
- For resubmission or "参考我上次那个审批再提交" flows, prefer a known previous instance or the user's initiated approvals before listing the full definition catalog.
- For "我曾经发起过哪些审批" or similar, run the initiated-list path first. Do not ask an extra "同意授权" question after the user has already asked to query approvals.

## Write Safety

- Ask for explicit user confirmation before approve/reject, recall/cancel, comment, or submit operations.
- Use the scripts' `--yes` gates only after confirmation. Do not bypass them with raw API calls.
- Before new approval creation, fetch the current approval definition and use the current widget IDs and option `value` fields.
- The submit script runs `/approval/v4/instances/preview` before creating the real approval instance. If preview fails, report the failure and stop.

## Stop Conditions

- If a script returns `oauth-pending`, a command is still running, or OpenClaw app binding is missing, follow `references/oauth-recovery.md`.
- If a script returns `not-installed`, stop and report the missing local dependency from the script output. Do not start OAuth or permission-link recovery until the `lark-cli` skill/runner or Node/npm/npx fallback is available.
- If a script returns `permission-link`, send the `console_url`, scope list, and missing-scope reason. Do not keep retrying API calls.
- If app/admin approval is required, stop until the user confirms the app administrator has approved the requested tenant scopes.
- If `approval_submit_payload.sh` returns `invalid-payload` or `form-parameter-error` with `retry: stop`, stop. Do not rewrite `tmp/create_approval.json` repeatedly, do not call raw `lark_cli_run.sh api POST`, and do not web search. Report the script reason and Feishu log ID, then fetch or refresh the definition schema before a later retry.
- If `approval_comment_instance.sh` returns `missing-commenter-open-id`, complete user OAuth or pass the current comment operator's `open_id`. Do not use an arbitrary instance initiator `open_id`, and do not retry raw comment API shapes.
- If the reason is `invalid_submitter_id`, fetch the instance with `--as bot --user-id-type user_id` and rebuild once using the returned internal `user_id`, `open_id`, and `department_id`. Do not put an `ou_...` open_id into `user_id`.
- If Feishu returns `1395001` for a payload containing `amount`, check the amount field range from recent instance detail `ext.minValue`/`ext.maxValue` before changing formats. Do not convert numeric amounts to strings to guess around the error.
- Do not copy `department_id` from user-scoped old instance details when it starts with `od-`; that is an open_department_id and create will reject it. Use the `department_id` returned by bot instance detail with `--user-id-type user_id`.

## Recovery Rules

- `99991679` with `action_privilege_required`: missing app/tenant approval permission. Do not loop OAuth. Generate the grouped app approval tenant link, preserve the raw missing subjects/log ID, and ask for admin approval before retrying.
- `99991672` or `app_scope_not_applied`: app has not applied for required app/tenant scopes. Stop and show the grouped app approval tenant link. Do not add broad `approval:approval` unless the API error explicitly requires it and the app administrator accepts that wider scope.
- For approval comment permission errors, generate the comment permission link, not the submission/schema app bundle.
- `99991668` or `user access token not support` on create instance: the API does not support user token for that write. Retry as bot only if app scopes are already approved.
- Web search is not part of this workflow. Use `lark-cli schema approval` and the bundled scripts.

## References

- `references/commands.md`: exact commands for common approval tasks.
- `references/oauth-recovery.md`: OAuth, pending command, and OpenClaw binding recovery.
- `references/minimal-permissions.md`: minimal scopes and what not to request.
- `references/submission.md`: safe resubmission and new approval creation workflow.
