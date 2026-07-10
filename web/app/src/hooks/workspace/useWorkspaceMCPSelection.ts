import { useCallback, useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { errorMessage } from "@/api/client";
import { createMCPServerRequest, deleteMCPServerRequest, updateMCPServerRequest } from "@/api/mcp";
import { mcpServersFromResponse } from "@/models/mcp";
import type { MCPServer, MCPServerPayload } from "@/models/mcp";
import { workspaceQueryKeys, useWorkspaceMCPServersQuery } from "./workspaceQueries";

type HubResourceType = "template" | "skill" | "mcp";

type MCPServerNameSetter = (value: string | ((current: string) => string)) => void;

type UseWorkspaceMCPSelectionArgs = {
  selectedMCPServerName: string;
  selectedHubResourceType: HubResourceType;
  setSelectedMCPServerName: MCPServerNameSetter;
  setSelectedHubResourceType: (value: HubResourceType) => void;
  skillCount: number;
  t: (key: string) => string;
  templateCount: number;
};

export function useWorkspaceMCPSelection({
  selectedMCPServerName,
  selectedHubResourceType,
  setSelectedMCPServerName,
  setSelectedHubResourceType,
  skillCount,
  t,
  templateCount,
}: UseWorkspaceMCPSelectionArgs) {
  const queryClient = useQueryClient();
  const [mcpCreateDialogOpen, setMCPCreateDialogOpen] = useState(false);
  const [mcpMutationBusy, setMCPMutationBusy] = useState(false);
  const [mcpMutationError, setMCPMutationError] = useState("");
  const mcpServersQuery = useWorkspaceMCPServersQuery();

  const mcps = useMemo(() => mcpServersFromResponse(mcpServersQuery.data ?? null), [mcpServersQuery.data]);
  const selectedMCPServer = useMemo(
    () => mcps.find((item) => item.name === selectedMCPServerName) || mcps[0] || null,
    [mcps, selectedMCPServerName],
  );

  useEffect(() => {
    if (!mcps.length) {
      setSelectedMCPServerName("");
      return;
    }
    setSelectedMCPServerName((current) => (mcps.some((item) => item.name === current) ? current : mcps[0]?.name || ""));
  }, [mcps, setSelectedMCPServerName]);

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

  const createMCPServer = useCallback(
    async (payload: MCPServerPayload) => {
      setMCPMutationBusy(true);
      setMCPMutationError("");
      try {
        const state = await createMCPServerRequest(payload);
        queryClient.setQueryData(workspaceQueryKeys.mcpServers(), state);
        setSelectedHubResourceType("mcp");
        setSelectedMCPServerName(payload.name);
        setMCPCreateDialogOpen(false);
        return true;
      } catch (error) {
        setMCPMutationError(errorMessage(error, t("resourcesMCPSaveFailed")));
        return false;
      } finally {
        setMCPMutationBusy(false);
      }
    },
    [queryClient, setSelectedMCPServerName, setSelectedHubResourceType, t],
  );

  const updateMCPServer = useCallback(
    async (currentName: string, payload: MCPServerPayload) => {
      setMCPMutationBusy(true);
      setMCPMutationError("");
      try {
        const state = await updateMCPServerRequest(currentName, payload);
        queryClient.setQueryData(workspaceQueryKeys.mcpServers(), state);
        setSelectedHubResourceType("mcp");
        setSelectedMCPServerName(payload.name);
        return true;
      } catch (error) {
        setMCPMutationError(errorMessage(error, t("resourcesMCPSaveFailed")));
        return false;
      } finally {
        setMCPMutationBusy(false);
      }
    },
    [queryClient, setSelectedMCPServerName, setSelectedHubResourceType, t],
  );

  const deleteMCPServer = useCallback(
    async (item: MCPServer | null | undefined) => {
      const name = String(item?.name || "").trim();
      if (!name) {
        return false;
      }
      setMCPMutationBusy(true);
      setMCPMutationError("");
      try {
        const state = await deleteMCPServerRequest(name);
        queryClient.setQueryData(workspaceQueryKeys.mcpServers(), state);
        setSelectedMCPServerName("");
        setSelectedHubResourceType("mcp");
        return true;
      } catch (error) {
        setMCPMutationError(errorMessage(error, t("resourcesMCPDeleteFailed")));
        return false;
      } finally {
        setMCPMutationBusy(false);
      }
    },
    [queryClient, setSelectedMCPServerName, setSelectedHubResourceType, t],
  );

  const rawMCPServersError = mcpServersQuery.error
    ? errorMessage(mcpServersQuery.error, t("resourcesMCPLoadFailed"))
    : "";
  const mcpStateError = selectedHubResourceType === "mcp" ? rawMCPServersError : "";

  return {
    createMCPServer,
    deleteMCPServer,
    mcpServersFetching: mcpServersQuery.isFetching,
    mcps,
    mcpCreateDialogOpen,
    mcpMutationBusy,
    mcpMutationError,
    mcpStateError,
    openCreateMCPDialog,
    refetchMCPServers: mcpServersQuery.refetch,
    selectedMCPServer,
    setMCPCreateDialogOpen,
    updateMCPServer,
  };
}
