import { post } from "@/api/client";
import type { ApiError } from "@/api/client";

export type CodexPermissionDecision = {
  decided_at?: string;
  kind?: string;
  option_id?: string;
};

export type CodexPermissionSnapshot = {
  decision?: CodexPermissionDecision | null;
  expires_at?: string;
  id: string;
  options?: Array<{ id: string; kind: string; label: string }>;
  requested_at?: string;
  runtime_id?: string;
  session_id?: string;
  status: string;
  title?: string;
  tool_call_id?: string;
};

export async function decideCodexPermissionRequest(
  requestID: string,
  optionID: string,
): Promise<CodexPermissionSnapshot> {
  try {
    return await post<CodexPermissionSnapshot>(
      `api/v1/codex/permissions/${encodeURIComponent(requestID)}/decision`,
      { option_id: optionID },
    );
  } catch (error) {
    const maybeSnapshot = snapshotFromAPIError(error);
    if (maybeSnapshot) {
      return maybeSnapshot;
    }
    throw error;
  }
}

function snapshotFromAPIError(error: unknown): CodexPermissionSnapshot | null {
  const apiError = error as ApiError | null;
  if (!apiError || (apiError.status !== 409 && apiError.status !== 410)) {
    return null;
  }
  try {
    const parsed = JSON.parse(apiError.message) as Partial<CodexPermissionSnapshot>;
    if (typeof parsed?.id === "string" && typeof parsed?.status === "string") {
      return parsed as CodexPermissionSnapshot;
    }
  } catch {
    return null;
  }
  return null;
}
