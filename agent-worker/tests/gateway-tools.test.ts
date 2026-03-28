import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  createGatewayCallTool,
  createListToolsForToolTypeTool,
  createGetToolDetailTool,
  createListToolTypesTool,
  createExecuteScriptTool,
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

    it("should have promptSnippet for system prompt inclusion (required since pi-mono 0.59.0)", () => {
      const tool = createGatewayCallTool({ client: mockClient });

      expect(tool.promptSnippet).toBeDefined();
      expect(typeof tool.promptSnippet).toBe("string");
      expect(tool.promptSnippet!.length).toBeGreaterThan(0);
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

    it("should have promptSnippet for system prompt inclusion (required since pi-mono 0.59.0)", () => {
      const tool = createListToolsForToolTypeTool({ client: mockClient });

      expect(tool.promptSnippet).toBeDefined();
      expect(typeof tool.promptSnippet).toBe("string");
      expect(tool.promptSnippet!.length).toBeGreaterThan(0);
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

    it("should have promptSnippet for system prompt inclusion (required since pi-mono 0.59.0)", () => {
      const tool = createGetToolDetailTool({ client: mockClient });

      expect(tool.promptSnippet).toBeDefined();
      expect(typeof tool.promptSnippet).toBe("string");
      expect(tool.promptSnippet!.length).toBeGreaterThan(0);
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

    it("should have promptSnippet for system prompt inclusion (required since pi-mono 0.59.0)", () => {
      const tool = createListToolTypesTool({ client: mockClient });

      expect(tool.promptSnippet).toBeDefined();
      expect(typeof tool.promptSnippet).toBe("string");
      expect(tool.promptSnippet!.length).toBeGreaterThan(0);
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

    it("should have promptSnippet for system prompt inclusion (required since pi-mono 0.59.0)", () => {
      const tool = createExecuteScriptTool({ client: mockClient, workDir: tmpDir });

      expect(tool.promptSnippet).toBeDefined();
      expect(typeof tool.promptSnippet).toBe("string");
      expect(tool.promptSnippet!.length).toBeGreaterThan(0);
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
// AbortSignal cancellation propagation (pi-mono 0.63.1 ctx.signal)
// ---------------------------------------------------------------------------

describe("cancellation propagation via AbortSignal", () => {
  describe("gateway_call", () => {
    it("should forward AbortSignal to GatewayClient.call", async () => {
      const controller = new AbortController();
      const mockClient = createMockClient();
      const tool = createGatewayCallTool({ client: mockClient });

      await tool.execute(
        "tc-sig1",
        { tool_name: "ssh.execute_command", args: { command: "uptime" } },
        controller.signal,
        undefined,
      );

      expect(mockClient.call).toHaveBeenCalledWith(
        "ssh.execute_command",
        { command: "uptime" },
        undefined,
        controller.signal,
      );
    });

    it("should abort in-flight gateway_call when signal fires", async () => {
      const controller = new AbortController();
      const mockClient = createMockClient({
        call: vi.fn(async (_name, _args, _instance, signal?: AbortSignal) => {
          // Simulate a long-running call that respects the signal
          return new Promise<CallResult>((resolve, reject) => {
            if (signal?.aborted) {
              reject(new Error("Request aborted"));
              return;
            }
            const onAbort = () => reject(new Error("Request aborted"));
            signal?.addEventListener("abort", onAbort, { once: true });
            // Resolve after a delay (if not aborted)
            setTimeout(() => {
              signal?.removeEventListener("abort", onAbort);
              resolve({ data: "should not reach" });
            }, 5000);
          });
        }) as any,
      });
      const tool = createGatewayCallTool({ client: mockClient });

      // Start execution, then abort immediately
      const resultPromise = tool.execute(
        "tc-sig2",
        { tool_name: "ssh.execute_command", args: { command: "sleep 60" } },
        controller.signal,
        undefined,
      );
      controller.abort();

      const result = await resultPromise;
      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("aborted");
    });

    it("should handle already-aborted signal", async () => {
      const controller = new AbortController();
      controller.abort(); // Abort before calling

      const mockClient = createMockClient({
        call: vi.fn(async (_name, _args, _instance, signal?: AbortSignal) => {
          if (signal?.aborted) {
            throw new Error("Request aborted");
          }
          return { data: "should not reach" } as CallResult;
        }) as any,
      });
      const tool = createGatewayCallTool({ client: mockClient });

      const result = await tool.execute(
        "tc-sig3",
        { tool_name: "ssh.execute_command", args: {} },
        controller.signal,
        undefined,
      );

      expect(result.content[0].text).toContain("Error:");
      expect(result.content[0].text).toContain("aborted");
    });
  });

  describe("list_tools_for_tool_type", () => {
    it("should forward AbortSignal to GatewayClient.listToolsByType", async () => {
      const controller = new AbortController();
      const mockClient = createMockClient();
      const tool = createListToolsForToolTypeTool({ client: mockClient });

      await tool.execute("tc-sig4", { tool_type: "ssh" }, controller.signal, undefined);

      expect(mockClient.listToolsByType).toHaveBeenCalledWith("ssh", controller.signal);
    });
  });

  describe("get_tool_detail", () => {
    it("should forward AbortSignal to GatewayClient.getToolDetail", async () => {
      const controller = new AbortController();
      const mockClient = createMockClient();
      const tool = createGetToolDetailTool({ client: mockClient });

      await tool.execute("tc-sig5", { tool_name: "ssh.execute_command" }, controller.signal, undefined);

      expect(mockClient.getToolDetail).toHaveBeenCalledWith("ssh.execute_command", controller.signal);
    });
  });

  describe("list_tool_types", () => {
    it("should forward AbortSignal to GatewayClient.listToolTypes", async () => {
      const controller = new AbortController();
      const mockClient = createMockClient();
      const tool = createListToolTypesTool({ client: mockClient });

      await tool.execute("tc-sig6", {} as Record<string, never>, controller.signal, undefined);

      expect(mockClient.listToolTypes).toHaveBeenCalledWith(controller.signal);
    });
  });

  describe("execute_script", () => {
    it("should forward AbortSignal to script executor", async () => {
      const controller = new AbortController();
      const mockClient = createMockClient();
      const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "exec-sig-test-"));

      try {
        const tool = createExecuteScriptTool({ client: mockClient, workDir: tmpDir });

        const result = await tool.execute(
          "tc-sig7",
          { code: 'return "hello"' },
          controller.signal,
          undefined,
        );

        expect(result.content[0].text).toBe("hello");
      } finally {
        fs.rmSync(tmpDir, { recursive: true, force: true });
      }
    });
  });
});

describe("ExecuteScriptParams schema", () => {
  it("should require code as string", () => {
    expect(ExecuteScriptParams.properties.code.type).toBe("string");
  });
});
