import { describe, it, expect, beforeEach, afterEach, afterAll } from "vitest";
import * as http from "node:http";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import { GatewayClient, GatewayError } from "../src/gateway-client.js";

// ---------------------------------------------------------------------------
// Mock HTTP server helpers
// ---------------------------------------------------------------------------

interface MockRequest {
  method: string;
  url: string;
  headers: http.IncomingHttpHeaders;
  body: string;
}

function createMockGateway(
  handler: (req: MockRequest) => { status: number; body: unknown },
): Promise<{ url: string; server: http.Server; requests: MockRequest[] }> {
  return new Promise((resolve) => {
    const requests: MockRequest[] = [];
    const server = http.createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on("data", (chunk: Buffer) => chunks.push(chunk));
      req.on("end", () => {
        const mockReq: MockRequest = {
          method: req.method ?? "GET",
          url: req.url ?? "/",
          headers: req.headers,
          body: Buffer.concat(chunks).toString("utf-8"),
        };
        requests.push(mockReq);
        const result = handler(mockReq);
        res.writeHead(result.status, { "Content-Type": "application/json" });
        res.end(JSON.stringify(result.body));
      });
    });
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address() as { port: number };
      resolve({ url: `http://127.0.0.1:${addr.port}`, server, requests });
    });
  });
}

function jsonRpcSuccess(result: unknown) {
  return { status: 200, body: { jsonrpc: "2.0", result, id: 1 } };
}

