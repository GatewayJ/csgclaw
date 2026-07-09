import type { JSONRecord } from "@/models/agents";

export type HubMCPServer = {
  config: JSONRecord;
  description?: string;
  name: string;
};

export type HubMCPServerPayload = {
  config: JSONRecord;
  name: string;
};

export function hubMCPServersFromResponse(response: unknown): HubMCPServer[] {
  if (!isJSONRecord(response)) {
    return [];
  }
  const servers = mcpServerRecordFromRoot(response);
  if (!servers) {
    return [];
  }
  return Object.entries(servers)
    .reduce<HubMCPServer[]>((items, [name, value]) => {
      const normalizedName = String(name || "").trim();
      if (!normalizedName || !isJSONRecord(value)) {
        return items;
      }
      const config = cloneJSONRecord(value);
      items.push({
        name: normalizedName,
        config,
        description: mcpServerDescription(config),
      });
      return items;
    }, [])
    .sort((left, right) => left.name.localeCompare(right.name));
}

export function hubMCPServersFromConfig(config: unknown): HubMCPServer[] {
  const servers = mcpServerRecordFromRoot(config);
  if (!servers) {
    return [];
  }
  return Object.entries(servers)
    .reduce<HubMCPServer[]>((items, [name, value]) => {
      const normalizedName = String(name || "").trim();
      if (!normalizedName || !isJSONRecord(value)) {
        return items;
      }
      const serverConfig = cloneJSONRecord(value);
      items.push({
        name: normalizedName,
        config: serverConfig,
        description: mcpServerDescription(serverConfig),
      });
      return items;
    }, [])
    .sort((left, right) => left.name.localeCompare(right.name));
}

export function mcpServerRecordFromConfig(config: unknown): Record<string, JSONRecord> {
  return cloneMCPServersRecord(mcpServerRecordFromRoot(config));
}

export function parseMCPServerWrapper(value: string): HubMCPServerPayload | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(value);
  } catch {
    return null;
  }
  return mcpServerPayloadFromConfig(parsed);
}

export function mcpServerPayloadFromConfig(config: unknown): HubMCPServerPayload | null {
  const entries = Object.entries(cloneMCPServersRecord(mcpServerRecordFromRoot(config)));
  if (entries.length !== 1) {
    return null;
  }
  const [name, serverConfig] = entries[0];
  return {
    name,
    config: serverConfig,
  };
}

export function formatMCPServerWrapper(name: string, config: JSONRecord): string {
  return JSON.stringify(
    {
      mcpServers: {
        [String(name || "").trim()]: cloneJSONRecord(config),
      },
    },
    null,
    2,
  );
}

export function mcpConfigWithServers(currentConfig: unknown, servers: Record<string, JSONRecord>): JSONRecord {
  const base = isJSONRecord(currentConfig) ? cloneJSONRecord(currentConfig) : {};
  if (Object.keys(servers).length === 0) {
    delete base.mcpServers;
    return base;
  }
  return {
    ...base,
    mcpServers: cloneMCPServersRecord(servers),
  };
}

export function mcpServerDescription(config: JSONRecord | null | undefined): string {
  if (!config) {
    return "";
  }
  const explicit = String(config.description ?? "").trim();
  if (explicit) {
    return explicit;
  }
  const command = String(config.command ?? "").trim();
  const args = Array.isArray(config.args) ? config.args.map((item) => String(item ?? "").trim()).filter(Boolean) : [];
  if (command) {
    return [command, ...args].join(" ");
  }
  const url = String(config.url ?? "").trim();
  if (url) {
    return url;
  }
  const transport = String(config.transport ?? config.type ?? "").trim();
  return transport;
}

export function cloneJSONRecord(value: JSONRecord): JSONRecord {
  try {
    return JSON.parse(JSON.stringify(value)) as JSONRecord;
  } catch {
    return { ...value };
  }
}

export function runtimeMCPServerConfig(config: JSONRecord): JSONRecord {
  const out = cloneJSONRecord(config);
  delete out.description;
  return out;
}

function mcpServerRecordFromRoot(value: unknown): Record<string, unknown> | null {
  if (!isJSONRecord(value)) {
    return null;
  }
  return isJSONRecord(value.mcpServers) ? (value.mcpServers as Record<string, unknown>) : null;
}

function cloneMCPServersRecord(value: Record<string, unknown> | null | undefined): Record<string, JSONRecord> {
  const out: Record<string, JSONRecord> = {};
  Object.entries(value || {}).forEach(([name, config]) => {
    const normalizedName = String(name || "").trim();
    if (!normalizedName || !isJSONRecord(config)) {
      return;
    }
    out[normalizedName] = cloneJSONRecord(config);
  });
  return out;
}

function isJSONRecord(value: unknown): value is JSONRecord {
  return Boolean(value && typeof value === "object" && !Array.isArray(value));
}
