import { post } from "@/api/client";
import type { ApiError } from "@/api/client";

export type ActivityDecision = {
  decided_at?: string;
  kind?: string;
  option_id?: string;
};

export type ActivitySnapshot = {
  decision?: ActivityDecision | null;
  expires_at?: string;
  id: string;
  kind?: string;
  options?: Array<{ id: string; kind: string; label: string; scope?: string }>;
  requested_at?: string;
  status: string;
  title?: string;
};

export async function decideChannelActivity(
  channel: string,
  activityID: string,
  optionID: string,
): Promise<ActivitySnapshot> {
  try {
    return await post<ActivitySnapshot>(
      `api/v1/channels/${encodeURIComponent(channel)}/activities/${encodeURIComponent(activityID)}:decide`,
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

function snapshotFromAPIError(error: unknown): ActivitySnapshot | null {
  const apiError = error as ApiError | null;
  if (!apiError || (apiError.status !== 409 && apiError.status !== 410)) {
    return null;
  }
  try {
    const parsed = JSON.parse(apiError.message) as Partial<ActivitySnapshot>;
    if (typeof parsed?.id === "string" && typeof parsed?.status === "string") {
      return parsed as ActivitySnapshot;
    }
  } catch {
    return null;
  }
  return null;
}
