import { useCallback, useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { errorMessage } from "@/api/client";
import { createHubMCPServerRequest, deleteHubMCPServerRequest, updateHubMCPServerRequest } from "@/api/hub";
import { hubMCPServersFromResponse } from "@/models/mcpHub";
import type { HubMCPServer, HubMCPServerPayload } from "@/models/mcpHub";
import { workspaceQueryKeys, useWorkspaceHubMCPServersQuery } from "./workspaceQueries";

type HubResourceType = "template" | "skill" | "mcp";

type HubMCPNameSetter = (value: string | ((current: string) => string)) => void;

type UseWorkspaceHubMCPSelectionArgs = {
  selectedHubMCPName: string;
  selectedHubResourceType: HubResourceType;
  setSelectedHubMCPName: HubMCPNameSetter;
  setSelectedHubResourceType: (value: HubResourceType) => void;
  skillCount: number;
  t: (key: string) => string;
  templateCount: number;
};

export function useWorkspaceHubMCPSelection({
  selectedHubMCPName,
  selectedHubResourceType,
  setSelectedHubMCPName,
  setSelectedHubResourceType,
  skillCount,
  t,
  templateCount,
}: UseWorkspaceHubMCPSelectionArgs) {
  const queryClient = useQueryClient();
  const [mcpCreateDialogOpen, setMCPCreateDialogOpen] = useState(false);
  const [mcpMutationBusy, setMCPMutationBusy] = useState(false);
  const [mcpMutationError, setMCPMutationError] = useState("");
  const mcpServersQuery = useWorkspaceHubMCPServersQuery();

  const mcps = useMemo(() => hubMCPServersFromResponse(mcpServersQuery.data ?? null), [mcpServersQuery.data]);
  const selectedHubMCP = useMemo(
    () => mcps.find((item) => item.name === selectedHubMCPName) || mcps[0] || null,
    [mcps, selectedHubMCPName],
  );

  useEffect(() => {
    if (!mcps.length) {
      setSelectedHubMCPName("");
      return;
    }
    setSelectedHubMCPName((current) => (mcps.some((item) => item.name === current) ? current : mcps[0]?.name || ""));
  }, [mcps, setSelectedHubMCPName]);

  useEffect(() => {
    if (selectedHubResourceType === "mcp" && !mcps.length) {
      setSelectedHubResourceType(skillCount ? "skill" : "template");
      return;
    }
    if (selectedHubResourceType === "skill" && !skillCount) {
      setSelectedHubResourceType(mcps.length ? "mcp" : "template");
      return;
    }
    if (selectedHubResourceType === "template" && !templateCount) {
      setSelectedHubResourceType(mcps.length ? "mcp" : skillCount ? "skill" : "template");
    }
  }, [mcps.length, selectedHubResourceType, setSelectedHubResourceType, skillCount, templateCount]);

  const openCreateMCPDialog = useCallback(() => {
    setSelectedHubResourceType("mcp");
    setMCPMutationError("");
    setMCPCreateDialogOpen(true);
  }, [setSelectedHubResourceType]);

  const createHubMCPServer = useCallback(
    async (payload: HubMCPServerPayload) => {
      setMCPMutationBusy(true);
      setMCPMutationError("");
      try {
        const state = await createHubMCPServerRequest(payload);
        queryClient.setQueryData(workspaceQueryKeys.mcpServers(), state);
        setSelectedHubResourceType("mcp");
        setSelectedHubMCPName(payload.name);
        setMCPCreateDialogOpen(false);
        return true;
      } catch (error) {
        setMCPMutationError(errorMessage(error, t("resourcesMCPSaveFailed")));
        return false;
      } finally {
        setMCPMutationBusy(false);
      }
    },
    [queryClient, setSelectedHubMCPName, setSelectedHubResourceType, t],
  );

  const updateHubMCPServer = useCallback(
    async (currentName: string, payload: HubMCPServerPayload) => {
      setMCPMutationBusy(true);
      setMCPMutationError("");
      try {
        const state = await updateHubMCPServerRequest(currentName, payload);
        queryClient.setQueryData(workspaceQueryKeys.mcpServers(), state);
        setSelectedHubResourceType("mcp");
        setSelectedHubMCPName(payload.name);
        return true;
      } catch (error) {
        setMCPMutationError(errorMessage(error, t("resourcesMCPSaveFailed")));
        return false;
      } finally {
        setMCPMutationBusy(false);
      }
    },
    [queryClient, setSelectedHubMCPName, setSelectedHubResourceType, t],
  );

  const deleteHubMCPServer = useCallback(
    async (item: HubMCPServer | null | undefined) => {
      const name = String(item?.name || "").trim();
      if (!name) {
        return false;
      }
      setMCPMutationBusy(true);
      setMCPMutationError("");
      try {
        const state = await deleteHubMCPServerRequest(name);
        queryClient.setQueryData(workspaceQueryKeys.mcpServers(), state);
        setSelectedHubMCPName("");
        setSelectedHubResourceType("mcp");
        return true;
      } catch (error) {
        setMCPMutationError(errorMessage(error, t("resourcesMCPDeleteFailed")));
        return false;
      } finally {
        setMCPMutationBusy(false);
      }
    },
    [queryClient, setSelectedHubMCPName, setSelectedHubResourceType, t],
  );

  const rawMCPServersError = mcpServersQuery.error ? errorMessage(mcpServersQuery.error, t("resourcesMCPLoadFailed")) : "";
  const mcpStateError = selectedHubResourceType === "mcp" ? rawMCPServersError : "";

  return {
    createHubMCPServer,
    deleteHubMCPServer,
    mcpServersFetching: mcpServersQuery.isFetching,
    mcps,
    mcpCreateDialogOpen,
    mcpMutationBusy,
    mcpMutationError,
    mcpStateError,
    openCreateMCPDialog,
    refetchHubMCPServers: mcpServersQuery.refetch,
    selectedHubMCP,
    setMCPCreateDialogOpen,
    updateHubMCPServer,
  };
}
