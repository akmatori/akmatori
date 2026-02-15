import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { Orchestrator, type OrchestratorConfig } from "../src/orchestrator.js";
import type { WebSocketMessage, ProxyConfig } from "../src/types.js";

// ---------------------------------------------------------------------------
// Mock the pi-mono SDK modules (same as agent-runner tests)
// ---------------------------------------------------------------------------

function createMockSession() {
  const subscribers: Array<(event: any) => void> = [];
  return {
    sessionId: "mock-session-456",
    subscribe: vi.fn((listener: (event: any) => void) => {
      subscribers.push(listener);
      return () => {
        const idx = subscribers.indexOf(listener);
        if (idx >= 0) subscribers.splice(idx, 1);
      };
    }),
    prompt: vi.fn(async (_text: string) => {
      for (const sub of subscribers) {
        sub({
          type: "message_update",
          message: {},
          assistantMessageEvent: {
            type: "text_delta",
            contentIndex: 0,
            delta: "Done.",
            partial: {},
          },
        });
      }
      for (const sub of subscribers) {
        sub({
          type: "turn_end",
          message: {
            role: "assistant",
            usage: { totalTokens: 500, input: 300, output: 200 },
          },
          toolResults: [],
        });
      }
    }),
    abort: vi.fn(),
    getLastAssistantText: vi.fn(() => "Done."),
    _subscribers: subscribers,
  };
}

let mockSession = createMockSession();

vi.mock("@mariozechner/pi-coding-agent", () => ({
  createAgentSession: vi.fn(async () => ({
    session: mockSession,
  })),
  AuthStorage: vi.fn(() => ({
    setRuntimeApiKey: vi.fn(),
  })),
  ModelRegistry: vi.fn(() => ({})),
  SessionManager: {
    inMemory: vi.fn(() => ({})),
  },
  SettingsManager: {
    inMemory: vi.fn(() => ({})),
  },
  createCodingTools: vi.fn(() => []),
}));

vi.mock("@mariozechner/pi-ai", () => ({
  getModel: vi.fn(() => ({
    id: "o4-mini",
    name: "o4-mini",
    api: "openai-responses",
    provider: "openai",
  })),
}));

// Mock the MCP tools
vi.mock("../src/tools/mcp-tools.js", () => ({
  createMCPTools: vi.fn(() => []),
}));

// ---------------------------------------------------------------------------
// Mock WebSocket server for the WebSocketClient
// ---------------------------------------------------------------------------

import { WebSocketServer, WebSocket as WsWebSocket } from "ws";

let wss: WebSocketServer;
let serverPort: number;
let serverConnections: WsWebSocket[] = [];
let allServerMessages: WebSocketMessage[] = [];

async function startMockServer(): Promise<number> {
  return new Promise((resolve) => {
    wss = new WebSocketServer({ port: 0 });
    wss.on("connection", (ws) => {
      serverConnections.push(ws);
      // Automatically collect all messages from the client
      ws.on("message", (data) => {
        try {
          allServerMessages.push(JSON.parse(data.toString()));
        } catch {
          // ignore parse errors
        }
      });
    });
    wss.on("listening", () => {
      const addr = wss.address();
      const port = typeof addr === "object" ? addr!.port : 0;
      resolve(port);
    });
  });
}

function closeMockServer(): Promise<void> {
  return new Promise((resolve) => {
    serverConnections.forEach((ws) => ws.close());
    serverConnections = [];
    if (wss) {
      wss.close(() => resolve());
    } else {
      resolve();
    }
  });
}

/** Send a message from the mock server to the orchestrator's WebSocket client. */
function sendFromServer(msg: WebSocketMessage): void {
  const data = JSON.stringify(msg);
  serverConnections.forEach((ws) => ws.send(data));
}

/**
 * Wait until allServerMessages contains at least `count` messages,
 * or until timeout.
 */
async function waitForMessages(count: number, timeoutMs = 5000): Promise<WebSocketMessage[]> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (allServerMessages.length >= count) {
      // Small delay for any trailing messages
      await sleep(50);
      return [...allServerMessages];
    }
    await sleep(50);
  }
  return [...allServerMessages];
}

/**
 * Wait for at least one message matching a predicate.
 */
