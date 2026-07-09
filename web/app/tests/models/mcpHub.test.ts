import { describe, expect, it } from "vitest";
import {
  formatMCPServerWrapper,
  hubMCPServersFromResponse,
  parseMCPServerWrapper,
  runtimeMCPServerConfig,
} from "@/models/mcpHub";

describe("MCP hub helpers", () => {
  it("splits state mcpServers into individual sorted server entries", () => {
    expect(
      hubMCPServersFromResponse({
        mcpServers: {
          github: { url: "https://github.example/mcp" },
          filesystem: {
            command: "npx",
            args: ["-y", "@modelcontextprotocol/server-filesystem"],
            startup_timeout_sec: 60,
          },
        },
      }),
    ).toEqual([
      {
        name: "filesystem",
        config: { command: "npx", args: ["-y", "@modelcontextprotocol/server-filesystem"], startup_timeout_sec: 60 },
        description: "npx -y @modelcontextprotocol/server-filesystem",
      },
      {
        name: "github",
        config: { url: "https://github.example/mcp" },
        description: "https://github.example/mcp",
      },
    ]);
  });

  it("parses and formats a single wrapped MCP server", () => {
    const formatted = formatMCPServerWrapper("filesystem", {
      command: "npx",
      args: ["-y"],
      startup_timeout_sec: 60,
    });

    expect(parseMCPServerWrapper(formatted)).toEqual({
      name: "filesystem",
      config: { command: "npx", args: ["-y"], startup_timeout_sec: 60 },
    });
  });

  it("keeps runtime MCP fields while removing hub display metadata", () => {
    expect(
      runtimeMCPServerConfig({
        command: "uvx",
        description: "Grafana",
        startup_timeout_sec: 120,
      }),
    ).toEqual({
      command: "uvx",
      startup_timeout_sec: 120,
    });
  });
});
