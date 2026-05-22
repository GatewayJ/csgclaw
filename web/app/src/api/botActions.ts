import { post } from "@/api/client";
import type { ApiError } from "@/api/client";

export type BotActionDecision = {
  decided_at?: string;
  kind?: string;
  option_id?: string;
};

export type BotActionSnapshot = {
  bot_id?: string;
  decision?: BotActionDecision | null;
  expires_at?: string;
  id: string;
  kind?: string;
  options?: Array<{ id: string; kind: string; label: string }>;
  requested_at?: string;
  runtime_id?: string;
  session_id?: string;
  status: string;
  title?: string;
  tool_call_id?: string;
};

export async function decideBotAction(
  botID: string,
  actionID: string,
  optionID: string,
): Promise<BotActionSnapshot> {
  try {
    return await post<BotActionSnapshot>(
      `api/v1/bots/${encodeURIComponent(botID)}/actions/${encodeURIComponent(actionID)}/decision`,
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

function snapshotFromAPIError(error: unknown): BotActionSnapshot | null {
  const apiError = error as ApiError | null;
  if (!apiError || (apiError.status !== 409 && apiError.status !== 410)) {
    return null;
  }
  try {
    const parsed = JSON.parse(apiError.message) as Partial<BotActionSnapshot>;
    if (typeof parsed?.id === "string" && typeof parsed?.status === "string") {
      return parsed as BotActionSnapshot;
    }
  } catch {
    return null;
  }
  return null;
}
