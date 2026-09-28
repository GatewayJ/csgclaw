import { apiErrorCode } from "@/api/client";
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchAgentMCPPage, fetchAgentSkillPage, type PageRequest, type ResourcePage } from "@/api/agentResources";
import type { AgentSkillSummary } from "@/api/agents";
import type { MCPServer } from "@/models/mcp";
import { workspaceQueryKeys } from "./workspaceQueries";

export type ResourcePagination = {
  page: number;
  per: number;
  total: number;
  loading: boolean;
  retry: () => void;
  onPageChange: (page: number) => void;
  onCapacityChange: (per: number) => void;
};

function useResourcePage<T>(
  agentID: string,
  queryKey: readonly string[],
  fetchPage: (id: string, page: PageRequest, signal?: AbortSignal) => Promise<ResourcePage<T>>,
) {
  const [state, setState] = useState({ agentID, page: 1, per: 12, revision: "" });
  const request = state.agentID === agentID ? state : { agentID, page: 1, per: state.per, revision: "" };
  const query = useQuery({
    queryKey: [...queryKey, "page", request.page, request.per, request.revision],
    queryFn: ({ signal }) => fetchPage(agentID, request, signal),
    enabled: Boolean(agentID),
    placeholderData: (previous, previousQuery) => (previousQuery?.queryKey[2] === agentID ? previous : undefined),
    retry: (count, error) => apiErrorCode(error) !== "resource_list_changed" && count < 1,
  });
  const total = query.data?.total ?? 0;
  useEffect(() => {
    if (query.data && !query.isPlaceholderData && request.page > Math.max(1, Math.ceil(total / request.per))) {
      setState({ agentID, page: Math.max(1, Math.ceil(total / request.per)), per: request.per, revision: "" });
    }
  }, [agentID, query.data, query.isPlaceholderData, request.page, request.per, total]);
  const changed = apiErrorCode(query.error) === "resource_list_changed";
  useEffect(() => {
    if (changed) setState((current) => ({ ...current, revision: "" }));
  }, [changed]);
  const pagination: ResourcePagination = {
    page: request.page,
    per: request.per,
    total,
    loading: query.isFetching,
    retry: () => {
      void query.refetch();
    },
    onPageChange: (page) => setState({ ...request, page, revision: query.data?.list_revision ?? "" }),
    onCapacityChange: (per) => {
      if (per !== request.per)
        setState({ agentID, per, revision: "", page: Math.floor(((request.page - 1) * request.per) / per) + 1 });
    },
  };
  return { query, pagination };
}
export function useAgentResourcePages(agentID: string) {
  const skills = useResourcePage<AgentSkillSummary>(
    agentID,
    workspaceQueryKeys.agentSkills(agentID),
    fetchAgentSkillPage,
  );
  const mcp = useResourcePage<MCPServer>(agentID, workspaceQueryKeys.agentMCPServers(agentID), fetchAgentMCPPage);
  return { skills, mcp };
}
