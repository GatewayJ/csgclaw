# Schema And Raw OpenAPI

## Prefer Schema First

Before raw OpenAPI, try:

```bash
bash scripts/lark_cli_run.sh schema <service.resource.method>
```

Use high-level `lark-cli` commands when available. Raw API is for gaps in high-level command coverage.

## Raw API Checklist

Before running `bash scripts/lark_cli_run.sh api`, confirm:

1. Official OpenAPI method and path.
2. Token identity: `--as bot` or `--as user`.
3. Query params JSON for `--params`.
4. Request body JSON for `--data`.
5. Pagination handling: `--page-all` or explicit `page_token`.
6. User confirmation for write operations.

Generic form:

```bash
bash scripts/lark_cli_run.sh api <METHOD> <PATH> \
  --as bot|user \
  --params '<json>' \
  --data '<json>'
```

## Pagination

Use `--page-all` only when the result size is reasonable. For large tenants, explicitly page with `page_size` and `page_token`, summarize progress, and avoid unbounded pulls.

## Writes

For POST/PATCH/PUT/DELETE or any command that sends messages, creates resources, edits documents, or changes business state:

1. Explain the exact target and payload.
2. Ask for explicit confirmation.
3. Run the command once.
4. Preserve `log_id` and response IDs in the final summary.

Do not treat raw API as a permission bypass. It still requires the correct bot app scopes or user OAuth scopes.

For native Feishu approval workflows inside OpenClaw, use `skills/feishu-approval` instead of raw API calls unless that skill is unavailable.
