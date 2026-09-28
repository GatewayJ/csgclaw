import { builtinSlashCommandNames, suggestedSlashCommandNames, type SkillContinuation } from "@/models/slashCommands";
import { useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { fetchAgentSkillBatch } from "@/api/agentResources";
import { apiErrorCode } from "@/api/client";
import { workspaceQueryKeys } from "./workspaceQueries";

const excludedCommands = [...builtinSlashCommandNames, ...suggestedSlashCommandNames];
export function useInfiniteAgentSkills(agentID: string, search: string | null, enabled: boolean) {
  const [settledSearch, setSettledSearch] = useState(search);
  const client = useQueryClient();
  const currentScope = useRef("");
  const scope = JSON.stringify([agentID, search, enabled]);
  useEffect(() => {
    currentScope.current = scope;
  }, [scope]);
  useEffect(() => {
    const timer = setTimeout(() => setSettledSearch(search), 150);
    return () => clearTimeout(timer);
  }, [search]);
  const key = [...workspaceQueryKeys.agentSkills(agentID), "cursor", settledSearch ?? ""];
  const query = useInfiniteQuery({
    queryKey: key,
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      fetchAgentSkillBatch(agentID, settledSearch ?? "", pageParam, excludedCommands, signal),
    getNextPageParam: (page) => page.next_cursor || undefined,
    enabled: Boolean(agentID) && enabled && search !== null && search === settledSearch,
    retry: (count, error) => apiErrorCode(error) !== "resource_list_changed" && count < 1,
  });
  const changed = apiErrorCode(query.error) === "resource_list_changed";
  useEffect(() => {
    if (changed)
      void client.resetQueries({
        queryKey: [...workspaceQueryKeys.agentSkills(agentID), "cursor", settledSearch ?? ""],
        exact: true,
      });
  }, [changed, agentID, settledSearch, client]);
  const ready = search === settledSearch;
  const items = useMemo(
    () => (ready ? (query.data?.pages.flatMap((page) => page.items) ?? []) : []),
    [ready, query.data],
  );
  const continuation: SkillContinuation = {
    scope,
    hasMore: ready && Boolean(query.hasNextPage),
    loading: query.isFetching || !ready,
    failed: query.isError && !changed,
    loadMore: async () => {
      if (query.isFetching || !ready || !query.hasNextPage) return false;
      const result = await query.fetchNextPage({ cancelRefetch: false });
      return currentScope.current === scope && !result.isError;
    },
    retry: () => {
      if (query.hasNextPage) void query.fetchNextPage();
      else void query.refetch();
    },
  };
  return { items, continuation, loading: enabled && search !== null && (!ready || query.isLoading) };
}
