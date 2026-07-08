import { describe, expect, it } from "vitest";
import { formatMCPServerWrapper, hubMCPServersFromResponse, parseMCPServerWrapper } from "@/models/mcpHub";

describe("MCP hub helpers", () => {
  it("splits state mcpServers into individual sorted server entries", () => {
    expect(
      hubMCPServersFromResponse({
        mcpServers: {
          github: { url: "https://github.example/mcp" },
          filesystem: { command: "npx", args: ["-y", "@modelcontextprotocol/server-filesystem"] },
        },
      }),
    ).toEqual([
      {
        name: "filesystem",
        config: { command: "npx", args: ["-y", "@modelcontextprotocol/server-filesystem"] },
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
    const formatted = formatMCPServerWrapper("filesystem", { command: "npx", args: ["-y"] });

    expect(parseMCPServerWrapper(formatted)).toEqual({
      name: "filesystem",
      config: { command: "npx", args: ["-y"] },
    });
  });
});
