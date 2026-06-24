#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=approval_common.sh
. "$SCRIPT_DIR/approval_common.sh"

payload=""
yes="false"
user_id_type="auto"
skip_preview="false"
preview_only="false"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --payload) payload="${2:?}"; shift 2 ;;
    --yes) yes="true"; shift ;;
    --user-id-type) user_id_type="${2:?}"; shift 2 ;;
    --skip-preview) skip_preview="true"; shift ;;
    --preview-only) preview_only="true"; shift ;;
    -h|--help)
      printf '%s\n' "Usage: $0 --payload tmp/create_approval.json --yes [--user-id-type auto|open_id|user_id] [--skip-preview] [--preview-only]"
      exit 0
      ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [ "$yes" != "true" ]; then
  printf '{"status":"confirmation-required","hint":"Ask the user to confirm exact approval fields, then rerun with --yes"}\n'
  exit 2
fi

if [ -z "$payload" ]; then
  printf 'Missing --payload\n' >&2
  exit 2
fi

case "$payload" in
  /*|../*|*/../*)
    printf '{"status":"invalid-payload-path","hint":"Use a relative path inside the current workspace, for example tmp/create_approval.json"}\n'
    exit 2
    ;;
esac

if [ ! -f "$payload" ]; then
  printf 'Payload file not found: %s\n' "$payload" >&2
  exit 2
fi

if [ "$preview_only" = "true" ] && [ "$skip_preview" = "true" ]; then
  printf '{"status":"invalid-arguments","hint":"--preview-only cannot be combined with --skip-preview."}\n'
  exit 2
fi

case "$user_id_type" in
  auto|open_id|user_id) ;;
  *)
    printf '{"status":"invalid-user-id-type","hint":"Use auto, open_id, or user_id. The create-instance API does not accept union_id as the submitter."}\n'
    exit 2
    ;;
esac

preview_payload="$payload"
create_payload="$payload"
tmp_preview_payload=""
tmp_create_payload=""
preview_user_id_type="$user_id_type"
effective_user_id_type="$user_id_type"
cleanup() {
  if [ -n "$tmp_preview_payload" ] && [ -f "$tmp_preview_payload" ]; then
    rm -f "$tmp_preview_payload"
  fi
  if [ -n "$tmp_create_payload" ] && [ -f "$tmp_create_payload" ]; then
    rm -f "$tmp_create_payload"
  fi
}
trap cleanup EXIT

if ! command -v node >/dev/null 2>&1; then
  printf '{"status":"node-required","hint":"approval_submit_payload.sh requires Node.js to normalize preview/create payloads safely."}\n'
  exit 2
fi

payload_dir="$(dirname -- "$payload")"
if [ "$payload_dir" = "." ]; then
  tmp_preview_payload=".approval-preview.$$.$RANDOM.json"
  tmp_create_payload=".approval-create.$$.$RANDOM.json"
else
  mkdir -p "$payload_dir"
  tmp_preview_payload="$payload_dir/.approval-preview.$$.$RANDOM.json"
  tmp_create_payload="$payload_dir/.approval-create.$$.$RANDOM.json"
fi
set +e
validation="$(
  node - "$payload" "$user_id_type" "$tmp_preview_payload" "$tmp_create_payload" <<'NODE'
const fs = require("fs");

const file = process.argv[2];
const requestedUserIdType = process.argv[3];
const previewFile = process.argv[4];
const createFile = process.argv[5];

function fail(reason, hint) {
  console.log(JSON.stringify({ status: "invalid-payload", reason, hint }));
  process.exit(2);
}

let payload;
try {
  payload = JSON.parse(fs.readFileSync(file, "utf8"));
} catch (err) {
  fail("payload_json_parse_failed", `Payload file must be valid JSON: ${err.message}`);
}

if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
  fail("payload_must_be_object", "Payload must be a JSON object accepted by POST /approval/v4/instances.");
}

if (payload.department_id && typeof payload.department_id !== "string") {
  fail("department_id_must_be_string", "department_id must be a department_id string returned by Feishu, not an object or array.");
}

if (payload.department_id && payload.department_id.startsWith("od-")) {
  fail("open_department_id_not_supported", "Create approval instance requires department_id, not open_department_id. Fetch the previous instance with --as bot --user-id-type user_id and use its department_id.");
}

if (typeof payload.approval_code !== "string" || payload.approval_code.length === 0) {
  fail("missing_approval_code", "Payload must include approval_code.");
}

if (payload.open_id && typeof payload.open_id !== "string") {
  fail("open_id_must_be_string", "open_id must be a string.");
}

if (payload.user_id && typeof payload.user_id !== "string") {
  fail("user_id_must_be_string", "user_id must be a string.");
}

if (!payload.open_id && !payload.user_id) {
  fail("missing_submitter", "Payload must include open_id or user_id for the initiator.");
}

function inferUserIdType() {
  if (requestedUserIdType !== "auto") return requestedUserIdType;
  if (payload.user_id && !payload.user_id.startsWith("ou_")) return "user_id";
  if (payload.open_id || (payload.user_id && payload.user_id.startsWith("ou_"))) return "open_id";
  return "user_id";
}

if (typeof payload.form !== "string") {
  fail("form_must_be_string", "Feishu create-instance API requires form to be a JSON-array string, not an array/object.");
}

try {
  const form = JSON.parse(payload.form);
  if (!Array.isArray(form)) {
    fail("form_string_must_encode_array", "Payload form string must encode a JSON array of approval controls.");
  }
} catch (err) {
  fail("form_string_parse_failed", `Payload form string is not valid JSON: ${err.message}`);
}

const effectiveUserIdType = inferUserIdType();
let preview = { ...payload };
let create = { ...payload };
delete preview.department_id;

if (effectiveUserIdType === "open_id") {
  const openId = payload.open_id || payload.user_id;
  if (!openId || !openId.startsWith("ou_")) {
    fail("invalid_open_id_submitter", "For --user-id-type open_id, submitter must be an ou_... open_id.");
  }
  preview.user_id = openId;
  delete preview.open_id;
  create.open_id = openId;
  delete create.user_id;
} else if (effectiveUserIdType === "user_id") {
  const userId = payload.user_id;
  if (!userId || userId.startsWith("ou_")) {
    fail("invalid_user_id_submitter", "For --user-id-type user_id, use the internal Feishu user_id, not an ou_... open_id. Fetch instance details with approval_get_instance.sh --as bot --user-id-type user_id.");
  }
  if (!payload.open_id || !payload.open_id.startsWith("ou_")) {
    fail("missing_open_id_for_preview", "When creating with internal user_id, keep the submitter open_id in the payload too. The preview API validates open_id even though create uses user_id.");
  }
  preview.user_id = payload.open_id;
  delete preview.open_id;
  create.user_id = userId;
  delete create.open_id;
} else {
  fail("unsupported_submitter_id_type", "Create approval instance supports open_id or user_id submitters.");
}

try {
  fs.writeFileSync(previewFile, `${JSON.stringify(preview, null, 2)}\n`);
  fs.writeFileSync(createFile, `${JSON.stringify(create, null, 2)}\n`);
} catch (err) {
  fail("normalized_payload_write_failed", `Could not write normalized payloads: ${err.message}`);
}

const previewUserIdType = effectiveUserIdType === "user_id" ? "open_id" : effectiveUserIdType;
console.log(`${previewUserIdType} ${effectiveUserIdType}`);
NODE
)"
validation_rc=$?
set -e
if [ "$validation_rc" -ne 0 ]; then
  printf '%s\n' "$validation"
  exit "$validation_rc"
fi
validation_success="$(printf '%s' "$validation" | tail -n 1)"
preview_user_id_type="${validation_success%% *}"
effective_user_id_type="${validation_success##* }"
preview_payload="$tmp_preview_payload"
create_payload="$tmp_create_payload"

bound_output="$(approval_bootstrap bot 2>&1)" || {
  printf '%s\n' "$bound_output"
  exit 1
}

approval_handle_submit_failure() {
  local failure_output="$1"

  case "$failure_output" in
    *"99991672"*|*"99991679"*|*"missing_scopes"*|*"permission_violations"*|*"app_scope_not_applied"*|*"authorization"*)
      approval_run_script "$SCRIPT_DIR/approval_permission_link.sh" --purpose app || true
      ;;
  esac
  case "$failure_output" in
    *"部门ID不正确"*)
      printf '%s\n' '{"status":"form-parameter-error","retry":"stop","reason":"invalid_department_id","hint":"Create approval instance requires department_id, not open_department_id. Fetch instance details with approval_get_instance.sh --as bot --user-id-type user_id, then use the returned department_id."}'
      ;;
    *"用户ID不正确"*)
      printf '%s\n' '{"status":"form-parameter-error","retry":"stop","reason":"invalid_submitter_id","hint":"Do not keep retrying ID variants. For resubmission, fetch instance details with approval_get_instance.sh --as bot --user-id-type user_id and use the returned user_id, open_id, and department_id."}'
      ;;
    *"field validation failed"*)
      printf '%s\n' '{"status":"form-parameter-error","retry":"stop","reason":"submitter_field_validation_failed","hint":"Stop and report the raw error/log_id. Do not call raw lark_cli_run.sh to work around submitter fields."}'
      ;;
    *"Invalid parameter type in json: form"*|*"form_must_be_string"*)
      printf '%s\n' '{"status":"form-parameter-error","retry":"stop","reason":"form_must_be_json_string","hint":"The form field must be a JSON-array string. Fix this locally once; do not call raw lark-cli API."}'
      ;;
    *"控件的值不存在复选框的选项中"*|*"1390001"*|*"1395001"*|*"60022"*)
      printf '%s\n' '{"status":"form-parameter-error","retry":"stop","reason":"approval_form_schema_mismatch","hint":"Stop rewriting payload variants. Fetch the approval definition with approval_get_definition.sh, use current option value fields for radio/checkbox controls, and for amount controls check recent instance ext.minValue/ext.maxValue because out-of-range amounts can return generic 1395001."}'
      ;;
  esac
}

if [ "$skip_preview" != "true" ]; then
  set +e
  preview_output="$(approval_run_lark api POST "/open-apis/approval/v4/instances/preview?user_id_type=$preview_user_id_type" \
    --as bot \
    --data "@$preview_payload" \
    --format json 2>&1)"
  preview_rc=$?
  set -e

  if [ "$preview_rc" -ne 0 ]; then
    printf '%s\n' "$preview_output"
    approval_handle_submit_failure "$preview_output"
    exit "$preview_rc"
  fi

  if [ "$preview_only" = "true" ]; then
    printf '%s\n' "$preview_output"
    exit 0
  fi
fi

set +e
output="$(approval_run_lark api POST "/open-apis/approval/v4/instances" \
  --as bot \
  --data "@$create_payload" \
  --format json 2>&1)"
rc=$?
set -e

printf '%s\n' "$output"

if [ "$rc" -ne 0 ]; then
  approval_handle_submit_failure "$output"
  exit "$rc"
fi
