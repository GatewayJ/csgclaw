#!/usr/bin/env python3
import json
import sys


STATUS_LABELS = {
    1: "审批中",
    2: "已通过",
    3: "已拒绝",
    4: "已撤回",
    5: "已删除",
}

OPTIONAL_INSTANCE_FIELDS = (
    "start_time",
    "end_time",
    "update_time",
    "create_time",
    "submit_time",
)


def load_mixed_json(text: str):
    start = text.find("{")
    if start < 0:
        raise ValueError("no JSON object found")
    return json.loads(text[start:])


def summary_map(instance):
    values = {}
    for item in instance.get("summaries") or []:
        key = item.get("key")
        if key:
            values[key] = item.get("value", "")
    return values


def normalize_instance(instance):
    status = instance.get("instance_status")
    item = {
        "definition_name": instance.get("definition_name", ""),
        "definition_group_name": instance.get("definition_group_name", ""),
        "definition_code": instance.get("definition_code", ""),
        "instance_code": instance.get("instance_code", ""),
        "status": status,
        "status_label": STATUS_LABELS.get(status, f"未知({status})"),
        "initiator_name": instance.get("initiator_name", ""),
        "summaries": summary_map(instance),
    }
    for field in OPTIONAL_INSTANCE_FIELDS:
        value = instance.get(field)
        if value not in (None, ""):
            item[field] = value
    return item


raw = sys.stdin.read()
try:
    data = load_mixed_json(raw)
except Exception:
    sys.stdout.write(raw)
    sys.exit(0)

payload = data.get("data") or {}
items = []
for instance in payload.get("instances") or []:
    items.append(normalize_instance(instance))

print(
    json.dumps(
        {
            "ok": data.get("ok", False),
            "identity": data.get("identity", ""),
            "count": payload.get("count", len(items)),
            "has_more": payload.get("has_more", False),
            "page_token": payload.get("page_token", ""),
            "items": items,
        },
        ensure_ascii=False,
        indent=2,
    )
)
