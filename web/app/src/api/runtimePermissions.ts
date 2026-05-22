import { post } from "@/api/client";
import type { ApiError } from "@/api/client";

export type RuntimePermissionDecision = {
  decided_at?: string;
  kind?: string;
  option_id?: string;
};

export type RuntimePermissionSnapshot = {
  decision?: RuntimePermissionDecision | null;
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

export async function decideRuntimePermissionRequest(
  requestID: string,
  optionID: string,
): Promise<RuntimePermissionSnapshot> {
  try {
    return await post<RuntimePermissionSnapshot>(
      `api/v1/runtime/permissions/${encodeURIComponent(requestID)}/decision`,
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

function snapshotFromAPIError(error: unknown): RuntimePermissionSnapshot | null {
  const apiError = error as ApiError | null;
  if (!apiError || (apiError.status !== 409 && apiError.status !== 410)) {
    return null;
  }
  try {
    const parsed = JSON.parse(apiError.message) as Partial<RuntimePermissionSnapshot>;
    if (typeof parsed?.id === "string" && typeof parsed?.status === "string") {
      return parsed as RuntimePermissionSnapshot;
    }
  } catch {
    return null;
  }
  return null;
}
