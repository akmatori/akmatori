import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  createGatewayCallTool,
  createListToolsForToolTypeTool,
  createGetToolDetailTool,
  createListToolTypesTool,
  createExecuteScriptTool,
  isDotNamespacedToolName,
  formatDirectToolCallHint,
  GatewayCallParams,
  ListToolsForToolTypeParams,
  GetToolDetailParams,
  ExecuteScriptParams,
  type GatewayCallInput,
  type ListToolsForToolTypeInput,
  type GetToolDetailInput,
  type ExecuteScriptInput,
} from "../src/gateway-tools.js";
import type { GatewayClient, CallResult } from "../src/gateway-client.js";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";

// ---------------------------------------------------------------------------
// Mock GatewayClient
// ---------------------------------------------------------------------------

function createMockClient(overrides?: Partial<GatewayClient>): GatewayClient {
  return {
    call: vi.fn(async () => ({ data: { status: "ok" } } as CallResult)),
    listToolsByType: vi.fn(async () => ({ tools: [] })),
    getToolDetail: vi.fn(async () => ({
      name: "ssh.execute_command",
      description: "Execute SSH command",
      params: {},
      instances: [],
    })),
    listToolTypes: vi.fn(async () => ({ types: ["ssh", "zabbix"] })),
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
        undefined,
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
// list_tools_for_tool_type tool
// ---------------------------------------------------------------------------

describe("createListToolsForToolTypeTool", () => {
  let mockClient: GatewayClient;

  beforeEach(() => {
    mockClient = createMockClient();
  });

  describe("tool definition", () => {
    it("should have correct name and description", () => {
      const tool = createListToolsForToolTypeTool({ client: mockClient });

      expect(tool.name).toBe("list_tools_for_tool_type");
      expect(tool.label).toBe("List Tools For Tool Type");
      expect(tool.description).toContain("List available infrastructure tools");
    });

    it("should have correct parameter schema", () => {
      const tool = createListToolsForToolTypeTool({ client: mockClient });

      expect(tool.parameters).toBe(ListToolsForToolTypeParams);
      expect(tool.parameters.properties.tool_type).toBeDefined();
      expect(tool.parameters.properties.query).toBeUndefined();
    });

    it("should have promptGuidelines", () => {
      const tool = createListToolsForToolTypeTool({ client: mockClient });

      expect(tool.promptGuidelines).toBeDefined();
      expect(Array.isArray(tool.promptGuidelines)).toBe(true);
      expect(tool.promptGuidelines!.some((g: string) => g.includes("list_tools_for_tool_type"))).toBe(true);
    });
  });

  describe("execute handler", () => {
    it("should call GatewayClient.listToolsByType with tool_type", async () => {
      const tool = createListToolsForToolTypeTool({ client: mockClient });

      const params: ListToolsForToolTypeInput = { tool_type: "ssh" };
      await tool.execute("tc-s1", params, undefined, undefined);

      expect(mockClient.listToolsByType).toHaveBeenCalledWith("ssh", undefined);
    });

    it("should return JSON-stringified list results", async () => {
      const listResult = {
        tools: [
          {
            name: "ssh.execute_command",
            description: "Execute SSH command",
            instances: ["prod-ssh"],
          },
        ],
      };
      const client = createMockClient({
        listToolsByType: vi.fn(async () => listResult) as any,
      });
      const tool = createListToolsForToolTypeTool({ client });

      const result = await tool.execute(
        "tc-s3",
        { tool_type: "ssh" },
        undefined,
        undefined,
      );

      expect(result.content).toHaveLength(1);
      expect(result.content[0].type).toBe("text");
      const parsed = JSON.parse(result.content[0].text);
      expect(parsed.tools).toHaveLength(1);
      expect(parsed.tools[0].name).toBe("ssh.execute_command");
      expect(parsed.tools[0].instances[0]).toBe("prod-ssh");
    });

    it("should return empty tools array for unknown type", async () => {
      const client = createMockClient({
        listToolsByType: vi.fn(async () => ({ tools: [] })) as any,
      });
      const tool = createListToolsForToolTypeTool({ client });

      const result = await tool.execute(
        "tc-s4",
        { tool_type: "nonexistent" },
        undefined,
        undefined,
      );

      const parsed = JSON.parse(result.content[0].text);
      expect(parsed.tools).toHaveLength(0);
    });

    it("should handle errors gracefully", async () => {
      const client = createMockClient({
        listToolsByType: vi.fn(async () => {
          throw new Error("MCP Error -32000: Gateway unavailable");
        }) as any,
      });
      const tool = createListToolsForToolTypeTool({ client });

      const result = await tool.execute(
        "tc-s5",
        { tool_type: "ssh" },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("Gateway unavailable");
    });
  });
});

describe("ListToolsForToolTypeParams schema", () => {
  it("should require tool_type as string", () => {
    expect(ListToolsForToolTypeParams.properties.tool_type.type).toBe("string");
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

      expect(mockClient.getToolDetail).toHaveBeenCalledWith("ssh.execute_command", undefined);
    });

    it("should return JSON-stringified tool detail", async () => {
      const detail = {
        name: "zabbix.get_problems",
        description: "Get active Zabbix problems",
        input_schema: {
          severity_min: { type: "number", description: "Minimum severity" },
          hostids: { type: "array", description: "Filter by host IDs" },
        },
        instances: [
          { id: 1, logical_name: "prod-zabbix", name: "Production Zabbix" },
          { id: 2, logical_name: "staging-zabbix", name: "Staging Zabbix" },
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
      expect(parsed.input_schema.severity_min).toBeDefined();
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

// ---------------------------------------------------------------------------
// list_tool_types tool
// ---------------------------------------------------------------------------

describe("createListToolTypesTool", () => {
  let mockClient: GatewayClient;

  beforeEach(() => {
    mockClient = createMockClient();
  });

  describe("tool definition", () => {
    it("should have correct name and description", () => {
      const tool = createListToolTypesTool({ client: mockClient });

      expect(tool.name).toBe("list_tool_types");
      expect(tool.label).toBe("List Tool Types");
      expect(tool.description).toContain("available tool types");
    });

    it("should have empty parameter schema", () => {
      const tool = createListToolTypesTool({ client: mockClient });

      expect(tool.parameters).toBeDefined();
      expect(Object.keys(tool.parameters.properties ?? {})).toHaveLength(0);
    });

    it("should have promptGuidelines", () => {
      const tool = createListToolTypesTool({ client: mockClient });

      expect(tool.promptGuidelines).toBeDefined();
      expect(Array.isArray(tool.promptGuidelines)).toBe(true);
      expect(tool.promptGuidelines!.some((g: string) => g.includes("list_tool_types"))).toBe(true);
    });
  });

  describe("execute handler", () => {
    it("should call GatewayClient.listToolTypes", async () => {
      const tool = createListToolTypesTool({ client: mockClient });

      await tool.execute("tc-lt1", {} as Record<string, never>, undefined, undefined);

      expect(mockClient.listToolTypes).toHaveBeenCalledWith(undefined);
    });

    it("should return JSON-stringified types", async () => {
      const client = createMockClient({
        listToolTypes: vi.fn(async () => ({ types: ["ssh", "zabbix", "victoria_metrics"] })) as any,
      });
      const tool = createListToolTypesTool({ client });

      const result = await tool.execute(
        "tc-lt2",
        {} as Record<string, never>,
        undefined,
        undefined,
      );

      expect(result.content).toHaveLength(1);
      expect(result.content[0].type).toBe("text");
      const parsed = JSON.parse(result.content[0].text);
      expect(parsed.types).toEqual(["ssh", "zabbix", "victoria_metrics"]);
    });

    it("should handle errors gracefully", async () => {
      const client = createMockClient({
        listToolTypes: vi.fn(async () => {
          throw new Error("MCP Error -32000: Gateway unavailable");
        }) as any,
      });
      const tool = createListToolTypesTool({ client });

      const result = await tool.execute(
        "tc-lt3",
        {} as Record<string, never>,
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("Gateway unavailable");
    });
  });
});

// ---------------------------------------------------------------------------
// execute_script tool
// ---------------------------------------------------------------------------

describe("createExecuteScriptTool", () => {
  let mockClient: GatewayClient;
  let tmpDir: string;

  beforeEach(() => {
    mockClient = createMockClient();
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "exec-script-tool-test-"));
  });

  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  describe("tool definition", () => {
    it("should have correct name and description", () => {
      const tool = createExecuteScriptTool({ client: mockClient, workDir: tmpDir });

      expect(tool.name).toBe("execute_script");
      expect(tool.label).toBe("Execute Script");
      expect(tool.description).toContain("isolated sandbox");
    });

    it("should have correct parameter schema", () => {
      const tool = createExecuteScriptTool({ client: mockClient, workDir: tmpDir });

      expect(tool.parameters).toBe(ExecuteScriptParams);
      expect(tool.parameters.properties.code).toBeDefined();
    });

    it("should have promptGuidelines", () => {
      const tool = createExecuteScriptTool({ client: mockClient, workDir: tmpDir });

      expect(tool.promptGuidelines).toBeDefined();
      expect(Array.isArray(tool.promptGuidelines)).toBe(true);
      expect(tool.promptGuidelines!.some((g: string) => g.includes("execute_script"))).toBe(true);
    });
  });

  describe("execute handler", () => {
    it("should execute script and return result", async () => {
      const tool = createExecuteScriptTool({ client: mockClient, workDir: tmpDir });

      const result = await tool.execute(
        "tc-es1",
        { code: 'return "hello from script"' },
        undefined,
        undefined,
      );

      expect(result.content).toHaveLength(1);
      expect(result.content[0].type).toBe("text");
      expect(result.content[0].text).toBe("hello from script");
    });

    it("should execute script with gateway_call", async () => {
      const client = createMockClient({
        call: vi.fn(async () => ({ data: { hosts: ["web-01"] } })) as any,
      });
      const tool = createExecuteScriptTool({ client, workDir: tmpDir });

      const result = await tool.execute(
        "tc-es2",
        { code: 'const r = await gateway_call("zabbix.get_hosts", {}); return r;' },
        undefined,
        undefined,
      );

      expect(client.call).toHaveBeenCalledWith("zabbix.get_hosts", {}, undefined, expect.any(AbortSignal));
      const parsed = JSON.parse(result.content[0].text);
      expect(parsed.hosts).toEqual(["web-01"]);
    });

    it("should include console output alongside return value", async () => {
      const tool = createExecuteScriptTool({ client: mockClient, workDir: tmpDir });

      const result = await tool.execute(
        "tc-es3",
        { code: 'console.log("debug info"); return "result value"' },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("result value");
      expect(result.content[0].text).toContain("--- Console Output ---");
      expect(result.content[0].text).toContain("debug info");
    });

    it("should handle script errors gracefully", async () => {
      const tool = createExecuteScriptTool({ client: mockClient, workDir: tmpDir });

      const result = await tool.execute(
        "tc-es4",
        { code: 'throw new Error("something broke")' },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("something broke");
    });

    it("should handle syntax errors gracefully", async () => {
      const tool = createExecuteScriptTool({ client: mockClient, workDir: tmpDir });

      const result = await tool.execute(
        "tc-es5",
        { code: 'return {{{' },
        undefined,
        undefined,
      );

      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("compilation error");
    });
  });
});

// ---------------------------------------------------------------------------
// Direct tool call detection helpers
// ---------------------------------------------------------------------------

describe("isDotNamespacedToolName", () => {
  it("should return true for known tool namespaces", () => {
    expect(isDotNamespacedToolName("ssh.execute_command")).toBe(true);
    expect(isDotNamespacedToolName("victoria_metrics.instant_query")).toBe(true);
    expect(isDotNamespacedToolName("zabbix.get_problems")).toBe(true);
    expect(isDotNamespacedToolName("qmd.query")).toBe(true);
    expect(isDotNamespacedToolName("http_connector.some_tool")).toBe(true);
  });

  it("should return true for dynamic HTTP connector tool types", () => {
    expect(isDotNamespacedToolName("internal-billing.get_invoice")).toBe(true);
    expect(isDotNamespacedToolName("company-api.list_users")).toBe(true);
    expect(isDotNamespacedToolName("custom_service.health_check")).toBe(true);
  });

  it("should return false for non-namespaced names", () => {
    expect(isDotNamespacedToolName("gateway_call")).toBe(false);
    expect(isDotNamespacedToolName("execute_script")).toBe(false);
    expect(isDotNamespacedToolName("list_tool_types")).toBe(false);
  });

  it("should return false for empty or invalid inputs", () => {
    expect(isDotNamespacedToolName("")).toBe(false);
    expect(isDotNamespacedToolName(".")).toBe(false);
    expect(isDotNamespacedToolName(".leading_dot")).toBe(false);
    expect(isDotNamespacedToolName("trailing_dot.")).toBe(false);
    expect(isDotNamespacedToolName("has space.tool")).toBe(false);
  });

  it("should return true for any valid identifier namespace (permissive for dynamic tools)", () => {
    // These match the identifier pattern and are accepted — the hint is
    // best-effort and false positives in error messages are harmless.
    expect(isDotNamespacedToolName("db.prod")).toBe(true);
    expect(isDotNamespacedToolName("config.yaml")).toBe(true);
    expect(isDotNamespacedToolName("host.get")).toBe(true);
  });

  it("should return true for multi-segment proxy tool names", () => {
    expect(isDotNamespacedToolName("ext.github.create_issue")).toBe(true);
    expect(isDotNamespacedToolName("ext.db.query")).toBe(true);
  });

  it("should return false for namespaces starting with digits or special chars", () => {
    expect(isDotNamespacedToolName("123.tool")).toBe(false);
    expect(isDotNamespacedToolName("@scope.tool")).toBe(false);
  });
});

describe("formatDirectToolCallHint", () => {
  it("should return a hint for unquoted tool names (real gateway error format)", () => {
    const hint = formatDirectToolCallHint("Tool not found: victoria_metrics.instant_query");
    expect(hint).toContain("victoria_metrics.instant_query");
    expect(hint).toContain("gateway_call");
    expect(hint).toContain("is a gateway tool");
  });

  it("should not hint on authorization errors (no routing error pattern)", () => {
    expect(formatDirectToolCallHint("Unauthorized: incident abc is not authorized to use tool ssh.execute_command")).toBe("");
    expect(formatDirectToolCallHint("MCP Error -32600: Unauthorized: not authorized to use tool victoria_metrics.instant_query")).toBe("");
    expect(formatDirectToolCallHint("Forbidden: access denied for zabbix.get_problems")).toBe("");
  });

  it("should work with quoted tool names in not-found error messages", () => {
    const hint = formatDirectToolCallHint("Tool not found: 'zabbix.get_problems'");
    expect(hint).toContain("zabbix.get_problems");
    expect(hint).toContain("gateway_call");
  });

  it("should return empty string when no routing error pattern is present", () => {
    expect(formatDirectToolCallHint("Connection refused")).toBe("");
    expect(formatDirectToolCallHint("Timeout error")).toBe("");
    expect(formatDirectToolCallHint("Generic error message")).toBe("");
  });

  it("should return empty string for general execution errors with dot-like words", () => {
    // These contain dot-namespaced words but are NOT tool routing errors
    expect(formatDirectToolCallHint("error in node.js version 18")).toBe("");
    expect(formatDirectToolCallHint("process.exit called unexpectedly")).toBe("");
    expect(formatDirectToolCallHint("failed to parse config.yaml")).toBe("");
  });

  it("should not return a hint for registered tool names", () => {
    // These are the 5 registered tools - should not trigger a hint
    expect(formatDirectToolCallHint("Error with 'gateway_call'")).toBe("");
  });

  it("should return a hint for dynamic HTTP connector tool names", () => {
    const hint = formatDirectToolCallHint("Tool not found: internal-billing.get_invoice");
    expect(hint).toContain("internal-billing.get_invoice");
    expect(hint).toContain("gateway_call");
  });

  it("should not return a hint for non-routing errors even with dot patterns", () => {
    expect(formatDirectToolCallHint("Connection refused: 10.0.0.1:5432")).toBe("");
    expect(formatDirectToolCallHint("error in node.js version 18")).toBe("");
  });

  it("should correctly extract multi-segment proxy tool names", () => {
    const hint = formatDirectToolCallHint("Tool not found: ext.github.create_issue");
    expect(hint).toContain("ext.github.create_issue");
    expect(hint).toContain("gateway_call");
    // Must NOT suggest the partial name "ext.github"
    expect(hint).not.toContain('gateway_call("ext.github"');
  });

  it("should handle quoted multi-segment proxy tool names", () => {
    const hint = formatDirectToolCallHint("Unknown tool 'ext.db.query'");
    expect(hint).toContain("ext.db.query");
    expect(hint).toContain("gateway_call");
  });

  it("should correctly extract multi-segment proxy names with hyphens in sub-segments", () => {
    const hint = formatDirectToolCallHint("Tool not found: ext.github-enterprise.create_issue");
    expect(hint).toContain("ext.github-enterprise.create_issue");
    expect(hint).toContain("gateway_call");
    // Must NOT truncate to "ext.github"
    expect(hint).not.toContain('gateway_call("ext.github"');
  });

  it("should match pi-agent-core SDK error format: 'Tool <name> not found'", () => {
    // pi-agent-core generates "Tool ${toolCall.name} not found" at agent-loop.js:327
    // when the agent tries to call a tool that isn't registered
    const hint = formatDirectToolCallHint("Tool ssh.execute_command not found");
    expect(hint).toContain("ssh.execute_command");
    expect(hint).toContain("gateway_call");

    const hint2 = formatDirectToolCallHint("Tool victoria_metrics.instant_query not found");
    expect(hint2).toContain("victoria_metrics.instant_query");
    expect(hint2).toContain("gateway_call");
  });

  it("should match JSON-RPC error codes in error messages", () => {
    // The regex should match -32601 (method not found) preceded by non-word chars
    const hint = formatDirectToolCallHint("MCP Error -32601: victoria_metrics.instant_query");
    expect(hint).toContain("victoria_metrics.instant_query");
    expect(hint).toContain("gateway_call");
  });

  it("should not return a hint for generic 'not found' errors with dot-like filenames", () => {
    // "File not found" and "Module not found" are NOT tool routing errors
    // even though they contain "not found" and a dot-namespaced identifier
    expect(formatDirectToolCallHint("File not found: config.yaml")).toBe("");
    expect(formatDirectToolCallHint("Module not found: node.js")).toBe("");
    expect(formatDirectToolCallHint("Command not found: db.migrate")).toBe("");
    expect(formatDirectToolCallHint("Package not found: lodash.debounce")).toBe("");
  });
});

describe("gateway_call error messages suppress direct tool hints", () => {
  it("should NOT include gateway_call hint even for tool-not-found errors (agent is already using gateway_call)", async () => {
    const client = createMockClient({
      call: vi.fn(async () => {
        throw new Error("MCP Error -32601: Method not found: victoria_metrics.instant_query");
      }) as any,
    });
    const tool = createGatewayCallTool({ client });

    const result = await tool.execute(
      "tc-hint1",
      { tool_name: "victoria_metrics.instant_query", args: {} },
      undefined,
      undefined,
    );

    expect(result.content[0].text).toContain("Error:");
    expect(result.content[0].text).toContain("Method not found");
    // Hint should NOT be present — agent is already using gateway_call
    expect(result.content[0].text).not.toContain("Hint:");
  });

  it("should not include hint for authorization errors", async () => {
    const client = createMockClient({
      call: vi.fn(async () => {
        throw new Error("MCP Error -32600: Unauthorized: incident test is not authorized to use tool victoria_metrics.instant_query");
      }) as any,
    });
    const tool = createGatewayCallTool({ client });

    const result = await tool.execute(
      "tc-hint2",
      { tool_name: "victoria_metrics.instant_query", args: {} },
      undefined,
      undefined,
    );

    expect(result.content[0].text).toContain("Error:");
    expect(result.content[0].text).toContain("is not authorized");
    expect(result.content[0].text).not.toContain("Hint:");
  });

  it("should not include hint for connection errors", async () => {
    const client = createMockClient({
      call: vi.fn(async () => {
        throw new Error("MCP Error -32000: Connection refused");
      }) as any,
    });
    const tool = createGatewayCallTool({ client });

    const result = await tool.execute(
      "tc-hint3",
      { tool_name: "ssh.execute_command", args: { command: "uptime" } },
      undefined,
      undefined,
    );

    expect(result.content[0].text).toContain("Error:");
    expect(result.content[0].text).toContain("Connection refused");
    expect(result.content[0].text).not.toContain("Hint:");
  });
});

describe("ExecuteScriptParams schema", () => {
  it("should require code as string", () => {
    expect(ExecuteScriptParams.properties.code.type).toBe("string");
  });
});
