import { get } from "@/api/client";
import type { AgentSkillSummary } from "@/api/agents";
import type { JSONRecord } from "@/models/agents";
import type { MCPServer } from "@/models/mcp";
import { mcpServerDescription } from "@/models/mcp";

export type ResourceBatch<T> = {
  items: T[];
  total: number;
  list_revision: string;
  next_cursor?: string;
  has_more: boolean;
};
export type ResourcePage<T> = ResourceBatch<T> & { page: number; per: number };
export type PageRequest = { page: number; per: number; revision?: string };
export function fetchAgentSkillPage(agentID: string, page: PageRequest, signal?: AbortSignal) {
  return get<ResourcePage<AgentSkillSummary>>(
    resourcePath(agentID, "skill-summaries", {
      pagination: "page",
      page: String(page.page),
      per: String(page.per),
      list_revision: page.revision ?? "",
    }),
    { signal },
  );
}
export async function fetchAgentMCPPage(
  agentID: string,
  page: PageRequest,
  signal?: AbortSignal,
): Promise<ResourcePage<MCPServer>> {
  const result = await get<ResourcePage<{ name: string; config: JSONRecord }>>(
    resourcePath(agentID, "mcp-servers", {
      pagination: "page",
      page: String(page.page),
      per: String(page.per),
      list_revision: page.revision ?? "",
    }),
    { signal },
  );
  return {
    ...result,
    items: result.items.map((item) => ({ ...item, description: mcpServerDescription(item.config) })),
  };
}
export function fetchAgentSkillBatch(
  agentID: string,
  search: string,
  cursor: string,
  exclude: readonly string[],
  signal?: AbortSignal,
) {
  const params = new URLSearchParams({ pagination: "cursor", limit: "20", enabled: "true", search });
  if (cursor) params.set("cursor", cursor);
  for (const name of exclude) params.append("exclude", name);
  return get<ResourceBatch<AgentSkillSummary>>(
    `api/v1/agents/${encodeURIComponent(agentID)}/skill-summaries?${params}`,
    { signal },
  );
}
function resourcePath(agentID: string, resource: string, query: Record<string, string>) {
  return `api/v1/agents/${encodeURIComponent(agentID)}/${resource}?${new URLSearchParams(query)}`;
}
