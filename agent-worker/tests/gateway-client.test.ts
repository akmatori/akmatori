import { describe, it, expect, beforeEach, afterEach, afterAll } from "vitest";
import * as http from "node:http";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import { GatewayClient, GatewayError, buildSmartPreview } from "../src/gateway-client.js";

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
        expect((result.data as string)).toContain("Full output saved to:");
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
  // listToolsByType()
  // -----------------------------------------------------------------------

  describe("listToolsByType()", () => {
    it("sends list request with tool_type", async () => {
      const listResult = {
        tools: [
          { name: "ssh.execute_command", description: "Run SSH commands", instances: ["prod-ssh"] },
        ],
      };
      const mock = await createMockGateway(() => jsonRpcSuccess(listResult));

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        const result = await client.listToolsByType("ssh");

        const body = JSON.parse(mock.requests[0].body);
        expect(body.method).toBe("tools/list_by_type");
        expect(body.params.tool_type).toBe("ssh");

        expect(result.tools).toHaveLength(1);
        expect(result.tools[0].name).toBe("ssh.execute_command");
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
  // listToolTypes()
  // -----------------------------------------------------------------------

  describe("listToolTypes()", () => {
    it("sends list_types request and returns types", async () => {
      const typesResult = { types: ["ssh", "zabbix", "victoria_metrics"] };
      const mock = await createMockGateway(() => jsonRpcSuccess(typesResult));

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        const result = await client.listToolTypes();

        const body = JSON.parse(mock.requests[0].body);
        expect(body.method).toBe("tools/list_types");
        expect(body.params).toEqual({});

        expect(result.types).toEqual(["ssh", "zabbix", "victoria_metrics"]);
      } finally {
        mock.server.close();
      }
    });

    it("returns empty types array", async () => {
      const mock = await createMockGateway(() => jsonRpcSuccess({ types: [] }));

      try {
        const client = new GatewayClient({ gatewayUrl: mock.url, incidentId: "inc-1" });
        const result = await client.listToolTypes();

        expect(result.types).toEqual([]);
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

// ---------------------------------------------------------------------------
// buildSmartPreview tests
// ---------------------------------------------------------------------------

const OUTPUT_FILE = "/tmp/tool_outputs/test_tool_12345.json";

function ser(value: unknown): string {
  return JSON.stringify(value);
}

describe("buildSmartPreview", () => {
  it("summarises a Prometheus vector result", () => {
    const payload = {
      resultType: "vector",
      result: [
        { metric: { __name__: "up", job: "prometheus", instance: "localhost:9090" }, value: [1741000000, "1"] },
        { metric: { __name__: "up", job: "node", instance: "host1:9100" }, value: [1741000000, "0"] },
        { metric: { __name__: "node_cpu_seconds_total", job: "node", instance: "host1:9100" }, value: [1741000000, "1234"] },
        { metric: { __name__: "node_memory_MemAvailable_bytes", job: "node" }, value: [1741000000, "8192000"] },
      ],
    };

    const result = buildSmartPreview(ser(payload), OUTPUT_FILE);

    expect(result).toContain("Prometheus vector result");
    expect(result).toContain("4 series");
    expect(result).toContain("up");
    expect(result).toContain("node_cpu_seconds_total");
    expect(result).toContain("node_memory_MemAvailable_bytes");
    expect(result).toContain("[0] metric:");
    expect(result).toContain("[1] metric:");
    expect(result).toContain("[2] metric:");
    expect(result).toContain(`Full output saved to: ${OUTPUT_FILE}`);
    expect(result).toContain("execute_script");
  });

  it("summarises a Prometheus matrix result", () => {
    const payload = {
      resultType: "matrix",
      result: [
        { metric: { __name__: "http_requests_total", handler: "/api/v1" }, values: [[1741000000, "10"]] },
        { metric: { __name__: "http_requests_total", handler: "/health" }, values: [[1741000000, "1"]] },
      ],
    };

    const result = buildSmartPreview(ser(payload), OUTPUT_FILE);

    expect(result).toContain("Prometheus matrix result");
    expect(result).toContain("2 series");
    expect(result).toContain("http_requests_total");
  });

  it("handles Prometheus result with no __name__ labels", () => {
    const payload = {
      resultType: "vector",
      result: [{ metric: { job: "custom" }, value: [1741000000, "42"] }],
    };

    const result = buildSmartPreview(ser(payload), OUTPUT_FILE);
    expect(result).toContain("1 series");
    expect(result).toContain("(none)");
  });

  it("summarises a plain array of objects", () => {
    const payload = Array.from({ length: 5 }, (_, i) => ({ id: i, name: `item-${i}` }));

    const result = buildSmartPreview(ser(payload), OUTPUT_FILE);

    expect(result).toContain("Array with 5 item(s)");
    expect(result).toContain("[0]");
    expect(result).toContain("[2]");
    expect(result).not.toContain("[3]");
    expect(result).toContain("2 more");
  });

  it("summarises a plain object listing top-level keys", () => {
    const payload = { status: "ok", count: 42, tags: ["a", "b"], nested: { x: 1 }, enabled: true };

    const result = buildSmartPreview(ser(payload), OUTPUT_FILE);

    expect(result).toContain("Object with 5 top-level key(s)");
    expect(result).toContain("status");
    expect(result).toContain("array[2]");
    expect(result).toContain("object");
  });

  it("caps object key display at 10", () => {
    const payload: Record<string, number> = {};
    for (let i = 0; i < 15; i++) payload[`key${i}`] = i;

    const result = buildSmartPreview(ser(payload), OUTPUT_FILE);
    expect(result).toContain("15 top-level key(s)");
    expect(result).toContain("...");
  });

  it("falls back to raw slice for non-JSON input", () => {
    const raw = "ERROR: connection refused";
    const result = buildSmartPreview(raw, OUTPUT_FILE);
    expect(result).toContain("ERROR: connection refused");
    expect(result).toContain("[truncated]");
    expect(result).toContain("execute_script");
  });

  it("handles empty array", () => {
    const result = buildSmartPreview(ser([]), OUTPUT_FILE);
    expect(result).toContain("Array with 0 item(s)");
  });

  it("handles empty object", () => {
    const result = buildSmartPreview(ser({}), OUTPUT_FILE);
    expect(result).toContain("Object with 0 top-level key(s)");
  });

  it("always appends the execute_script tip", () => {
    for (const payload of [ser([1, 2]), ser({ a: 1 }), "plain text"]) {
      const result = buildSmartPreview(payload, OUTPUT_FILE);
      expect(result).toContain(`Full output saved to: ${OUTPUT_FILE}`);
      expect(result).toContain(`fs.readFileSync('${OUTPUT_FILE}', 'utf-8')`);
    }
  });
});