async function waitForMessage(
  predicate: (m: WebSocketMessage) => boolean,
  timeoutMs = 5000,
): Promise<WebSocketMessage | undefined> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const found = allServerMessages.find(predicate);
    if (found) return found;
    await sleep(50);
  }
  return allServerMessages.find(predicate);
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("Orchestrator", () => {
  let orchestrator: Orchestrator;
  let config: OrchestratorConfig;
  const logs: string[] = [];

  beforeEach(async () => {
    mockSession = createMockSession();

    // Reset mocks
    vi.clearAllMocks();
    logs.length = 0;
    allServerMessages = [];

    serverPort = await startMockServer();

    config = {
      apiWsUrl: `ws://127.0.0.1:${serverPort}`,
      mcpGatewayUrl: "http://mcp-gateway:8080",
      workspaceDir: "/tmp/test-workspaces",
      logger: (msg: string) => logs.push(msg),
    };

    orchestrator = new Orchestrator(config);
  });

  afterEach(async () => {
    try {
      await orchestrator.stop();
    } catch {
      // ignore
    }
    await closeMockServer();
  });

  // -----------------------------------------------------------------------
  // Lifecycle
  // -----------------------------------------------------------------------

  describe("lifecycle", () => {
    it("should connect and send ready status on start", async () => {
      await orchestrator.start();
      expect(orchestrator.isConnected()).toBe(true);

      // Wait for the ready status message
      const statusMsg = await waitForMessage((m) => m.type === "status");
      expect(statusMsg).toBeDefined();
      expect(statusMsg!.data).toEqual({ status: "ready" });
    });

    it("should report isStopped false before stop", async () => {
      await orchestrator.start();
      expect(orchestrator.isStopped()).toBe(false);
    });

    it("should disconnect and report isStopped true after stop", async () => {
      await orchestrator.start();
      await orchestrator.stop();
      expect(orchestrator.isStopped()).toBe(true);
      expect(orchestrator.isConnected()).toBe(false);
    });

    it("should dispose runner on stop", async () => {
      await orchestrator.start();

      const runner = orchestrator.getRunner();
      const disposeSpy = vi.spyOn(runner, "dispose");

      await orchestrator.stop();
      expect(disposeSpy).toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // Message routing: new_incident
  // -----------------------------------------------------------------------

  describe("new_incident routing", () => {
    it("should execute agent and send completion on new_incident", async () => {
      await orchestrator.start();

      // Wait for ready status first
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        incident_id: "incident-001",
        task: "Investigate high CPU usage",
        openai_api_key: "sk-test-key",
        model: "o4-mini",
        reasoning_effort: "medium",
      });

      // Wait for the completion message
      const completedMsg = await waitForMessage(
        (m) => m.type === "codex_completed" && m.incident_id === "incident-001",
      );

      expect(completedMsg).toBeDefined();
      expect(completedMsg!.session_id).toBe("mock-session-456");
      expect(completedMsg!.tokens_used).toBe(500);
      expect(completedMsg!.execution_time_ms).toBeGreaterThan(0);

      // Should also have output messages
      const outputMsgs = allServerMessages.filter(
        (m) => m.type === "codex_output" && m.incident_id === "incident-001",
      );
      expect(outputMsgs.length).toBeGreaterThanOrEqual(1);
    });

    it("should send error when new_incident has no API key", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        incident_id: "incident-002",
        task: "Some task",
        // No openai_api_key
      });

      const errorMsg = await waitForMessage(
        (m) => m.type === "codex_error" && m.incident_id === "incident-002",
      );
      expect(errorMsg).toBeDefined();
      expect(errorMsg!.error).toContain("Missing LLM settings");
    });

    it("should log warning when new_incident has no incident_id", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        task: "Some task",
        openai_api_key: "sk-test",
        // No incident_id
      });

      await sleep(200);
      expect(logs.some((l) => l.includes("missing incident_id"))).toBe(true);
    });

    it("should reject new_incident with path-traversal incident_id", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        incident_id: "../../../etc/passwd",
        task: "Some task",
        openai_api_key: "sk-test",
        model: "gpt-4o",
      });

      await sleep(200);
      expect(logs.some((l) => l.includes("invalid incident_id"))).toBe(true);
      // Should NOT have executed (no completed or error message sent back)
      expect(mockSession.prompt).not.toHaveBeenCalled();
    });

    it("should reject new_incident with special characters in incident_id", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        incident_id: "incident id with spaces",
        task: "Some task",
        openai_api_key: "sk-test",
        model: "gpt-4o",
      });

      await sleep(200);
      expect(logs.some((l) => l.includes("invalid incident_id"))).toBe(true);
      expect(mockSession.prompt).not.toHaveBeenCalled();
    });

    it("should stream output back through WebSocket during execution", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        incident_id: "incident-003",
        task: "Test streaming",
        openai_api_key: "sk-test-key",
        model: "o4-mini",
      });

      // Wait for completion
      await waitForMessage(
        (m) => m.type === "codex_completed" && m.incident_id === "incident-003",
      );

      const outputMsgs = allServerMessages.filter(
        (m) => m.type === "codex_output" && m.incident_id === "incident-003",
      );

      // The mock session emits a text_delta "Done.", which should be streamed
      expect(outputMsgs.length).toBeGreaterThanOrEqual(1);
      expect(outputMsgs.some((m) => m.output === "Done.")).toBe(true);
    });

    it("should use workspace dir with incident ID", async () => {
      const { createAgentSession } = await import("@mariozechner/pi-coding-agent");

      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        incident_id: "incident-ws-test",
        task: "Test workdir",
        openai_api_key: "sk-key",
      });

      // Wait for execution to complete
      await waitForMessage(
        (m) => m.type === "codex_completed" && m.incident_id === "incident-ws-test",
      );

      expect(createAgentSession).toHaveBeenCalledWith(
        expect.objectContaining({
          cwd: "/tmp/test-workspaces/incident-ws-test",
        }),
      );
    });
  });

  // -----------------------------------------------------------------------
  // Message routing: continue_incident
  // -----------------------------------------------------------------------

  describe("continue_incident routing", () => {
    it("should resume agent session on continue_incident", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "continue_incident",
        incident_id: "incident-010",
        session_id: "existing-session-id",
        message: "What about memory usage?",
        openai_api_key: "sk-test-key",
        model: "o4-mini",
      });

      const completedMsg = await waitForMessage(
        (m) => m.type === "codex_completed" && m.incident_id === "incident-010",
      );
      expect(completedMsg).toBeDefined();
      expect(completedMsg!.incident_id).toBe("incident-010");

      // The mock session.prompt should have been called with the follow-up message
      expect(mockSession.prompt).toHaveBeenCalledWith("What about memory usage?");
    });

    it("should reject continue_incident with invalid incident_id", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "continue_incident",
        incident_id: "../../secret",
        session_id: "existing-session-id",
        message: "Follow up",
        openai_api_key: "sk-test-key",
        model: "gpt-4o",
      });

      await sleep(200);
      expect(logs.some((l) => l.includes("invalid incident_id"))).toBe(true);
      expect(mockSession.prompt).not.toHaveBeenCalled();
    });

    it("should send error when continue_incident has no API key", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "continue_incident",
        incident_id: "incident-011",
        message: "Follow up",
        // No openai_api_key
      });

      const errorMsg = await waitForMessage(
        (m) => m.type === "codex_error" && m.incident_id === "incident-011",
      );
      expect(errorMsg).toBeDefined();
      expect(errorMsg!.error).toContain("Missing LLM settings");
    });
  });

  // -----------------------------------------------------------------------
  // Message routing: cancel_incident
  // -----------------------------------------------------------------------

  describe("cancel_incident routing", () => {
    it("should cancel active runner and send error notification", async () => {
      // Make prompt long-running
      mockSession.prompt.mockImplementation(async () => {
        await sleep(5000);
      });

      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      // Start an incident
      sendFromServer({
        type: "new_incident",
        incident_id: "incident-020",
        task: "Long running task",
        openai_api_key: "sk-test-key",
      });

      // Give it a moment to start
      await sleep(200);

      // Now cancel it
      sendFromServer({
        type: "cancel_incident",
        incident_id: "incident-020",
      });

      // Should get at least one cancellation error
      const errorMsg = await waitForMessage(
        (m) => m.type === "codex_error" && m.incident_id === "incident-020" && !!m.error?.includes("cancelled"),
      );
      expect(errorMsg).toBeDefined();
    });

    it("should ignore cancel for missing incident_id", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "cancel_incident",
        // No incident_id
      });

      await sleep(200);
      expect(logs.some((l) => l.includes("missing incident_id"))).toBe(true);
    });
  });

  // -----------------------------------------------------------------------
  // Message routing: proxy_config_update
  // -----------------------------------------------------------------------

  describe("proxy_config_update routing", () => {
    it("should cache proxy config", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      const proxyConfig: ProxyConfig = {
        url: "http://proxy:8080",
        no_proxy: "localhost",
        openai_enabled: true,
        slack_enabled: false,
        zabbix_enabled: false,
      };

      sendFromServer({
        type: "proxy_config_update",
        proxy_config: proxyConfig,
      });

      await sleep(200);
      expect(logs.some((l) => l.includes("Proxy configuration updated"))).toBe(true);
    });

    it("should use cached proxy config for new incidents when not provided in message", async () => {
      const { createAgentSession } = await import("@mariozechner/pi-coding-agent");

      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      // Set proxy config
      const proxyConfig: ProxyConfig = {
        url: "http://proxy:9090",
        no_proxy: "internal",
        openai_enabled: true,
        slack_enabled: false,
        zabbix_enabled: false,
      };

      sendFromServer({
        type: "proxy_config_update",
        proxy_config: proxyConfig,
      });

      await sleep(100);

      // Send incident without proxy_config - should use cached
      sendFromServer({
        type: "new_incident",
        incident_id: "incident-proxy-test",
        task: "Test proxy",
        openai_api_key: "sk-key",
      });

      await waitForMessage(
        (m) => m.type === "codex_completed" && m.incident_id === "incident-proxy-test",
      );

      expect(createAgentSession).toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // Message routing: unknown message type
  // -----------------------------------------------------------------------

  describe("unknown message type", () => {
    it("should log unknown message types", async () => {
      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "some_unknown_type" as any,
        incident_id: "test",
      });

      await sleep(200);
      expect(logs.some((l) => l.includes("Unknown message type: some_unknown_type"))).toBe(true);
    });
  });

  // -----------------------------------------------------------------------
  // Error propagation
  // -----------------------------------------------------------------------

  describe("error propagation", () => {
    it("should send error when runner throws", async () => {
      const { createAgentSession } = await import("@mariozechner/pi-coding-agent");
      (createAgentSession as any).mockRejectedValueOnce(new Error("Auth failed"));

      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        incident_id: "incident-err-001",
        task: "Should fail",
        openai_api_key: "sk-bad-key",
      });

      const errorMsg = await waitForMessage(
        (m) => m.type === "codex_error" && m.incident_id === "incident-err-001",
      );
      expect(errorMsg).toBeDefined();
      expect(errorMsg!.error).toContain("Auth failed");
    });

    it("should send error when runner result contains error", async () => {
      // Make the mock session throw
      mockSession.prompt.mockImplementationOnce(async () => {
        throw new Error("Model not found");
      });

      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        incident_id: "incident-err-002",
        task: "Should error in result",
        openai_api_key: "sk-key",
      });

      const errorMsg = await waitForMessage(
        (m) => m.type === "codex_error" && m.incident_id === "incident-err-002",
      );
      expect(errorMsg).toBeDefined();
      expect(errorMsg!.incident_id).toBe("incident-err-002");
    });
  });

  // -----------------------------------------------------------------------
  // LLM settings extraction
  // -----------------------------------------------------------------------

  describe("LLM settings extraction", () => {
    it("should map reasoning_effort to thinking_level", async () => {
      const { createAgentSession } = await import("@mariozechner/pi-coding-agent");

      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        incident_id: "incident-settings-001",
        task: "Test settings",
        openai_api_key: "sk-key",
        model: "gpt-4o",
        reasoning_effort: "high",
      });

      await waitForMessage(
        (m) => m.type === "codex_completed" && m.incident_id === "incident-settings-001",
      );

      expect(createAgentSession).toHaveBeenCalled();
    });

    it("should default model to o4-mini when not specified", async () => {
      const { createAgentSession } = await import("@mariozechner/pi-coding-agent");
      const { getModel } = await import("@mariozechner/pi-ai");

      await orchestrator.start();
      await waitForMessage((m) => m.type === "status");

      sendFromServer({
        type: "new_incident",
        incident_id: "incident-settings-002",
        task: "Test default model",
        openai_api_key: "sk-key",
        // No model specified
      });

      await waitForMessage(
        (m) => m.type === "codex_completed" && m.incident_id === "incident-settings-002",
      );

      // getModel should have been called with "o4-mini"
      expect(getModel).toHaveBeenCalledWith("openai", "o4-mini");
    });
  });

  // -----------------------------------------------------------------------
  // Graceful shutdown
  // -----------------------------------------------------------------------

  describe("graceful shutdown", () => {
    it("should handle stop called multiple times gracefully", async () => {
      await orchestrator.start();
      await orchestrator.stop();
      // Second stop should not throw
      await orchestrator.stop();
      expect(orchestrator.isStopped()).toBe(true);
    });
  });
});

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
