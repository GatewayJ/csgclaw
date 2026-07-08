import { del, get, post, put } from "@/api/client";
import type { JSONRecord } from "@/models/agents";
import type { HubTemplate, HubWorkspaceFile, HubWorkspaceListing } from "@/models/hubWorkspace";
import type { HubMCPServerPayload } from "@/models/mcpHub";

const HUB_TEMPLATES_PATH = "/api/v1/hub/templates";
const MCP_SERVERS_PATH = "/api/v1/mcp-servers";

type PublishAgentTemplatePayload = {
  agent_id: string;
};

export function fetchHubTemplates(): Promise<HubTemplate[]> {
  return get<HubTemplate[]>(HUB_TEMPLATES_PATH);
}

export function fetchHubMCPServers(): Promise<JSONRecord> {
  return get<JSONRecord>(MCP_SERVERS_PATH);
}

export function createHubMCPServerRequest(payload: HubMCPServerPayload): Promise<JSONRecord> {
  return post<JSONRecord>(MCP_SERVERS_PATH, payload);
}

export function updateHubMCPServerRequest(name: string, payload: HubMCPServerPayload): Promise<JSONRecord> {
  return put<JSONRecord>(hubMCPServerPath(name), payload);
}

export function deleteHubMCPServerRequest(name: string): Promise<JSONRecord> {
  return del<JSONRecord>(hubMCPServerPath(name));
}

export function fetchHubTemplate(templateID: string): Promise<HubTemplate> {
  return get<HubTemplate>(hubTemplatePath(templateID));
}

export function fetchHubWorkspace(templateID: string, workspacePath = ""): Promise<HubWorkspaceListing> {
  const query = workspacePath ? `?path=${encodeURIComponent(workspacePath)}` : "";
  return get<HubWorkspaceListing>(`${hubTemplatePath(templateID)}/workspace${query}`);
}

export function fetchHubWorkspaceFile(templateID: string, workspacePath: string): Promise<HubWorkspaceFile> {
  return get<HubWorkspaceFile>(
    `${hubTemplatePath(templateID)}/workspace/file?path=${encodeURIComponent(workspacePath)}`,
  );
}

export function publishAgentTemplateRequest(agentID: string): Promise<HubTemplate> {
  const payload: PublishAgentTemplatePayload = {
    agent_id: agentID,
  };
  return post<HubTemplate>(HUB_TEMPLATES_PATH, payload);
}

export function deleteHubTemplateRequest(templateID: string): Promise<void> {
  return del(hubTemplatePath(templateID));
}

function hubTemplatePath(templateID: string): string {
  return `${HUB_TEMPLATES_PATH}/${encodeURIComponent(String(templateID || "").trim())}`;
}

function hubMCPServerPath(name: string): string {
  return `${MCP_SERVERS_PATH}/${encodeURIComponent(String(name || "").trim())}`;
}
