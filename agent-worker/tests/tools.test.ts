import { describe, it, expect, beforeEach, afterEach, vi, beforeAll, afterAll } from "vitest";
import { createServer, type IncomingMessage, type ServerResponse, type Server } from "http";
import { MCPClient, MCPError, type JsonRpcRequest } from "../src/tools/mcp-client.js";
import { createMCPTools } from "../src/tools/mcp-tools.js";

// ---------------------------------------------------------------------------
// Mock HTTP server for MCP Gateway
// ---------------------------------------------------------------------------

interface MockServerState {
  server: Server;
  port: number;
  url: string;
  requests: Array<{ headers: Record<string, string | undefined>; body: JsonRpcRequest }>;
  responseHandler: (req: JsonRpcRequest) => object;
}

function createMockMCPServer(): Promise<MockServerState> {
  return new Promise((resolve) => {
    const requests: MockServerState["requests"] = [];
    let responseHandler: MockServerState["responseHandler"] = () => ({
      jsonrpc: "2.0",
      id: 1,
      result: { content: [{ type: "text", text: "ok" }], isError: false },
    });

    const server = createServer((req: IncomingMessage, res: ServerResponse) => {
      let body = "";
      req.on("data", (chunk: Buffer) => {
        body += chunk.toString();
      });
      req.on("end", () => {
        const parsed = JSON.parse(body) as JsonRpcRequest;
        requests.push({
          headers: {
            "content-type": req.headers["content-type"],
            "x-incident-id": req.headers["x-incident-id"] as string | undefined,
          },
          body: parsed,
        });

        const response = responseHandler(parsed);
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify(response));
      });
    });

    server.listen(0, "127.0.0.1", () => {
      const addr = server.address();
      const port = typeof addr === "object" ? addr!.port : 0;
      const url = `http://127.0.0.1:${port}`;
      resolve({
        server,
        port,
        url,
        requests,
        get responseHandler() {
          return responseHandler;
        },
        set responseHandler(h) {
          responseHandler = h;
        },
      });
    });
  });
}

function closeServer(state: MockServerState): Promise<void> {
  return new Promise((resolve) => {
    state.server.close(() => resolve());
  });
}

// ===========================================================================
// MCPClient tests
// ===========================================================================