function jsonRpcError(code: number, message: string, data?: unknown) {
  return { status: 200, body: { jsonrpc: "2.0", error: { code, message, data }, id: 1 } };
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("GatewayClient", () => {
  let tmpDir: string;

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "gateway-test-"));
  });

  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  // -----------------------------------------------------------------------
  // call()
  // -----------------------------------------------------------------------

  describe("call()", () => {
    it("sends JSON-RPC 2.0 request with correct format", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({
          content: [{ type: "text", text: JSON.stringify({ hostname: "server-1" }) }],
        }),
      );

      try {
        const client = new GatewayClient({
          gatewayUrl: mock.url,
          incidentId: "inc-123",
          workDir: tmpDir,
        });

        await client.call("ssh.execute_command", { command: "hostname" });

        expect(mock.requests).toHaveLength(1);
        const req = mock.requests[0];
        expect(req.method).toBe("POST");
        expect(req.url).toBe("/mcp");
        expect(req.headers["content-type"]).toBe("application/json");
        expect(req.headers["x-incident-id"]).toBe("inc-123");

        const body = JSON.parse(req.body);
        expect(body.jsonrpc).toBe("2.0");
        expect(body.method).toBe("tools/call");
        expect(body.params.name).toBe("ssh.execute_command");
        expect(body.params.arguments).toEqual({ command: "hostname" });
        expect(typeof body.id).toBe("number");
      } finally {
        mock.server.close();
      }
    });

    it("passes instance hint when provided", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({
          content: [{ type: "text", text: '"ok"' }],
        }),
      );

      try {
        const client = new GatewayClient({
          gatewayUrl: mock.url,
          incidentId: "inc-1",
        });

        await client.call("ssh.execute_command", { command: "uptime" }, "prod-ssh");

        const body = JSON.parse(mock.requests[0].body);
        expect(body.params.instance).toBe("prod-ssh");
      } finally {
        mock.server.close();
      }
    });

    it("extracts JSON result from MCP content envelope", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({
          content: [{ type: "text", text: JSON.stringify({ cpu: 42, mem: 80 }) }],
        }),
      );

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        const result = await client.call("zabbix.get_items", { hostid: "1" });
        expect(result.data).toEqual({ cpu: 42, mem: 80 });
        expect(result.outputFile).toBeUndefined();
      } finally {
        mock.server.close();
      }
    });

    it("returns raw text when content is not JSON", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({
          content: [{ type: "text", text: "uptime: 42 days" }],
        }),
      );

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        const result = await client.call("ssh.execute_command", { command: "uptime" });
        expect(result.data).toBe("uptime: 42 days");
      } finally {
        mock.server.close();
      }
    });

    it("returns raw result when no content envelope", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({ status: "ok" }),
      );

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        const result = await client.call("custom.tool", {});
        expect(result.data).toEqual({ status: "ok" });
      } finally {
        mock.server.close();
      }
    });

    it("increments request ID across calls", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({ content: [{ type: "text", text: '"ok"' }] }),
      );

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        await client.call("tool.a", {});
        await client.call("tool.b", {});

        const id1 = JSON.parse(mock.requests[0].body).id;
        const id2 = JSON.parse(mock.requests[1].body).id;
        expect(id2).toBeGreaterThan(id1);
      } finally {
        mock.server.close();
      }
    });
  });

  // -----------------------------------------------------------------------
  // Output management
  // -----------------------------------------------------------------------

  describe("output management", () => {
    it("returns inline for small responses (< 4KB)", async () => {
      const smallData = { key: "value" };
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({
          content: [{ type: "text", text: JSON.stringify(smallData) }],
        }),
      );

      try {
        const client = new GatewayClient({
          gatewayUrl: mock.url,
          incidentId: "inc-1",
          workDir: tmpDir,
        });

        const result = await client.call("tool.small", {});
        expect(result.data).toEqual(smallData);
        expect(result.outputFile).toBeUndefined();
      } finally {
        mock.server.close();
      }
    });

    it("writes large responses (>= 4KB) to file", async () => {
      const largeData = { data: "x".repeat(5000) };
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({
          content: [{ type: "text", text: JSON.stringify(largeData) }],
        }),
      );

      try {
        const client = new GatewayClient({
          gatewayUrl: mock.url,
          incidentId: "inc-1",
          workDir: tmpDir,
        });

        const result = await client.call("tool.large", {});
        expect(result.outputFile).toBeDefined();
        expect(typeof result.data).toBe("string");
        expect((result.data as string)).toContain("truncated");
        expect((result.data as string)).toContain(result.outputFile!);

        // Verify file was written
        const fileContent = fs.readFileSync(result.outputFile!, "utf-8");
        expect(JSON.parse(fileContent)).toEqual(largeData);
      } finally {
        mock.server.close();
      }
    });

    it("returns inline for large responses when no workDir", async () => {
      const largeData = { data: "x".repeat(5000) };
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({
          content: [{ type: "text", text: JSON.stringify(largeData) }],
        }),
      );

      try {
        const client = new GatewayClient({
          gatewayUrl: mock.url,
          incidentId: "inc-1",
          // no workDir
        });

        const result = await client.call("tool.large", {});
        expect(result.data).toEqual(largeData);
        expect(result.outputFile).toBeUndefined();
      } finally {
        mock.server.close();
      }
    });

    it("creates tool_outputs directory if needed", async () => {
      const largeText = "x".repeat(5000);
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({
          content: [{ type: "text", text: largeText }],
        }),
      );

      try {
        const client = new GatewayClient({
          gatewayUrl: mock.url,
          incidentId: "inc-1",
          workDir: tmpDir,
        });

        const result = await client.call("tool.big", {});
        expect(result.outputFile).toBeDefined();

        const outputDir = path.join(tmpDir, "tool_outputs");
        expect(fs.existsSync(outputDir)).toBe(true);
      } finally {
        mock.server.close();
      }
    });
  });

  // -----------------------------------------------------------------------
  // searchTools()
  // -----------------------------------------------------------------------

  describe("searchTools()", () => {
    it("sends search request with query", async () => {
      const searchResult = {
        tools: [
          { name: "ssh.execute_command", description: "Run SSH commands", instances: ["prod-ssh"] },
        ],
      };
      const mock = await createMockGateway(() => jsonRpcSuccess(searchResult));

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        const result = await client.searchTools("ssh");

        const body = JSON.parse(mock.requests[0].body);
        expect(body.method).toBe("tools/search");
        expect(body.params.query).toBe("ssh");
        expect(body.params.tool_type).toBeUndefined();

        expect(result.tools).toHaveLength(1);
        expect(result.tools[0].name).toBe("ssh.execute_command");
      } finally {
        mock.server.close();
      }
    });

    it("sends search request with tool_type filter", async () => {
      const mock = await createMockGateway(() => jsonRpcSuccess({ tools: [] }));

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        await client.searchTools("query", "zabbix");

        const body = JSON.parse(mock.requests[0].body);
        expect(body.params.tool_type).toBe("zabbix");
      } finally {
        mock.server.close();
      }
    });
  });

  // -----------------------------------------------------------------------
  // getToolDetail()
  // -----------------------------------------------------------------------

  describe("getToolDetail()", () => {
    it("sends detail request for tool name", async () => {
      const detailResult = {
        name: "ssh.execute_command",
        description: "Execute a command on a remote server via SSH",
        input_schema: { command: { type: "string", required: true } },
        instances: [{ id: 1, logical_name: "prod-ssh", name: "Production SSH" }],
      };
      const mock = await createMockGateway(() => jsonRpcSuccess(detailResult));

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        const result = await client.getToolDetail("ssh.execute_command");

        const body = JSON.parse(mock.requests[0].body);
        expect(body.method).toBe("tools/detail");
        expect(body.params.tool_name).toBe("ssh.execute_command");

        expect(result.name).toBe("ssh.execute_command");
        expect(result.input_schema).toHaveProperty("command");
        expect(result.instances).toHaveLength(1);
      } finally {
        mock.server.close();
      }
    });
  });

  // -----------------------------------------------------------------------
  // Error handling
  // -----------------------------------------------------------------------

  describe("error handling", () => {
    it("throws GatewayError on JSON-RPC error response", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcError(-32601, "Method not found"),
      );

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        await expect(client.call("nonexistent.tool", {})).rejects.toThrow(GatewayError);
        await expect(client.call("nonexistent.tool", {})).rejects.toThrow("Method not found");
      } finally {
        mock.server.close();
      }
    });

    it("preserves error code and data", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcError(-32600, "Unauthorized", { allowed: ["ssh"] }),
      );

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });

        try {
          await client.call("zabbix.get_hosts", {});
          expect.fail("should have thrown");
        } catch (err) {
          expect(err).toBeInstanceOf(GatewayError);
          const ge = err as GatewayError;
          expect(ge.code).toBe(-32600);
          expect(ge.data).toEqual({ allowed: ["ssh"] });
        }
      } finally {
        mock.server.close();
      }
    });

    it("throws GatewayError on tool execution failure (isError)", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({
          content: [{ type: "text", text: "Permission denied" }],
          isError: true,
        }),
      );

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        await expect(client.call("ssh.execute_command", {})).rejects.toThrow("Tool execution failed");
      } finally {
        mock.server.close();
      }
    });

    it("throws GatewayError on invalid JSON response", async () => {
      const mock = await createMockGateway(() => ({
        status: 200,
        body: "not json" as unknown,
      }));

      // The mock server will try to JSON.stringify "not json" which is valid,
      // but we need to test truly invalid JSON. Use a raw server instead.
      mock.server.close();

      const rawServer = await new Promise<{ url: string; server: http.Server }>((resolve) => {
        const server = http.createServer((_req, res) => {
          res.writeHead(200);
          res.end("<<<not json>>>");
        });
        server.listen(0, "127.0.0.1", () => {
          const addr = server.address() as { port: number };
          resolve({ url: `http://127.0.0.1:${addr.port}`, server });
        });
      });

      try {
        const client = new GatewayClient({ gatewayUrl: rawServer.url, incidentId: "inc-1" });
        await expect(client.call("tool", {})).rejects.toThrow("Invalid JSON response");
      } finally {
        rawServer.server.close();
      }
    });

    it("throws GatewayError on connection error", async () => {
      const client = new GatewayClient({
        gatewayUrl: "http://127.0.0.1:1",
        incidentId: "inc-1",
      });

      await expect(client.call("tool", {})).rejects.toThrow(GatewayError);
      await expect(client.call("tool", {})).rejects.toThrow("Connection error");
    });

    it("handles null result", async () => {
      const mock = await createMockGateway(() => jsonRpcSuccess(null));

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        const result = await client.call("tool", {});
        expect(result.data).toBeNull();
      } finally {
        mock.server.close();
      }
    });
  });

  // -----------------------------------------------------------------------
  // Headers
  // -----------------------------------------------------------------------

  describe("headers", () => {
    it("omits X-Incident-ID when incidentId is empty", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({ content: [{ type: "text", text: '"ok"' }] }),
      );

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "" });
        await client.call("tool", {});

        expect(mock.requests[0].headers["x-incident-id"]).toBeUndefined();
      } finally {
        mock.server.close();
      }
    });

    it("strips trailing slashes from gatewayUrl", async () => {
      const mock = await createMockGateway(() =>
        jsonRpcSuccess({ content: [{ type: "text", text: '"ok"' }] }),
      );

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url + "///", incidentId: "inc-1" });
        await client.call("tool", {});

        expect(mock.requests[0].url).toBe("/mcp");
      } finally {
        mock.server.close();
      }
    });
  });
});
