import { describe, expect, it } from "vitest";
import {
  formatMCPServerWrapper,
  mcpServersFromResponse,
  mcpServerPayloadFromConfig,
  parseMCPServerWrapper,
  runtimeMCPServerConfig,
} from "@/models/mcp";

describe("MCP catalog helpers", () => {
  it("splits state mcpServers into individual sorted server entries", () => {
    expect(
      mcpServersFromResponse({
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

  it("builds a single MCP server payload from an already parsed config", () => {
    expect(
      mcpServerPayloadFromConfig({
        mcpServers: {
          filesystem: { command: "npx", args: ["-y"] },
        },
      }),
    ).toEqual({
      name: "filesystem",
      config: { command: "npx", args: ["-y"] },
    });
  });

  it("keeps runtime MCP fields while removing catalog display metadata", () => {
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