describe("MCPClient", () => {
  let mock: MockServerState;

  beforeAll(async () => {
    mock = await createMockMCPServer();
  });

  afterAll(async () => {
    await closeServer(mock);
  });

  beforeEach(() => {
    mock.requests.length = 0;
  });

  // -----------------------------------------------------------------------
  // Successful tool calls
  // -----------------------------------------------------------------------

  describe("callTool", () => {
    it("should send JSON-RPC 2.0 request to /mcp endpoint", async () => {
      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: {
          content: [{ type: "text", text: '{"status": "success"}' }],
          isError: false,
        },
      });

      const client = new MCPClient({
        gatewayUrl: mock.url,
        incidentId: "inc-123",
      });

      const result = await client.callTool("ssh.execute_command", {
        command: "uptime",
      });

      expect(result).toBe('{"status": "success"}');
      expect(mock.requests).toHaveLength(1);

      const req = mock.requests[0];
      expect(req.body.jsonrpc).toBe("2.0");
      expect(req.body.method).toBe("tools/call");
      expect(req.body.params.name).toBe("ssh.execute_command");
      expect(req.body.params.arguments).toEqual({ command: "uptime" });
    });

    it("should set X-Incident-ID header", async () => {
      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: { content: [{ type: "text", text: "ok" }] },
      });

      const client = new MCPClient({
        gatewayUrl: mock.url,
        incidentId: "incident-abc-456",
      });

      await client.callTool("ssh.test_connectivity", {});

      expect(mock.requests[0].headers["x-incident-id"]).toBe("incident-abc-456");
    });

    it("should set Content-Type header to application/json", async () => {
      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: { content: [{ type: "text", text: "ok" }] },
      });

      const client = new MCPClient({
        gatewayUrl: mock.url,
        incidentId: "inc-1",
      });

      await client.callTool("ssh.test_connectivity", {});

      expect(mock.requests[0].headers["content-type"]).toBe("application/json");
    });

    it("should increment request IDs", async () => {
      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: { content: [{ type: "text", text: "ok" }] },
      });

      const client = new MCPClient({
        gatewayUrl: mock.url,
        incidentId: "inc-1",
      });

      await client.callTool("tool1", {});
      await client.callTool("tool2", {});
      await client.callTool("tool3", {});

      expect(mock.requests[0].body.id).toBe(1);
      expect(mock.requests[1].body.id).toBe(2);
      expect(mock.requests[2].body.id).toBe(3);
    });

    it("should concatenate multiple text content items", async () => {
      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: {
          content: [
            { type: "text", text: "line 1" },
            { type: "text", text: "line 2" },
          ],
        },
      });

      const client = new MCPClient({
        gatewayUrl: mock.url,
        incidentId: "inc-1",
      });

      const result = await client.callTool("some.tool", {});
      expect(result).toBe("line 1\nline 2");
    });

    it("should handle empty arguments", async () => {
      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: { content: [{ type: "text", text: "ok" }] },
      });

      const client = new MCPClient({
        gatewayUrl: mock.url,
        incidentId: "inc-1",
      });

      await client.callTool("ssh.test_connectivity");

      expect(mock.requests[0].body.params.arguments).toEqual({});
    });

    it("should strip trailing slashes from gateway URL", async () => {
      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: { content: [{ type: "text", text: "ok" }] },
      });

      const client = new MCPClient({
        gatewayUrl: mock.url + "///",
        incidentId: "inc-1",
      });

      // Should not throw - trailing slashes stripped
      const result = await client.callTool("test.tool", {});
      expect(result).toBe("ok");
    });
  });

  // -----------------------------------------------------------------------
  // Error handling
  // -----------------------------------------------------------------------

  describe("error handling", () => {
    it("should throw MCPError on JSON-RPC error response", async () => {
      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        error: {
          code: -32601,
          message: "Method not found",
        },
      });

      const client = new MCPClient({
        gatewayUrl: mock.url,
        incidentId: "inc-1",
      });

      await expect(client.callTool("nonexistent.tool", {})).rejects.toThrow(
        MCPError,
      );
      await expect(client.callTool("nonexistent.tool", {})).rejects.toThrow(
        "Method not found",
      );
    });

    it("should throw MCPError when result has isError=true", async () => {
      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: {
          content: [{ type: "text", text: "SSH connection refused" }],
          isError: true,
        },
      });

      const client = new MCPClient({
        gatewayUrl: mock.url,
        incidentId: "inc-1",
      });

      await expect(
        client.callTool("ssh.execute_command", { command: "uptime" }),
      ).rejects.toThrow("SSH connection refused");
    });

    it("should include error code in MCPError", async () => {
      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        error: {
          code: -32602,
          message: "Invalid params",
          data: { param: "command" },
        },
      });

      const client = new MCPClient({
        gatewayUrl: mock.url,
        incidentId: "inc-1",
      });

      try {
        await client.callTool("ssh.execute_command", {});
        expect.fail("Should have thrown");
      } catch (err) {
        expect(err).toBeInstanceOf(MCPError);
        expect((err as MCPError).code).toBe(-32602);
        expect((err as MCPError).data).toEqual({ param: "command" });
      }
    });

    it("should throw MCPError on network error", async () => {
      const client = new MCPClient({
        gatewayUrl: "http://127.0.0.1:59997",
        incidentId: "inc-1",
        timeoutMs: 1000,
      });

      await expect(client.callTool("ssh.test_connectivity", {})).rejects.toThrow(
        MCPError,
      );
    });

    it("should throw MCPError on timeout", async () => {
      // Create a server that never responds
      const slowServer = createServer((_req, _res) => {
        // intentionally do nothing - let it timeout
      });

      await new Promise<void>((resolve) => {
        slowServer.listen(0, "127.0.0.1", () => resolve());
      });

      const addr = slowServer.address();
      const port = typeof addr === "object" ? addr!.port : 0;

      const client = new MCPClient({
        gatewayUrl: `http://127.0.0.1:${port}`,
        incidentId: "inc-1",
        timeoutMs: 100,
      });

      await expect(client.callTool("slow.tool", {})).rejects.toThrow(
        /timed out/,
      );

      await new Promise<void>((resolve) => {
        slowServer.close(() => resolve());
      });
    });

    it("should throw MCPError on HTTP error status", async () => {
      const errorServer = createServer((_req, res) => {
        res.writeHead(500, { "Content-Type": "text/plain" });
        res.end("Internal Server Error");
      });

      await new Promise<void>((resolve) => {
        errorServer.listen(0, "127.0.0.1", () => resolve());
      });

      const addr = errorServer.address();
      const port = typeof addr === "object" ? addr!.port : 0;

      const client = new MCPClient({
        gatewayUrl: `http://127.0.0.1:${port}`,
        incidentId: "inc-1",
      });

      await expect(client.callTool("some.tool", {})).rejects.toThrow(
        /HTTP 500/,
      );

      await new Promise<void>((resolve) => {
        errorServer.close(() => resolve());
      });
    });
  });
});

// ===========================================================================
// MCP Tools tests
// ===========================================================================

