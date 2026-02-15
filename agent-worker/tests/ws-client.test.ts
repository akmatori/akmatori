import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { WebSocketServer, WebSocket as WsWebSocket } from "ws";
import { WebSocketClient } from "../src/ws-client.js";
import type { WebSocketMessage } from "../src/types.js";

/** Find a free port and create a WS server. */
function createMockServer(): Promise<{
  server: WebSocketServer;
  port: number;
  url: string;
  clients: WsWebSocket[];
  received: string[];
}> {
  return new Promise((resolve) => {
    const server = new WebSocketServer({ port: 0 });
    const clients: WsWebSocket[] = [];
    const received: string[] = [];

    server.on("listening", () => {
      const addr = server.address();
      const port = typeof addr === "object" ? addr!.port : 0;
      const url = `ws://127.0.0.1:${port}`;

      server.on("connection", (ws) => {
        clients.push(ws);
        ws.on("message", (data) => {
          received.push(data.toString());
        });
      });

      resolve({ server, port, url, clients, received });
    });
  });
}

function closeServer(server: WebSocketServer): Promise<void> {
  return new Promise((resolve) => {
    server.clients.forEach((ws) => ws.terminate());
    server.close(() => resolve());
  });
}

describe("WebSocketClient", () => {
  let mockServer: Awaited<ReturnType<typeof createMockServer>>;
  let client: WebSocketClient;

  beforeEach(async () => {
    mockServer = await createMockServer();
  });

  afterEach(async () => {
    if (client) {
      client.close();
    }
    await closeServer(mockServer.server);
  });

  // -----------------------------------------------------------------------
  // Connection
  // -----------------------------------------------------------------------

  describe("connect", () => {
    it("should connect to the server", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        logger: () => {},
      });

      await client.connect();
      expect(client.isConnected()).toBe(true);
    });

    it("should reject when server is not available", async () => {
      client = new WebSocketClient({
        url: "ws://127.0.0.1:59999",
        connectTimeoutMs: 1000,
        logger: () => {},
      });

      await expect(client.connect()).rejects.toThrow();
      expect(client.isConnected()).toBe(false);
    });

    it("should reject after close is called", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        logger: () => {},
      });

      client.close();

      await expect(client.connect()).rejects.toThrow("Client has been closed");
    });
  });

  // -----------------------------------------------------------------------
  // Message sending
  // -----------------------------------------------------------------------

  describe("send", () => {
    it("should send messages in Go-compatible JSON format", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000, // prevent heartbeat noise in test
        logger: () => {},
      });

      await client.connect();

      // Wait for the server to register the client
      await sleep(50);

      client.send({
        type: "codex_output",
        incident_id: "inc-123",
        output: "some output",
      });

      // Wait for message delivery
      await sleep(50);

      expect(mockServer.received.length).toBeGreaterThanOrEqual(1);
      const parsed = JSON.parse(mockServer.received[0]);
      expect(parsed.type).toBe("codex_output");
      expect(parsed.incident_id).toBe("inc-123");
      expect(parsed.output).toBe("some output");
      // omitempty: fields not set should not be present
      expect(parsed.task).toBeUndefined();
      expect(parsed.session_id).toBeUndefined();
    });

    it("should not send when not connected", async () => {
      const logMessages: string[] = [];
      client = new WebSocketClient({
        url: mockServer.url,
        logger: (msg) => logMessages.push(msg),
      });

      // Don't connect
      client.send({ type: "heartbeat" });

      expect(logMessages.some((m) => m.includes("not connected"))).toBe(true);
    });
  });

  describe("sendOutput", () => {
    it("should send codex_output message", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      await client.connect();
      await sleep(50);

      client.sendOutput("inc-456", "running diagnostics...");
      await sleep(50);

      const parsed = JSON.parse(mockServer.received[0]);
      expect(parsed.type).toBe("codex_output");
      expect(parsed.incident_id).toBe("inc-456");
      expect(parsed.output).toBe("running diagnostics...");
    });
  });

  describe("sendCompleted", () => {
    it("should send codex_completed message with metrics", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      await client.connect();
      await sleep(50);

      client.sendCompleted("inc-789", "sess-001", "Resolved the issue", 1500, 45000);
      await sleep(50);

      const parsed = JSON.parse(mockServer.received[0]);
      expect(parsed.type).toBe("codex_completed");
      expect(parsed.incident_id).toBe("inc-789");
      expect(parsed.session_id).toBe("sess-001");
      expect(parsed.output).toBe("Resolved the issue");
      expect(parsed.tokens_used).toBe(1500);
      expect(parsed.execution_time_ms).toBe(45000);
    });
  });

  describe("sendError", () => {
    it("should send codex_error message", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      await client.connect();
      await sleep(50);

      client.sendError("inc-err", "API key invalid");
      await sleep(50);

      const parsed = JSON.parse(mockServer.received[0]);
      expect(parsed.type).toBe("codex_error");
      expect(parsed.incident_id).toBe("inc-err");
      expect(parsed.error).toBe("API key invalid");
    });
  });

  // -----------------------------------------------------------------------
  // Message serialization matches Go format
  // -----------------------------------------------------------------------

  describe("message serialization", () => {
    it("should match Go JSON format for codex_completed", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      await client.connect();
      await sleep(50);

      client.sendCompleted("inc-1", "sess-1", "done", 500, 10000);
      await sleep(50);

      const raw = mockServer.received[0];
      const parsed = JSON.parse(raw);

      // Go uses snake_case for JSON tags
      expect(parsed).toHaveProperty("type");
      expect(parsed).toHaveProperty("incident_id");
      expect(parsed).toHaveProperty("session_id");
      expect(parsed).toHaveProperty("output");
      expect(parsed).toHaveProperty("tokens_used");
      expect(parsed).toHaveProperty("execution_time_ms");

      // Go omits zero-value fields with omitempty
      expect(parsed).not.toHaveProperty("task");
      expect(parsed).not.toHaveProperty("error");
      expect(parsed).not.toHaveProperty("data");
    });

    it("should match Go JSON format for heartbeat", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      await client.connect();
      await sleep(50);

      client.sendHeartbeat();
      await sleep(50);

      const parsed = JSON.parse(mockServer.received[0]);
      // Heartbeat should only have type
      expect(parsed).toEqual({ type: "heartbeat" });
    });

    it("should match Go JSON format for status message with data", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      await client.connect();
      await sleep(50);

      client.send({
        type: "status",
        data: { status: "ready" },
      });
      await sleep(50);

      const parsed = JSON.parse(mockServer.received[0]);
      expect(parsed).toEqual({
        type: "status",
        data: { status: "ready" },
      });
    });
  });

  // -----------------------------------------------------------------------
  // Message receiving
  // -----------------------------------------------------------------------

  describe("onMessage", () => {
    it("should invoke handler when server sends a message", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      const received: WebSocketMessage[] = [];
      client.onMessage((msg) => received.push(msg));

      await client.connect();
      await sleep(50);

      // Server sends a message to the client
      const serverMsg: WebSocketMessage = {
        type: "new_incident",
        incident_id: "inc-new",
        task: "Check server status",
        openai_api_key: "sk-test",
        model: "gpt-4o",
      };
      mockServer.clients[0].send(JSON.stringify(serverMsg));

      await sleep(50);

      expect(received).toHaveLength(1);
      expect(received[0].type).toBe("new_incident");
      expect(received[0].incident_id).toBe("inc-new");
      expect(received[0].task).toBe("Check server status");
    });

    it("should handle malformed messages gracefully", async () => {
      const logMessages: string[] = [];
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: (msg) => logMessages.push(msg),
      });

      const received: WebSocketMessage[] = [];
      client.onMessage((msg) => received.push(msg));

      await client.connect();
      await sleep(50);

      // Send invalid JSON
      mockServer.clients[0].send("not-json{{{");

      await sleep(50);

      expect(received).toHaveLength(0);
      expect(logMessages.some((m) => m.includes("Failed to parse"))).toBe(true);
    });
  });

  // -----------------------------------------------------------------------
  // Heartbeat
  // -----------------------------------------------------------------------

  describe("heartbeat", () => {
    it("should send heartbeats at the configured interval", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 100, // 100ms for fast test
        logger: () => {},
      });

      await client.connect();

      // Wait for ~3 heartbeat intervals
      await sleep(350);

      const heartbeats = mockServer.received
        .map((r) => JSON.parse(r))
        .filter((m: WebSocketMessage) => m.type === "heartbeat");

      // Should have at least 2 heartbeats in 350ms with 100ms interval
      expect(heartbeats.length).toBeGreaterThanOrEqual(2);
    });

    it("should stop heartbeats when disconnected", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 50,
        logger: () => {},
      });

      await client.connect();
      await sleep(100); // let some heartbeats flow

      client.close();
      const countAtClose = mockServer.received.length;

      await sleep(200); // wait to see if more arrive

      // No new messages should arrive after close
      expect(mockServer.received.length).toBe(countAtClose);
    });
  });

  // -----------------------------------------------------------------------
  // Reconnection
  // -----------------------------------------------------------------------

  describe("reconnection", () => {
    it("should reconnect with exponential backoff", async () => {
      // Start client connecting to a port that doesn't exist yet
      const port = mockServer.port;
      await closeServer(mockServer.server);

      const logMessages: string[] = [];
      client = new WebSocketClient({
        url: `ws://127.0.0.1:${port}`,
        reconnectBaseMs: 50,
        reconnectMaxMs: 200,
        connectTimeoutMs: 500,
        logger: (msg) => logMessages.push(msg),
      });

      // Start connectWithReconnect in background
      const connectPromise = client.connectWithReconnect();

      // Let it fail a few times
      await sleep(300);

      // Now start a new server on the same port
      const newServer = await new Promise<WebSocketServer>((resolve) => {
        const s = new WebSocketServer({ port });
        s.on("listening", () => resolve(s));
      });

      // Wait for the client to connect
      await connectPromise;

      expect(client.isConnected()).toBe(true);
      expect(
        logMessages.some((m) => m.includes("Connection failed"))
      ).toBe(true);

      client.close();
      await new Promise<void>((resolve) => {
        newServer.close(() => resolve());
      });
    });

    it("should stop reconnecting when close is called", async () => {
      await closeServer(mockServer.server);

      const logMessages: string[] = [];
      client = new WebSocketClient({
        url: "ws://127.0.0.1:59998",
        reconnectBaseMs: 50,
        reconnectMaxMs: 100,
        connectTimeoutMs: 100,
        logger: (msg) => logMessages.push(msg),
      });

      const connectPromise = client.connectWithReconnect();

      // Let it fail once
      await sleep(200);

      // Close should stop the reconnect loop
      client.close();

      // The promise should resolve without throwing (loop exits)
      await connectPromise;

      expect(client.isConnected()).toBe(false);
    });

    it("should reset state for reconnection", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      await client.connect();
      expect(client.isConnected()).toBe(true);

      client.reset();
      expect(client.isConnected()).toBe(false);

      // Should be able to connect again
      await client.connect();
      expect(client.isConnected()).toBe(true);
    });
  });

  // -----------------------------------------------------------------------
  // Error handling on connection loss
  // -----------------------------------------------------------------------

  describe("connection loss", () => {
    it("should report disconnection when server closes", async () => {
      const logMessages: string[] = [];
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: (msg) => logMessages.push(msg),
      });

      await client.connect();
      expect(client.isConnected()).toBe(true);

      // Server closes the connection
      mockServer.clients[0].close();
      await sleep(100);

      expect(client.isConnected()).toBe(false);
      expect(logMessages.some((m) => m.includes("Connection closed"))).toBe(true);
    });

    it("should handle server termination", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      await client.connect();
      expect(client.isConnected()).toBe(true);

      // Terminate server-side connection abruptly
      mockServer.clients[0].terminate();
      await sleep(100);

      expect(client.isConnected()).toBe(false);
    });
  });

  // -----------------------------------------------------------------------
  // Close/cleanup
  // -----------------------------------------------------------------------

  describe("close", () => {
    it("should gracefully close the connection", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      await client.connect();
      expect(client.isConnected()).toBe(true);

      client.close();
      expect(client.isConnected()).toBe(false);
    });

    it("should be safe to call close multiple times", async () => {
      client = new WebSocketClient({
        url: mockServer.url,
        heartbeatIntervalMs: 60_000,
        logger: () => {},
      });

      await client.connect();

      client.close();
      client.close();
      client.close();

      expect(client.isConnected()).toBe(false);
    });
  });
});

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
