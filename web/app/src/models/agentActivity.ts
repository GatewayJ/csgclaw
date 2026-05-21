import {
  AgentActivityMsgTypes,
  CSGCLAW_AGENT_ACTIVITY_TYPE,
} from "@/shared/constants/messages";
import type { IMMessage } from "@/models/conversations";

type UnknownRecord = Record<string, unknown>;

export type AgentActivityRuntime = {
  kind: string;
  runtime_id: string;
  session_id: string;
};

export type AgentActivityTool = {
  id: string;
  input_summary?: string;
  kind?: string;
  output_summary?: string;
  status: string;
  title: string;
};

export type AgentActivityPermissionOption = {
  id: string;
  kind: string;
  label: string;
};

export type AgentActivityPermissionDecision = {
  decided_at?: string;
  kind?: string;
  option_id?: string;
};

export type AgentActivityPermission = {
  decision?: AgentActivityPermissionDecision | null;
  expires_at?: string;
  id: string;
  options?: AgentActivityPermissionOption[];
  requested_at?: string;
  status: string;
  title: string;
  tool_call_id?: string;
};

export type AgentActivityContent = {
  body: string;
  msgtype: string;
  permission?: AgentActivityPermission;
  runtime?: AgentActivityRuntime;
  tool?: AgentActivityTool;
};

export type AgentActivityPayload = {
  content: AgentActivityContent;
  event_id: string;
  origin_server_ts: number;
  room_id: string;
  sender: string;
  type: typeof CSGCLAW_AGENT_ACTIVITY_TYPE;
};

export function parseAgentActivity(content: unknown): AgentActivityPayload | null {
  const parsed = typeof content === "string" ? parseJSON(content.trim()) : content;
  if (!isRecord(parsed) || parsed.type !== CSGCLAW_AGENT_ACTIVITY_TYPE || !isRecord(parsed.content)) {
    return null;
  }

  const activityContent = parsed.content;
  const msgtype = stringValue(activityContent.msgtype);
  if (!Object.values(AgentActivityMsgTypes).includes(msgtype as (typeof AgentActivityMsgTypes)[keyof typeof AgentActivityMsgTypes])) {
    return null;
  }

  return {
    content: {
      body: stringValue(activityContent.body, "Agent activity"),
      msgtype,
      permission: parsePermission(activityContent.permission),
      runtime: parseRuntime(activityContent.runtime),
      tool: parseTool(activityContent.tool),
    },
    event_id: stringValue(parsed.event_id),
    origin_server_ts: numberValue(parsed.origin_server_ts),
    room_id: stringValue(parsed.room_id),
    sender: stringValue(parsed.sender),
    type: CSGCLAW_AGENT_ACTIVITY_TYPE,
  };
}

export function isToolActivityMessage(message: IMMessage | null | undefined): boolean {
  const activity = parseAgentActivity(message?.content);
  return activity?.content.msgtype === AgentActivityMsgTypes.tool;
}

export function permissionOptionLabel(option: AgentActivityPermissionOption): string {
  return stringValue(option.label, option.kind, option.id);
}

export function statusLabel(status: string): string {
  switch (status) {
    case "allowed":
      return "Allowed";
    case "rejected":
      return "Rejected";
    case "expired":
      return "Expired";
    case "canceled":
      return "Canceled";
    case "completed":
      return "Completed";
    case "failed":
      return "Failed";
    case "running":
      return "Running";
    case "pending":
      return "Pending";
    default:
      return stringValue(status, "Status");
  }
}

function parseRuntime(value: unknown): AgentActivityRuntime | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  return {
    kind: stringValue(value.kind),
    runtime_id: stringValue(value.runtime_id),
    session_id: stringValue(value.session_id),
  };
}

function parseTool(value: unknown): AgentActivityTool | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  return {
    id: stringValue(value.id),
    input_summary: stringValue(value.input_summary),
    kind: stringValue(value.kind),
    output_summary: stringValue(value.output_summary),
    status: stringValue(value.status, "running"),
    title: stringValue(value.title, "Run tool"),
  };
}

function parsePermission(value: unknown): AgentActivityPermission | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  return {
    decision: parseDecision(value.decision),
    expires_at: stringValue(value.expires_at),
    id: stringValue(value.id),
    options: Array.isArray(value.options) ? value.options.map(parseOption).filter(isPermissionOption) : [],
    requested_at: stringValue(value.requested_at),
    status: stringValue(value.status, "pending"),
    title: stringValue(value.title, "Run tool"),
    tool_call_id: stringValue(value.tool_call_id),
  };
}

function parseOption(value: unknown): AgentActivityPermissionOption | null {
  if (!isRecord(value)) {
    return null;
  }
  const id = stringValue(value.id);
  if (!id) {
    return null;
  }
  return {
    id,
    kind: stringValue(value.kind),
    label: stringValue(value.label, value.kind, id),
  };
}

function isPermissionOption(value: AgentActivityPermissionOption | null): value is AgentActivityPermissionOption {
  return value !== null;
}

function parseDecision(value: unknown): AgentActivityPermissionDecision | null {
  if (!isRecord(value)) {
    return null;
  }
  return {
    decided_at: stringValue(value.decided_at),
    kind: stringValue(value.kind),
    option_id: stringValue(value.option_id),
  };
}

function parseJSON(input: string): unknown {
  if (!input.startsWith("{")) {
    return null;
  }
  try {
    return JSON.parse(input);
  } catch {
    return null;
  }
}

function isRecord(value: unknown): value is UnknownRecord {
  return Boolean(value && typeof value === "object" && !Array.isArray(value));
}

function stringValue(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) {
      return value.trim();
    }
  }
  return "";
}

function numberValue(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}