describe("createMCPTools", () => {
  let mock: MockServerState;

  beforeAll(async () => {
    mock = await createMockMCPServer();
  });

  afterAll(async () => {
    await closeServer(mock);
  });

  beforeEach(() => {
    mock.requests.length = 0;
    mock.responseHandler = (req) => ({
      jsonrpc: "2.0",
      id: req.id,
      result: {
        content: [{ type: "text", text: '{"status": "ok"}' }],
        isError: false,
      },
    });
  });

  // -----------------------------------------------------------------------
  // Tool creation
  // -----------------------------------------------------------------------

  it("should create all expected tools", () => {
    const tools = createMCPTools(mock.url, "inc-1");

    const toolNames = tools.map((t) => t.name);
    expect(toolNames).toContain("ssh_execute_command");
    expect(toolNames).toContain("ssh_test_connectivity");
    expect(toolNames).toContain("ssh_get_server_info");
    expect(toolNames).toContain("zabbix_get_hosts");
    expect(toolNames).toContain("zabbix_get_problems");
    expect(toolNames).toContain("zabbix_get_history");
    expect(toolNames).toContain("zabbix_get_items_batch");
    expect(toolNames).toContain("zabbix_acknowledge_event");
  });

  it("should have 8 tools total", () => {
    const tools = createMCPTools(mock.url, "inc-1");
    expect(tools).toHaveLength(8);
  });

  it("should give every tool a name, label, and description", () => {
    const tools = createMCPTools(mock.url, "inc-1");

    for (const tool of tools) {
      expect(tool.name).toBeTruthy();
      expect(tool.label).toBeTruthy();
      expect(tool.description).toBeTruthy();
      expect(tool.description.length).toBeGreaterThan(10);
    }
  });

  it("should give every tool a parameters schema", () => {
    const tools = createMCPTools(mock.url, "inc-1");

    for (const tool of tools) {
      expect(tool.parameters).toBeDefined();
      expect(tool.parameters.type).toBe("object");
    }
  });

  // -----------------------------------------------------------------------
  // SSH tool execution
  // -----------------------------------------------------------------------

  describe("ssh_execute_command", () => {
    it("should call ssh.execute_command on MCP Gateway", async () => {
      const tools = createMCPTools(mock.url, "inc-ssh");
      const sshTool = tools.find((t) => t.name === "ssh_execute_command")!;

      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: {
          content: [
            {
              type: "text",
              text: JSON.stringify({
                results: [
                  {
                    server: "web-01",
                    success: true,
                    stdout: " 14:30:00 up 30 days",
                    stderr: "",
                    exit_code: 0,
                    duration_ms: 120,
                  },
                ],
                summary: { total: 1, succeeded: 1, failed: 0 },
              }),
            },
          ],
        },
      });

      const result = await sshTool.execute(
        "call-1",
        { command: "uptime" } as any,
        undefined,
        undefined,
        {} as any,
      );

      expect(result.content).toHaveLength(1);
      expect(result.content[0].type).toBe("text");
      const parsed = JSON.parse((result.content[0] as any).text);
      expect(parsed.results[0].server).toBe("web-01");
      expect(parsed.results[0].stdout).toContain("up 30 days");

      // Verify MCP Gateway received correct request
      expect(mock.requests).toHaveLength(1);
      expect(mock.requests[0].body.params.name).toBe("ssh.execute_command");
      expect(mock.requests[0].body.params.arguments).toEqual({
        command: "uptime",
      });
      expect(mock.requests[0].headers["x-incident-id"]).toBe("inc-ssh");
    });

    it("should handle SSH connection error", async () => {
      const tools = createMCPTools(mock.url, "inc-ssh-err");
      const sshTool = tools.find((t) => t.name === "ssh_execute_command")!;

      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: {
          content: [
            { type: "text", text: "Connection refused: no SSH credentials configured" },
          ],
          isError: true,
        },
      });

      await expect(
        sshTool.execute("call-2", { command: "ls" } as any, undefined, undefined, {} as any),
      ).rejects.toThrow("Connection refused");
    });
  });

  describe("ssh_test_connectivity", () => {
    it("should call ssh.test_connectivity with no arguments", async () => {
      const tools = createMCPTools(mock.url, "inc-conn");
      const tool = tools.find((t) => t.name === "ssh_test_connectivity")!;

      await tool.execute("call-3", {} as any, undefined, undefined, {} as any);

      expect(mock.requests[0].body.params.name).toBe("ssh.test_connectivity");
      expect(mock.requests[0].body.params.arguments).toEqual({});
    });
  });

  describe("ssh_get_server_info", () => {
    it("should pass servers parameter to ssh.get_server_info", async () => {
      const tools = createMCPTools(mock.url, "inc-info");
      const tool = tools.find((t) => t.name === "ssh_get_server_info")!;

      await tool.execute(
        "call-4",
        { servers: ["web-01", "db-01"] } as any,
        undefined,
        undefined,
        {} as any,
      );

      expect(mock.requests[0].body.params.name).toBe("ssh.get_server_info");
      expect(mock.requests[0].body.params.arguments).toEqual({
        servers: ["web-01", "db-01"],
      });
    });
  });

  // -----------------------------------------------------------------------
  // Zabbix tool execution
  // -----------------------------------------------------------------------

  describe("zabbix_get_hosts", () => {
    it("should call zabbix.get_hosts with search and filter", async () => {
      const tools = createMCPTools(mock.url, "inc-zabbix");
      const tool = tools.find((t) => t.name === "zabbix_get_hosts")!;

      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: {
          content: [
            {
              type: "text",
              text: JSON.stringify([
                { hostid: "10084", host: "web-01", name: "Web Server 01" },
              ]),
            },
          ],
        },
      });

      const result = await tool.execute(
        "call-5",
        {
          search: { name: "web" },
          filter: { status: "0" },
          limit: 10,
        } as any,
        undefined,
        undefined,
        {} as any,
      );

      expect(result.content).toHaveLength(1);
      const parsed = JSON.parse((result.content[0] as any).text);
      expect(parsed[0].host).toBe("web-01");

      expect(mock.requests[0].body.params.name).toBe("zabbix.get_hosts");
      expect(mock.requests[0].body.params.arguments).toEqual({
        search: { name: "web" },
        filter: { status: "0" },
        limit: 10,
      });
    });
  });

  describe("zabbix_get_problems", () => {
    it("should call zabbix.get_problems with severity filter", async () => {
      const tools = createMCPTools(mock.url, "inc-problems");
      const tool = tools.find((t) => t.name === "zabbix_get_problems")!;

      await tool.execute(
        "call-6",
        { recent: true, severity_min: 3 } as any,
        undefined,
        undefined,
        {} as any,
      );

      expect(mock.requests[0].body.params.name).toBe("zabbix.get_problems");
      expect(mock.requests[0].body.params.arguments).toEqual({
        recent: true,
        severity_min: 3,
      });
    });
  });

  describe("zabbix_get_history", () => {
    it("should call zabbix.get_history with item IDs", async () => {
      const tools = createMCPTools(mock.url, "inc-history");
      const tool = tools.find((t) => t.name === "zabbix_get_history")!;

      await tool.execute(
        "call-7",
        { itemids: [31337, 31338], history_type: 0, limit: 100 } as any,
        undefined,
        undefined,
        {} as any,
      );

      expect(mock.requests[0].body.params.name).toBe("zabbix.get_history");
      expect(mock.requests[0].body.params.arguments).toEqual({
        itemids: [31337, 31338],
        history_type: 0,
        limit: 100,
      });
    });
  });

  describe("zabbix_get_items_batch", () => {
    it("should call zabbix.get_items_batch with search patterns", async () => {
      const tools = createMCPTools(mock.url, "inc-batch");
      const tool = tools.find((t) => t.name === "zabbix_get_items_batch")!;

      await tool.execute(
        "call-8",
        {
          searches: ["cpu", "memory", "disk"],
          hostids: ["10084"],
        } as any,
        undefined,
        undefined,
        {} as any,
      );

      expect(mock.requests[0].body.params.name).toBe("zabbix.get_items_batch");
      expect(mock.requests[0].body.params.arguments).toEqual({
        searches: ["cpu", "memory", "disk"],
        hostids: ["10084"],
      });
    });
  });

  describe("zabbix_acknowledge_event", () => {
    it("should call zabbix.acknowledge_event with event IDs and message", async () => {
      const tools = createMCPTools(mock.url, "inc-ack");
      const tool = tools.find((t) => t.name === "zabbix_acknowledge_event")!;

      await tool.execute(
        "call-9",
        {
          eventids: ["12345", "12346"],
          message: "Investigating - automated by Akmatori",
        } as any,
        undefined,
        undefined,
        {} as any,
      );

      expect(mock.requests[0].body.params.name).toBe("zabbix.acknowledge_event");
      expect(mock.requests[0].body.params.arguments).toEqual({
        eventids: ["12345", "12346"],
        message: "Investigating - automated by Akmatori",
      });
    });
  });

  // -----------------------------------------------------------------------
  // Tool result format
  // -----------------------------------------------------------------------

  describe("result format", () => {
    it("should return AgentToolResult with text content", async () => {
      const tools = createMCPTools(mock.url, "inc-format");
      const tool = tools.find((t) => t.name === "ssh_test_connectivity")!;

      mock.responseHandler = (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: {
          content: [{ type: "text", text: "all servers reachable" }],
        },
      });

      const result = await tool.execute(
        "call-fmt",
        {} as any,
        undefined,
        undefined,
        {} as any,
      );

      expect(result).toEqual({
        content: [{ type: "text", text: "all servers reachable" }],
        details: undefined,
      });
    });
  });
});
