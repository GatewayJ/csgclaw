#!/usr/bin/env python3
"""Feishu/Lark scan-to-create registration helper for CSGClaw.

This script is intentionally project-local to the manager skill. It uses the
same Feishu/Lark accounts registration flow that Hermes' Feishu gateway setup
uses:

  init -> begin -> poll -> receive client_id/client_secret

Secrets are never printed. On finalize, the returned client_secret is written
immediately to CSGClaw through /api/v1/channels/feishu/config/{bot_id}.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import uuid
from pathlib import Path
from typing import Any, Dict, Optional
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urlparse, urlunparse, parse_qsl, quote
from urllib.request import Request, urlopen

ONBOARD_ACCOUNTS_URLS = {
    "feishu": "https://accounts.feishu.cn",
    "lark": "https://accounts.larksuite.com",
}
ONBOARD_OPEN_URLS = {
    "feishu": "https://open.feishu.cn",
    "lark": "https://open.larksuite.com",
}
REGISTRATION_PATH = "/oauth/v1/app/registration"
REQUEST_TIMEOUT = 15
API_REQUEST_TIMEOUT = 600
DEFAULT_EXPIRE_SECONDS = 600


def eprint(*args: Any) -> None:
    print(*args, file=sys.stderr)


def accounts_base_url(domain: str) -> str:
    return ONBOARD_ACCOUNTS_URLS.get(domain, ONBOARD_ACCOUNTS_URLS["feishu"])


def open_base_url(domain: str) -> str:
    return ONBOARD_OPEN_URLS.get(domain, ONBOARD_OPEN_URLS["feishu"])


def validate_bot_id(bot_id: str) -> str:
    bot_id = (bot_id or "").strip()
    if not bot_id:
        raise RuntimeError("--bot-id is required")
    for ch in bot_id:
        if not (ch.isalnum() or ch in "-_"):
            raise RuntimeError(f"invalid bot id {bot_id!r}: only letters, digits, '-' and '_' are allowed")
    return bot_id


def append_launcher_params(url: str, source: str = "csgclaw") -> str:
    parsed = urlparse(url)
    query = dict(parse_qsl(parsed.query, keep_blank_values=True))
    query.setdefault("from", source)
    query.setdefault("tp", source)
    return urlunparse(parsed._replace(query=urlencode(query)))


def post_form(url: str, body: Dict[str, str]) -> dict:
    data = urlencode(body).encode("utf-8")
    req = Request(url, data=data, headers={"Content-Type": "application/x-www-form-urlencoded"})
    try:
        with urlopen(req, timeout=REQUEST_TIMEOUT) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except HTTPError as exc:
        body_bytes = exc.read()
        if body_bytes:
            try:
                return json.loads(body_bytes.decode("utf-8"))
            except (ValueError, json.JSONDecodeError):
                raise exc from None
        raise


def post_registration(domain: str, body: Dict[str, str]) -> dict:
    return post_form(f"{accounts_base_url(domain)}{REGISTRATION_PATH}", body)


def init_registration(domain: str) -> None:
    res = post_registration(domain, {"action": "init"})
    methods = res.get("supported_auth_methods") or []
    if "client_secret" not in methods:
        raise RuntimeError(f"Feishu/Lark registration does not support client_secret auth; supported={methods}")


def begin_registration(domain: str) -> dict:
    res = post_registration(domain, {
        "action": "begin",
        "archetype": "PersonalAgent",
        "auth_method": "client_secret",
        "request_user_info": "open_id",
    })
    device_code = res.get("device_code")
    if not device_code:
        raise RuntimeError("registration begin did not return device_code")
    qr_url = append_launcher_params(res.get("verification_uri_complete", ""), "csgclaw")
    if not qr_url:
        raise RuntimeError("registration begin did not return verification_uri_complete")
    return {
        "device_code": device_code,
        "qr_url": qr_url,
        "user_code": res.get("user_code", ""),
        "interval": int(res.get("interval") or 5),
        "expire_in": int(res.get("expire_in") or DEFAULT_EXPIRE_SECONDS),
    }


def poll_registration_once(domain: str, device_code: str) -> dict:
    return post_registration(domain, {
        "action": "poll",
        "device_code": device_code,
        "tp": "ob_app",
    })


def render_ascii_qr(url: str) -> bool:
    try:
        import qrcode  # type: ignore
    except Exception:
        return False
    try:
        qr = qrcode.QRCode()
        qr.add_data(url)
        qr.make(fit=True)
        qr.print_ascii(invert=True)
        return True
    except Exception:
        return False


def default_state_dir() -> Path:
    override = os.environ.get("CSGCLAW_FEISHU_SETUP_STATE_DIR")
    if override:
        return Path(override).expanduser()
    picoclaw_workspace = Path("~/.picoclaw/workspace").expanduser()
    if picoclaw_workspace.exists() or Path("~/.picoclaw").expanduser().exists():
        return picoclaw_workspace / ".feishu-channel-setup"
    return Path("~/.cache/csgclaw-feishu-channel-setup").expanduser()


def state_dir(args: argparse.Namespace) -> Path:
    return Path(args.state_dir).expanduser() if args.state_dir else default_state_dir()


def state_path(args: argparse.Namespace, registration_id: str) -> Path:
    safe = "".join(ch for ch in registration_id if ch.isalnum() or ch in "-_")
    return state_dir(args) / f"{safe}.json"


def save_state(args: argparse.Namespace, state: dict) -> None:
    directory = state_dir(args)
    directory.mkdir(parents=True, exist_ok=True)
    os.chmod(directory, 0o700)
    path = state_path(args, state["registration_id"])
    tmp = path.with_suffix(".tmp")
    tmp.write_text(json.dumps(state, ensure_ascii=False, indent=2), encoding="utf-8")
    os.chmod(tmp, 0o600)
    tmp.replace(path)


def load_state(args: argparse.Namespace) -> dict:
    if not args.registration_id:
        raise SystemExit("--registration-id is required")
    path = state_path(args, args.registration_id)
    if not path.exists():
        raise SystemExit(f"registration state not found: {path}")
    return json.loads(path.read_text(encoding="utf-8"))


def delete_state(args: argparse.Namespace, registration_id: str) -> None:
    path = state_path(args, registration_id)
    try:
        path.unlink()
    except FileNotFoundError:
        pass


def api_base(args: argparse.Namespace) -> str:
    return (args.csgclaw_base_url or os.environ.get("CSGCLAW_BASE_URL") or "http://127.0.0.1:18080").rstrip("/")


def api_token(args: argparse.Namespace) -> str:
    return getattr(args, "csgclaw_access_token", "") or os.environ.get("CSGCLAW_ACCESS_TOKEN", "")


def api_request_timeout(args: argparse.Namespace) -> int:
    value = getattr(args, "api_timeout", None)
    if value is None:
        raw = os.environ.get("CSGCLAW_API_TIMEOUT", "").strip()
        if raw:
            try:
                value = int(raw)
            except ValueError:
                value = API_REQUEST_TIMEOUT
        else:
            value = API_REQUEST_TIMEOUT
    return max(1, int(value))


def api_json(args: argparse.Namespace, method: str, path: str, body: Optional[dict] = None) -> Any:
    data = None if body is None else json.dumps(body).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    token = api_token(args)
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = Request(f"{api_base(args)}{path}", data=data, headers=headers, method=method)
    try:
        with urlopen(req, timeout=api_request_timeout(args)) as resp:
            raw = resp.read().decode("utf-8")
            return json.loads(raw) if raw else None
    except HTTPError as exc:
        raw = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"CSGClaw API {method} {path} failed: HTTP {exc.code}: {raw.strip()}") from None


def configure_csgclaw(args: argparse.Namespace, state: dict, result: dict) -> dict:
    bot_id = state["bot_id"]
    path_bot_id = quote(bot_id, safe="")
    existing = api_json(args, "GET", f"/api/v1/channels/feishu/config/{path_bot_id}", None) or {}
    existing_admin_open_id = str(existing.get("admin_open_id") or "").strip()
    candidate_admin_open_id = str(state.get("admin_open_id") or result.get("open_id") or "").strip()
    payload = {
        "app_id": result["app_id"],
        "app_secret": result["app_secret"],
        "reload": True,
    }
    if not existing_admin_open_id and candidate_admin_open_id:
        payload["admin_open_id"] = candidate_admin_open_id
    response = api_json(args, "PUT", f"/api/v1/channels/feishu/config/{path_bot_id}", payload) or {}
    if existing_admin_open_id and not response.get("admin_open_id"):
        response["admin_open_id"] = existing_admin_open_id
        response["admin_open_id_preserved"] = True
    elif payload.get("admin_open_id"):
        response["admin_open_id_source"] = "registration"
    return response


def resolve_role(args: argparse.Namespace, state: dict) -> str:
    bot_id = state["bot_id"]
    return args.role or state.get("role") or ("manager" if bot_id == "u-manager" else "worker")


def ensure_bot(args: argparse.Namespace, state: dict, result: dict) -> Optional[dict]:
    if args.no_ensure_bot:
        return None
    bot_id = state["bot_id"]
    name = args.bot_name or state.get("bot_name") or bot_id.removeprefix("u-") or bot_id
    role = resolve_role(args, state)
    description = args.description or state.get("description") or f"{name} Feishu {role} agent"
    payload = {
        "id": bot_id,
        "name": name,
        "description": description,
        "role": role,
        "channel": "feishu",
    }
    return api_json(args, "POST", "/api/v1/bots", payload)


def worker_box_conflict_message(bot_id: str, name: str) -> str:
    return (
        f"worker {bot_id!r} could not be created because a residual BoxLite box named {name!r} already exists, "
        f"but CSGClaw has no matching agent record. Stop here and ask the host operator to clean the stale worker "
        f"runtime, for example: ./bin/boxlite --home ~/.csgclaw/agents/{name}/boxlite rm -f {name}"
    )


def is_box_name_conflict(exc: RuntimeError, name: str) -> bool:
    message = str(exc)
    return "box with name" in message and f"'{name}' already exists" in message


def agent_exists(args: argparse.Namespace, bot_id: str) -> bool:
    try:
        api_json(args, "GET", f"/api/v1/agents/{quote(bot_id, safe='')}", None)
        return True
    except RuntimeError as exc:
        message = str(exc)
        if "HTTP 404" in message and "agent not found" in message:
            return False
        raise


def maybe_recreate(args: argparse.Namespace, state: dict, worker_existed_before_ensure: Optional[bool] = None) -> Optional[dict]:
    mode = args.recreate
    bot_id = state["bot_id"]
    role = resolve_role(args, state)
    if mode == "none":
        return None
    if role == "manager":
        if mode in ("auto", "worker"):
            return {"skipped": True, "reason": "manager recreate requires explicit final confirmation"}
        if mode == "manager" and not getattr(args, "confirm_manager", False):
            raise RuntimeError("manager recreate can interrupt the current run; pass --confirm-manager as the final confirmed action")
        result = api_json(args, "POST", f"/api/v1/agents/{quote(bot_id, safe='')}/recreate", None)
        return manager_recreate_terminal_result(bot_id, result)
    if mode == "manager":
        return {"skipped": True, "reason": "manager recreate requested for a worker bot"}
    if worker_existed_before_ensure is False:
        if getattr(args, "no_ensure_bot", False):
            return {"skipped": True, "reason": "worker agent does not exist and --no-ensure-bot skipped creation"}
        return {"skipped": True, "reason": "worker agent did not exist before ensure; ensure_bot created it with current config"}
    return api_json(args, "POST", f"/api/v1/agents/{quote(bot_id, safe='')}/recreate", None)


def public_result(data: dict) -> dict:
    clean = dict(data)
    for key in ("app_secret", "client_secret", "access_token", "tenant_access_token"):
        if key in clean:
            clean[key] = "present"
    return clean


def manager_recreate_terminal_result(bot_id: str, result: Optional[dict]) -> dict:
    return {
        "status": "recreate_requested",
        "bot_id": bot_id,
        "terminal": True,
        "post_recreate_status_check": "skip",
        "message": "Manager self-recreate was requested. Stop this manager-hosted run here; do not inspect, start, or retry manager based on runtime status.",
        "result": public_result(result or {}),
    }


def cmd_start(args: argparse.Namespace) -> int:
    bot_id = validate_bot_id(args.bot_id)
    domain = args.domain
    init_registration(domain)
    begin = begin_registration(domain)
    registration_id = str(uuid.uuid4())
    now = int(time.time())
    role = args.role or ("manager" if bot_id == "u-manager" else "worker")
    state = {
        "registration_id": registration_id,
        "bot_id": bot_id,
        "role": role,
        "bot_name": args.bot_name or bot_id.removeprefix("u-") or bot_id,
        "description": args.description or "",
        "admin_open_id": args.admin_open_id or "",
        "domain": domain,
        "device_code": begin["device_code"],
        "qr_url": begin["qr_url"],
        "user_code": begin.get("user_code", ""),
        "interval": begin["interval"],
        "expire_in": begin["expire_in"],
        "created_at": now,
        "expires_at": now + min(begin["expire_in"], args.timeout),
    }
    save_state(args, state)
    output = {
        "registration_id": registration_id,
        "bot_id": bot_id,
        "role": role,
        "qr_url": begin["qr_url"],
        "user_code": begin.get("user_code", ""),
        "interval": begin["interval"],
        "expires_in": min(begin["expire_in"], args.timeout),
        "state_path": str(state_path(args, registration_id)),
        "next": f"python scripts/feishu_register.py finalize --registration-id {registration_id}",
        "next_tool_timeout_seconds": API_REQUEST_TIMEOUT,
    }
    if args.json:
        print(json.dumps(output, ensure_ascii=False, indent=2))
    else:
        print(f"Feishu registration started for {bot_id}.")
        print(f"Registration ID: {registration_id}")
        print()
        if args.qr:
            rendered = render_ascii_qr(begin["qr_url"])
            if rendered:
                print()
        print("Open this URL in Feishu/Lark and confirm bot creation:")
        print(begin["qr_url"])
        print()
        print("After the user confirms, run:")
        print(output["next"])
        print(f"Use a tool timeout of at least {API_REQUEST_TIMEOUT} seconds for finalize when creating worker boxes.")
    return 0


def extract_success(state: dict, res: dict) -> Optional[dict]:
    user_info = res.get("user_info") or {}
    domain = state.get("domain", "feishu")
    if user_info.get("tenant_brand") == "lark":
        domain = "lark"
    if res.get("client_id") and res.get("client_secret"):
        return {
            "app_id": res["client_id"],
            "app_secret": res["client_secret"],
            "domain": domain,
            "open_id": user_info.get("open_id"),
        }
    return None


def poll_until_success(args: argparse.Namespace, state: dict, wait: bool) -> Optional[dict]:
    deadline = min(int(state.get("expires_at", 0)) or (int(time.time()) + args.timeout), int(time.time()) + args.timeout)
    interval = max(1, int(state.get("interval") or 5))
    domain = state.get("domain", "feishu")
    while True:
        try:
            res = poll_registration_once(domain, state["device_code"])
        except (URLError, OSError, json.JSONDecodeError) as exc:
            if not wait:
                raise RuntimeError(f"poll failed: {exc}") from exc
            res = {"error": "temporary_network_error"}
        success = extract_success(state, res)
        if success:
            return success
        error = res.get("error")
        if error in ("access_denied", "expired_token"):
            raise RuntimeError(f"registration failed: {error}")
        if not wait:
            return None
        if time.time() >= deadline:
            raise RuntimeError("registration timed out before user confirmation")
        time.sleep(interval)


def cmd_poll(args: argparse.Namespace) -> int:
    state = load_state(args)
    result = poll_until_success(args, state, wait=False)
    if result:
        print(json.dumps({
            "status": "confirmed",
            "bot_id": state["bot_id"],
            "credentials": "available",
            "next": f"python scripts/feishu_register.py finalize --registration-id {state['registration_id']}",
            "next_tool_timeout_seconds": API_REQUEST_TIMEOUT,
        }, ensure_ascii=False, indent=2))
    else:
        print(json.dumps({"status": "pending", "bot_id": state["bot_id"]}, ensure_ascii=False, indent=2))
    return 0


def cmd_finalize(args: argparse.Namespace) -> int:
    state = load_state(args)
    result = poll_until_success(args, state, wait=True)
    if not result:
        raise RuntimeError("registration has not completed")
    configured = configure_csgclaw(args, state, result) if not args.no_configure else None
    role = resolve_role(args, state)
    worker_existed_before_ensure = None
    if role == "worker" and args.recreate in ("auto", "worker"):
        worker_existed_before_ensure = agent_exists(args, state["bot_id"])
    try:
        ensured = ensure_bot(args, state, result)
    except RuntimeError as exc:
        name = args.bot_name or state.get("bot_name") or state["bot_id"].removeprefix("u-") or state["bot_id"]
        if role == "worker" and worker_existed_before_ensure is False and is_box_name_conflict(exc, name):
            raise RuntimeError(worker_box_conflict_message(state["bot_id"], name)) from None
        raise
    recreated = maybe_recreate(args, state, worker_existed_before_ensure)
    if not args.keep_state:
        delete_state(args, state["registration_id"])
    if configured is not None:
        admin_open_id = str((configured or {}).get("admin_open_id") or "").strip()
    else:
        admin_open_id = str(result.get("open_id") or state.get("admin_open_id") or "").strip()
    worker_recreate_policy = None
    if role == "worker":
        worker_recreate_policy = "existing_worker_recreated" if worker_existed_before_ensure else "new_worker_not_recreated"
    output = {
        "status": "configured" if configured else "credentials_received",
        "bot_id": state["bot_id"],
        "role": state.get("role"),
        "app_id": result["app_id"],
        "app_secret": "present",
        "domain": result.get("domain"),
        "admin_open_id": admin_open_id,
        "config": public_result(configured or {}),
        "bot_ensured": ensured is not None,
        "worker_existed_before_ensure": worker_existed_before_ensure,
        "worker_recreate_policy": worker_recreate_policy,
        "recreate": public_result(recreated or {}),
    }
    print(json.dumps(output, ensure_ascii=False, indent=2))
    return 0


def cmd_status(args: argparse.Namespace) -> int:
    state = load_state(args)
    safe = {k: v for k, v in state.items() if k not in {"device_code"}}
    safe["device_code"] = "present"
    print(json.dumps(safe, ensure_ascii=False, indent=2))
    return 0


def cmd_recreate_agent(args: argparse.Namespace) -> int:
    bot_id = validate_bot_id(args.bot_id)
    if bot_id == "u-manager" and not args.confirm_manager:
        raise RuntimeError("manager recreate can interrupt the current run; pass --confirm-manager as the final confirmed action")
    result = api_json(args, "POST", f"/api/v1/agents/{quote(bot_id, safe='')}/recreate", None)
    if bot_id == "u-manager":
        output = manager_recreate_terminal_result(bot_id, result)
    else:
        output = {"status": "recreate_requested", "bot_id": bot_id, "result": public_result(result or {})}
    print(json.dumps(output, ensure_ascii=False, indent=2))
    return 0


def add_common(p: argparse.ArgumentParser) -> None:
    p.add_argument("--state-dir", default="", help="State directory; default is ~/.picoclaw/workspace/.feishu-channel-setup or ~/.cache/csgclaw-feishu-channel-setup")


def add_api_common(p: argparse.ArgumentParser) -> None:
    p.add_argument("--csgclaw-base-url", default="", help="CSGClaw base URL; default $CSGCLAW_BASE_URL or http://127.0.0.1:18080")
    p.add_argument("--api-timeout", type=int, default=None, help="CSGClaw API timeout in seconds; default $CSGCLAW_API_TIMEOUT or 600")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Feishu/Lark QR registration helper for CSGClaw Feishu channel setup")
    sub = parser.add_subparsers(dest="command", required=True)

    start = sub.add_parser("start", help="Start QR registration and print URL/QR")
    add_common(start)
    start.add_argument("--bot-id", required=True, help="CSGClaw bot id, e.g. u-dev or u-manager")
    start.add_argument("--role", choices=["worker", "manager"], default="", help="Bot role; inferred from bot id when omitted")
    start.add_argument("--bot-name", default="", help="CSGClaw bot display name")
    start.add_argument("--description", default="", help="CSGClaw bot description")
    start.add_argument("--admin-open-id", default="", help="Fallback admin open_id if registration does not return one")
    start.add_argument("--domain", choices=["feishu", "lark"], default="feishu")
    start.add_argument("--timeout", type=int, default=DEFAULT_EXPIRE_SECONDS)
    start.add_argument("--json", action="store_true", help="Print machine-readable JSON")
    start.add_argument("--qr", action="store_true", help="Try to render an ASCII QR code if qrcode is installed")
    start.set_defaults(func=cmd_start)

    poll = sub.add_parser("poll", help="Check whether the user has completed registration; does not print secrets")
    add_common(poll)
    poll.add_argument("--registration-id", required=True)
    poll.add_argument("--timeout", type=int, default=30)
    poll.set_defaults(func=cmd_poll)

    finalize = sub.add_parser("finalize", help="Wait for registration, write CSGClaw config, ensure bot, and optionally recreate agent")
    add_common(finalize)
    add_api_common(finalize)
    finalize.add_argument("--registration-id", required=True)
    finalize.add_argument("--timeout", type=int, default=DEFAULT_EXPIRE_SECONDS)
    finalize.add_argument("--no-configure", action="store_true", help="Do not write CSGClaw config; for debugging only, still never prints secret")
    finalize.add_argument("--no-ensure-bot", action="store_true", help="Skip POST /api/v1/bots")
    finalize.add_argument("--role", choices=["worker", "manager"], default="", help="Override role for ensure/recreate logic")
    finalize.add_argument("--bot-name", default="", help="Override bot name for ensure")
    finalize.add_argument("--description", default="", help="Override bot description for ensure")
    finalize.add_argument("--recreate", choices=["none", "auto", "worker", "manager"], default="auto", help="auto recreates existing workers but skips newly created workers and manager; manager requires --recreate manager --confirm-manager")
    finalize.add_argument("--confirm-manager", action="store_true", help="Required with --recreate manager; run only as the final confirmed action")
    finalize.add_argument("--keep-state", action="store_true", help="Keep registration state file after successful finalize")
    finalize.set_defaults(func=cmd_finalize)

    status = sub.add_parser("status", help="Print saved registration state without secrets")
    add_common(status)
    status.add_argument("--registration-id", required=True)
    status.set_defaults(func=cmd_status)

    recreate = sub.add_parser("recreate-agent", help="Request agent recreate after configuration; manager requires explicit confirmation")
    add_api_common(recreate)
    recreate.add_argument("--bot-id", required=True, help="CSGClaw bot/agent id to recreate")
    recreate.add_argument("--confirm-manager", action="store_true", help="Required when --bot-id u-manager; run only as the final confirmed action")
    recreate.set_defaults(func=cmd_recreate_agent)
    return parser


def main(argv: Optional[list[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except Exception as exc:
        eprint(f"error: {exc}")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
