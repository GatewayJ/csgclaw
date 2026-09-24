import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { setAgentResourceEnabled, type AgentResourceKind } from "@/api/agents";
import { localizeAPIError } from "@/shared/i18n";
import type { TranslateFn } from "@/models/conversations";
import { workspaceQueryKeys } from "./workspaceQueries";

export function useAgentResourceEnablement(agentID: string, t: TranslateFn, onChanged: (id: string) => Promise<void>) {
  const queryClient = useQueryClient();
  const currentAgent = useRef(agentID);
  currentAgent.current = agentID;
  const inFlight = useRef(false);
  const lastRequest = useRef<{ agentID: string; kind: AgentResourceKind; name: string; enabled: boolean } | null>(null);
  const [state, setState] = useState({ agentID, busy: "", error: "" });
  async function setEnabled(kind: AgentResourceKind, name: string, enabled: boolean): Promise<void> {
    if (!agentID || inFlight.current) return;
    inFlight.current = true;
    lastRequest.current = { agentID, kind, name, enabled };
    setState({ agentID, busy: `${kind}:${name}`, error: "" });
    let failure = "";
    try {
      await setAgentResourceEnabled(agentID, kind, name, enabled);
    } catch (error) {
      failure = localizeAPIError(error, t, t("agentResourceApplyFailed"));
    } finally {
      await Promise.allSettled([
        queryClient.invalidateQueries({ queryKey: workspaceQueryKeys.agentSkills(agentID) }),
        queryClient.invalidateQueries({ queryKey: workspaceQueryKeys.agentMCPServers(agentID) }),
        currentAgent.current === agentID ? onChanged(agentID) : Promise.resolve(),
      ]);
      inFlight.current = false;
      if (currentAgent.current === agentID) setState({ agentID, busy: "", error: failure });
    }
  }
  return {
    busy: state.agentID === agentID ? state.busy : "",
    error: state.agentID === agentID ? state.error : "",
    setEnabled,
    retry: async () => {
      const request = lastRequest.current;
      if (request?.agentID === agentID) await setEnabled(request.kind, request.name, request.enabled);
    },
  };
}
