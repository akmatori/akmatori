import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  createGatewayCallTool,
  createSearchToolsTool,
  createGetToolDetailTool,
  GatewayCallParams,
  SearchToolsParams,
  GetToolDetailParams,
  type GatewayCallInput,
  type SearchToolsInput,
  type GetToolDetailInput,
} from "../src/gateway-tools.js";
import type { GatewayClient, CallResult } from "../src/gateway-client.js";

// ---------------------------------------------------------------------------
// Mock GatewayClient
// ---------------------------------------------------------------------------

function createMockClient(overrides?: Partial<GatewayClient>): GatewayClient {
  return {
    call: vi.fn(async () => ({ data: { status: "ok" } } as CallResult)),
    searchTools: vi.fn(async () => ({ tools: [] })),
    getToolDetail: vi.fn(async () => ({
      name: "ssh.execute_command",
      description: "Execute SSH command",
      params: {},
      instances: [],
    })),
    ...overrides,
  } as unknown as GatewayClient;
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("createGatewayCallTool", () => {
  let mockClient: GatewayClient;

  beforeEach(() => {
    mockClient = createMockClient();
  });

  describe("tool definition", () => {
    it("should have correct name and description", () => {
      const tool = createGatewayCallTool({ client: mockClient });

      expect(tool.name).toBe("gateway_call");
      expect(tool.label).toBe("Gateway Call");
      expect(tool.description).toContain("MCP Gateway");
    });

    it("should have correct parameter schema", () => {
      const tool = createGatewayCallTool({ client: mockClient });

      expect(tool.parameters).toBe(GatewayCallParams);
      // Verify schema has the expected properties
      expect(tool.parameters.properties.tool_name).toBeDefined();
      expect(tool.parameters.properties.args).toBeDefined();
      expect(tool.parameters.properties.instance).toBeDefined();
    });

    it("should have promptGuidelines", () => {
      const tool = createGatewayCallTool({ client: mockClient });

      expect(tool.promptGuidelines).toBeDefined();
      expect(Array.isArray(tool.promptGuidelines)).toBe(true);
      expect(tool.promptGuidelines!.length).toBeGreaterThan(0);
      expect(tool.promptGuidelines!.some((g: string) => g.includes("gateway_call"))).toBe(true);
    });
  });

  describe("execute handler", () => {
    it("should call GatewayClient.call with correct arguments", async () => {
      const tool = createGatewayCallTool({ client: mockClient });

      const params: GatewayCallInput = {
        tool_name: "ssh.execute_command",
        args: { command: "uptime", servers: ["web-01"] },
        instance: "prod-ssh",
      };

      await tool.execute("tc-1", params, undefined, undefined);

      expect(mockClient.call).toHaveBeenCalledWith(
        "ssh.execute_command",
        { command: "uptime", servers: ["web-01"] },
        "prod-ssh",
      );
    });

    it("should call without instance when not provided", async () => {
      const tool = createGatewayCallTool({ client: mockClient });

      const params: GatewayCallInput = {
        tool_name: "zabbix.get_problems",
        args: { severity_min: 3 },
      };

      await tool.execute("tc-2", params, undefined, undefined);

      expect(mockClient.call).toHaveBeenCalledWith(
        "zabbix.get_problems",
        { severity_min: 3 },
        undefined,
      );
    });

    it("should return JSON-stringified object results", async () => {
      const client = createMockClient({
        call: vi.fn(async () => ({
          data: { hosts: ["web-01", "web-02"], count: 2 },
        })) as any,
      });
      const tool = createGatewayCallTool({ client });

      const result = await tool.execute(
        "tc-3",
        { tool_name: "zabbix.get_hosts", args: {} },
        undefined,
        undefined,
      );

      expect(result.content).toHaveLength(1);
      expect(result.content[0].type).toBe("text");
      const parsed = JSON.parse(result.content[0].text);
      expect(parsed.hosts).toEqual(["web-01", "web-02"]);
      expect(parsed.count).toBe(2);
    });

    it("should return string results directly", async () => {
      const client = createMockClient({
        call: vi.fn(async () => ({
          data: "uptime: 42 days, 3 users",
        })) as any,
      });
      const tool = createGatewayCallTool({ client });

      const result = await tool.execute(
        "tc-4",
        { tool_name: "ssh.execute_command", args: { command: "uptime" } },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toBe("uptime: 42 days, 3 users");
    });

    it("should include output file path when result was truncated", async () => {
      const client = createMockClient({
        call: vi.fn(async () => ({
          data: "truncated preview...",
          outputFile: "/workspace/tool_outputs/ssh_1234.json",
        })) as any,
      });
      const tool = createGatewayCallTool({ client });

      const result = await tool.execute(
        "tc-5",
        { tool_name: "ssh.execute_command", args: { command: "ls -la" } },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("truncated preview...");
      expect(result.content[0].text).toContain("/workspace/tool_outputs/ssh_1234.json");
    });

    it("should handle errors gracefully", async () => {
      const client = createMockClient({
        call: vi.fn(async () => {
          throw new Error("MCP Error -32000: Connection refused");
        }) as any,
      });
      const tool = createGatewayCallTool({ client });

      const result = await tool.execute(
        "tc-6",
        { tool_name: "ssh.execute_command", args: { command: "uptime" } },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("Connection refused");
    });

    it("should handle non-Error thrown values", async () => {
      const client = createMockClient({
        call: vi.fn(async () => {
          throw "unexpected string error";
        }) as any,
      });
      const tool = createGatewayCallTool({ client });

      const result = await tool.execute(
        "tc-7",
        { tool_name: "ssh.execute_command", args: {} },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("unexpected string error");
    });
  });
});

describe("GatewayCallParams schema", () => {
  it("should require tool_name as string", () => {
    expect(GatewayCallParams.properties.tool_name.type).toBe("string");
  });

  it("should require args as record", () => {
    expect(GatewayCallParams.properties.args).toBeDefined();
  });

  it("should have optional instance field", () => {
    // Optional fields in TypeBox are wrapped in Type.Optional
    const instanceProp = GatewayCallParams.properties.instance;
    expect(instanceProp).toBeDefined();
  });
});

// ---------------------------------------------------------------------------
// search_tools tool
// ---------------------------------------------------------------------------

describe("createSearchToolsTool", () => {
  let mockClient: GatewayClient;

  beforeEach(() => {
    mockClient = createMockClient();
  });

  describe("tool definition", () => {
    it("should have correct name and description", () => {
      const tool = createSearchToolsTool({ client: mockClient });

      expect(tool.name).toBe("search_tools");
      expect(tool.label).toBe("Search Tools");
      expect(tool.description).toContain("Search for available infrastructure tools");
    });

    it("should have correct parameter schema", () => {
      const tool = createSearchToolsTool({ client: mockClient });

      expect(tool.parameters).toBe(SearchToolsParams);
      expect(tool.parameters.properties.query).toBeDefined();
      expect(tool.parameters.properties.tool_type).toBeDefined();
    });

    it("should have promptGuidelines", () => {
      const tool = createSearchToolsTool({ client: mockClient });

      expect(tool.promptGuidelines).toBeDefined();
      expect(Array.isArray(tool.promptGuidelines)).toBe(true);
      expect(tool.promptGuidelines!.some((g: string) => g.includes("search_tools"))).toBe(true);
    });
  });

  describe("execute handler", () => {
    it("should call GatewayClient.searchTools with query only", async () => {
      const tool = createSearchToolsTool({ client: mockClient });

      const params: SearchToolsInput = { query: "ssh" };
      await tool.execute("tc-s1", params, undefined, undefined);

      expect(mockClient.searchTools).toHaveBeenCalledWith("ssh", undefined);
    });

    it("should call GatewayClient.searchTools with query and tool_type", async () => {
      const tool = createSearchToolsTool({ client: mockClient });

      const params: SearchToolsInput = { query: "metrics", tool_type: "victoriametrics" };
      await tool.execute("tc-s2", params, undefined, undefined);

      expect(mockClient.searchTools).toHaveBeenCalledWith("metrics", "victoriametrics");
    });

    it("should return JSON-stringified search results", async () => {
      const searchResult = {
        tools: [
          {
            name: "ssh.execute_command",
            description: "Execute SSH command",
            instances: [{ id: 1, logical_name: "prod-ssh" }],
          },
        ],
      };
      const client = createMockClient({
        searchTools: vi.fn(async () => searchResult) as any,
      });
      const tool = createSearchToolsTool({ client });

      const result = await tool.execute(
        "tc-s3",
        { query: "ssh" },
        undefined,
        undefined,
      );

      expect(result.content).toHaveLength(1);
      expect(result.content[0].type).toBe("text");
      const parsed = JSON.parse(result.content[0].text);
      expect(parsed.tools).toHaveLength(1);
      expect(parsed.tools[0].name).toBe("ssh.execute_command");
      expect(parsed.tools[0].instances[0].logical_name).toBe("prod-ssh");
    });

    it("should return empty tools array for no matches", async () => {
      const client = createMockClient({
        searchTools: vi.fn(async () => ({ tools: [] })) as any,
      });
      const tool = createSearchToolsTool({ client });

      const result = await tool.execute(
        "tc-s4",
        { query: "nonexistent" },
        undefined,
        undefined,
      );

      const parsed = JSON.parse(result.content[0].text);
      expect(parsed.tools).toHaveLength(0);
    });

    it("should handle errors gracefully", async () => {
      const client = createMockClient({
        searchTools: vi.fn(async () => {
          throw new Error("MCP Error -32000: Gateway unavailable");
        }) as any,
      });
      const tool = createSearchToolsTool({ client });

      const result = await tool.execute(
        "tc-s5",
        { query: "ssh" },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("Gateway unavailable");
    });
  });
});

describe("SearchToolsParams schema", () => {
  it("should require query as string", () => {
    expect(SearchToolsParams.properties.query.type).toBe("string");
  });

  it("should have optional tool_type field", () => {
    expect(SearchToolsParams.properties.tool_type).toBeDefined();
  });
});

// ---------------------------------------------------------------------------
// get_tool_detail tool
// ---------------------------------------------------------------------------

describe("createGetToolDetailTool", () => {
  let mockClient: GatewayClient;

  beforeEach(() => {
    mockClient = createMockClient();
  });

  describe("tool definition", () => {
    it("should have correct name and description", () => {
      const tool = createGetToolDetailTool({ client: mockClient });

      expect(tool.name).toBe("get_tool_detail");
      expect(tool.label).toBe("Get Tool Detail");
      expect(tool.description).toContain("full details");
    });

    it("should have correct parameter schema", () => {
      const tool = createGetToolDetailTool({ client: mockClient });

      expect(tool.parameters).toBe(GetToolDetailParams);
      expect(tool.parameters.properties.tool_name).toBeDefined();
    });

    it("should have promptGuidelines", () => {
      const tool = createGetToolDetailTool({ client: mockClient });

      expect(tool.promptGuidelines).toBeDefined();
      expect(Array.isArray(tool.promptGuidelines)).toBe(true);
      expect(tool.promptGuidelines!.some((g: string) => g.includes("get_tool_detail"))).toBe(true);
    });
  });

  describe("execute handler", () => {
    it("should call GatewayClient.getToolDetail with tool_name", async () => {
      const tool = createGetToolDetailTool({ client: mockClient });

      const params: GetToolDetailInput = { tool_name: "ssh.execute_command" };
      await tool.execute("tc-d1", params, undefined, undefined);

      expect(mockClient.getToolDetail).toHaveBeenCalledWith("ssh.execute_command");
    });

    it("should return JSON-stringified tool detail", async () => {
      const detail = {
        name: "zabbix.get_problems",
        description: "Get active Zabbix problems",
        params: {
          severity_min: { type: "number", description: "Minimum severity" },
          hostids: { type: "array", description: "Filter by host IDs" },
        },
        instances: [
          { id: 1, logical_name: "prod-zabbix" },
          { id: 2, logical_name: "staging-zabbix" },
        ],
      };
      const client = createMockClient({
        getToolDetail: vi.fn(async () => detail) as any,
      });
      const tool = createGetToolDetailTool({ client });

      const result = await tool.execute(
        "tc-d2",
        { tool_name: "zabbix.get_problems" },
        undefined,
        undefined,
      );

      expect(result.content).toHaveLength(1);
      expect(result.content[0].type).toBe("text");
      const parsed = JSON.parse(result.content[0].text);
      expect(parsed.name).toBe("zabbix.get_problems");
      expect(parsed.params.severity_min).toBeDefined();
      expect(parsed.instances).toHaveLength(2);
      expect(parsed.instances[0].logical_name).toBe("prod-zabbix");
    });

    it("should handle errors for unknown tools", async () => {
      const client = createMockClient({
        getToolDetail: vi.fn(async () => {
          throw new Error("MCP Error -32602: Tool not found: nonexistent.tool");
        }) as any,
      });
      const tool = createGetToolDetailTool({ client });

      const result = await tool.execute(
        "tc-d3",
        { tool_name: "nonexistent.tool" },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("Tool not found");
    });

    it("should handle non-Error thrown values", async () => {
      const client = createMockClient({
        getToolDetail: vi.fn(async () => {
          throw "string error from gateway";
        }) as any,
      });
      const tool = createGetToolDetailTool({ client });

      const result = await tool.execute(
        "tc-d4",
        { tool_name: "ssh.execute_command" },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("string error from gateway");
    });
  });
});

describe("GetToolDetailParams schema", () => {
  it("should require tool_name as string", () => {
    expect(GetToolDetailParams.properties.tool_name.type).toBe("string");
  });
});
