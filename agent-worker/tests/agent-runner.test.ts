import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  AgentRunner,
  mapThinkingLevel,
  resolveModel,
  type ExecuteParams,
  type ResumeParams,
} from "../src/agent-runner.js";
import type { LLMSettings, ThinkingLevel, ProxyConfig } from "../src/types.js";

// ---------------------------------------------------------------------------
// Mock the pi-mono SDK modules
// ---------------------------------------------------------------------------

// Session mock that captures all calls
function createMockSession() {
  const subscribers: Array<(event: any) => void> = [];
  return {
    sessionId: "mock-session-123",
    subscribe: vi.fn((listener: (event: any) => void) => {
      subscribers.push(listener);
      return () => {
        const idx = subscribers.indexOf(listener);
        if (idx >= 0) subscribers.splice(idx, 1);
      };
    }),
    prompt: vi.fn(async (_text: string) => {
      // Simulate events: message_update with text_delta, then turn_end
      for (const sub of subscribers) {
        sub({
          type: "message_update",
          message: {},
          assistantMessageEvent: {
            type: "text_delta",
            contentIndex: 0,
            delta: "Analysis complete.",
            partial: {},
          },
        });
      }
      for (const sub of subscribers) {
        sub({
          type: "turn_end",
          message: {
            role: "assistant",
            usage: { totalTokens: 1500, input: 1000, output: 500 },
          },
          toolResults: [],
        });
      }
    }),
    abort: vi.fn(async () => {}),
    getLastAssistantText: vi.fn(() => "Analysis complete."),
    _emitEvent: (event: any) => {
      for (const sub of subscribers) {
        sub(event);
      }
    },
    _subscribers: subscribers,
  };
}

let mockSession = createMockSession();
let createAgentSessionCalls: any[] = [];

vi.mock("@mariozechner/pi-coding-agent", () => {
  return {
    createAgentSession: vi.fn(async (opts: any) => {
      createAgentSessionCalls.push(opts);
      return { session: mockSession, extensionsResult: {} };
    }),
    AgentSession: vi.fn(),
    AuthStorage: {
      inMemory: vi.fn(() => ({
        setRuntimeApiKey: vi.fn(),
      })),
    },
    ModelRegistry: vi.fn().mockImplementation(() => ({})),
    SessionManager: {
      inMemory: vi.fn(() => ({
        newSession: vi.fn(),
        getSessionId: vi.fn(() => "mock-session-123"),
        getSessionFile: vi.fn(() => undefined),
      })),
      create: vi.fn(() => ({
        newSession: vi.fn(),
        getSessionId: vi.fn(() => "mock-session-123"),
        getSessionFile: vi.fn(() => undefined),
      })),
      continueRecent: vi.fn(() => ({
        newSession: vi.fn(),
        getSessionId: vi.fn(() => "mock-session-123"),
        getSessionFile: vi.fn(() => undefined),
      })),
      open: vi.fn(() => ({
        newSession: vi.fn(),
        getSessionId: vi.fn(() => "mock-session-123"),
        getSessionFile: vi.fn(() => undefined),
      })),
    },
    SettingsManager: {
      inMemory: vi.fn(() => ({})),
      create: vi.fn(() => ({})),
    },
    DefaultResourceLoader: vi.fn().mockImplementation(() => ({
      reload: vi.fn(async () => {}),
      getSkills: vi.fn(() => ({ skills: [], diagnostics: [] })),
      getPrompts: vi.fn(() => ({ prompts: [], diagnostics: [] })),
      getThemes: vi.fn(() => ({ themes: [], diagnostics: [] })),
      getExtensions: vi.fn(() => ({})),
      getAgentsFiles: vi.fn(() => ({ agentsFiles: [] })),
      getSystemPrompt: vi.fn(() => undefined),
      getAppendSystemPrompt: vi.fn(() => []),
      getPathMetadata: vi.fn(() => new Map()),
      extendResources: vi.fn(),
    })),
    createBashToolDefinition: vi.fn((_cwd: string, _opts?: any) => ({
      name: "bash",
      label: "Bash",
      description: "Execute bash commands",
      parameters: {},
      execute: vi.fn(),
      _spawnHookOpts: _opts,
    })),
  };
});

vi.mock("@mariozechner/pi-ai", () => {
  return {
    getModel: vi.fn((provider: string, modelId: string) => {
      // Return a mock model for known combinations
      if (provider === "anthropic" && modelId === "claude-sonnet-4-5-20250929") {
        return {
          id: "claude-sonnet-4-5-20250929",
          name: "Claude Sonnet 4.5",
          api: "anthropic-messages",
          provider: "anthropic",
          baseUrl: "https://api.anthropic.com",
          reasoning: true,
          input: ["text", "image"],
          cost: { input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75 },
          contextWindow: 200000,
          maxTokens: 16384,
        };
      }
      if (provider === "openai" && modelId === "gpt-4o") {
        return {
          id: "gpt-4o",
          name: "GPT-4o",
          api: "openai-responses",
          provider: "openai",
          baseUrl: "https://api.openai.com/v1",
          reasoning: false,
          input: ["text", "image"],
          cost: { input: 2.5, output: 10, cacheRead: 1.25, cacheWrite: 0 },
          contextWindow: 128000,
          maxTokens: 16384,
        };
      }
      // Simulate pi-ai behavior where unknown models may return undefined
      // instead of throwing (observed for custom providers).
      if (provider === "custom" && modelId === "my-model-undefined-return") {
        return undefined as any;
      }
      // Unknown model - throw to trigger fallback
      throw new Error(`Unknown model: ${provider}/${modelId}`);
    }),
  };
});


// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeLLMSettings(overrides?: Partial<LLMSettings>): LLMSettings {
  return {
    provider: "anthropic",
    api_key: "sk-test-key-123",
    model: "claude-sonnet-4-5-20250929",
    thinking_level: "medium",
    ...overrides,
  };
}

function makeExecuteParams(overrides?: Partial<ExecuteParams>): ExecuteParams {
  return {
    incidentId: "inc-001",
    task: "Investigate high CPU on web-01",
    llmSettings: makeLLMSettings(),
    workDir: "/tmp/workspace",
    onOutput: vi.fn(),
    ...overrides,
  };
}

function makeResumeParams(overrides?: Partial<ResumeParams>): ResumeParams {
  return {
    incidentId: "inc-001",
    sessionId: "mock-session-123",
    message: "Check disk usage too",
    llmSettings: makeLLMSettings(),
    workDir: "/tmp/workspace",
    onOutput: vi.fn(),
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("mapThinkingLevel", () => {
  it("should map 'off' to 'minimal'", () => {
    expect(mapThinkingLevel("off")).toBe("minimal");
  });

  it("should map 'minimal' to 'minimal'", () => {
    expect(mapThinkingLevel("minimal")).toBe("minimal");
  });

  it("should map 'low' to 'low'", () => {
    expect(mapThinkingLevel("low")).toBe("low");
  });

  it("should map 'medium' to 'medium'", () => {
    expect(mapThinkingLevel("medium")).toBe("medium");
  });

  it("should map 'high' to 'high'", () => {
    expect(mapThinkingLevel("high")).toBe("high");
  });

  it("should map 'xhigh' to 'xhigh'", () => {
    expect(mapThinkingLevel("xhigh")).toBe("xhigh");
  });

  it("should default to 'medium' for unknown values", () => {
    expect(mapThinkingLevel("unknown" as ThinkingLevel)).toBe("medium");
  });
});

describe("resolveModel", () => {
  it("should return model from pi-ai registry for known provider/model", () => {
    const model = resolveModel("anthropic", "claude-sonnet-4-5-20250929");
    expect(model.id).toBe("claude-sonnet-4-5-20250929");
    expect(model.provider).toBe("anthropic");
    expect(model.api).toBe("anthropic-messages");
  });

  it("should return model from pi-ai registry for OpenAI", () => {
    const model = resolveModel("openai", "gpt-4o");
    expect(model.id).toBe("gpt-4o");
    expect(model.provider).toBe("openai");
  });

  it("should create custom model spec for unknown provider/model", () => {
    const model = resolveModel("custom", "my-model", "https://my-api.example.com");
    expect(model.id).toBe("my-model");
    expect(model.provider).toBe("custom");
    expect(model.api).toBe("openai-completions");
    expect(model.baseUrl).toBe("https://my-api.example.com");
  });

  it("should create custom model spec when getModel returns undefined", () => {
    const model = resolveModel("custom", "my-model-undefined-return", "https://my-api.example.com");
    expect(model.id).toBe("my-model-undefined-return");
    expect(model.provider).toBe("custom");
    expect(model.api).toBe("openai-completions");
    expect(model.baseUrl).toBe("https://my-api.example.com");
  });

  it("should create custom model spec for openrouter", () => {
    const model = resolveModel("openrouter", "anthropic/claude-3.5-sonnet");
    expect(model.id).toBe("anthropic/claude-3.5-sonnet");
    expect(model.provider).toBe("openrouter");
    expect(model.api).toBe("openai-completions");
  });

  it("should use correct API type for known provider with unknown model", () => {
    const model = resolveModel("google", "gemini-new-model");
    expect(model.api).toBe("google-generative-ai");
    expect(model.provider).toBe("google");
  });
});

describe("AgentRunner", () => {
  let runner: AgentRunner;
  const originalEnv = { ...process.env };

  beforeEach(() => {
    vi.clearAllMocks();
    mockSession = createMockSession();
    createAgentSessionCalls = [];
    runner = new AgentRunner({ mcpGatewayUrl: "http://mcp-gateway:8080" });
    // Reset env
    delete process.env.HTTP_PROXY;
    delete process.env.HTTPS_PROXY;
    delete process.env.NO_PROXY;
  });

  afterEach(() => {
    // Restore env
    process.env = { ...originalEnv };
  });

  // -----------------------------------------------------------------------
  // execute
  // -----------------------------------------------------------------------

  describe("execute", () => {
    it("should create a session with correct parameters", async () => {
      const params = makeExecuteParams();
      await runner.execute(params);

      expect(createAgentSessionCalls).toHaveLength(1);
      const opts = createAgentSessionCalls[0];
      expect(opts.cwd).toBe("/tmp/workspace");
      expect(opts.model.id).toBe("claude-sonnet-4-5-20250929");
      expect(opts.thinkingLevel).toBe("medium");
    });

    it("should use incident ID as deterministic session ID for new sessions", async () => {
      const { SessionManager } = await import("@mariozechner/pi-coding-agent");
      const params = makeExecuteParams({ incidentId: "inc-uuid-abc-123" });
      await runner.execute(params);

      // SessionManager.create should have been called (not continueRecent)
      expect(SessionManager.create).toHaveBeenCalled();

      // newSession should have been called with the incident ID
      const mockSessionManager = (SessionManager.create as any).mock.results[
        (SessionManager.create as any).mock.results.length - 1
      ].value;
      expect(mockSessionManager.newSession).toHaveBeenCalledWith({ id: "inc-uuid-abc-123" });
    });

    it("should pass sessionDir to SessionManager.create for workspace isolation", async () => {
      const { SessionManager } = await import("@mariozechner/pi-coding-agent");
      const params = makeExecuteParams({ workDir: "/tmp/workspace" });
      await runner.execute(params);

      // SessionManager.create should receive workDir and sessionDir
      expect(SessionManager.create).toHaveBeenCalledWith(
        "/tmp/workspace",
        "/tmp/workspace/.sessions",
      );
    });

    it("should NOT call newSession with deterministic ID for resume", async () => {
      const { SessionManager } = await import("@mariozechner/pi-coding-agent");
      const params = makeResumeParams({ incidentId: "inc-resume-456" });
      await runner.resume(params);

      // continueRecent should have been called (not create)
      expect(SessionManager.continueRecent).toHaveBeenCalled();

      // newSession should NOT have been called (resume uses existing session)
      const mockSessionManager = (SessionManager.continueRecent as any).mock.results[
        (SessionManager.continueRecent as any).mock.results.length - 1
      ].value;
      expect(mockSessionManager.newSession).not.toHaveBeenCalled();
    });

    it("should pass sessionDir to SessionManager.continueRecent for resume", async () => {
      const { SessionManager } = await import("@mariozechner/pi-coding-agent");
      const params = makeResumeParams({ workDir: "/tmp/workspace" });
      await runner.resume(params);

      expect(SessionManager.continueRecent).toHaveBeenCalledWith(
        "/tmp/workspace",
        "/tmp/workspace/.sessions",
      );
    });

    it("should call session.prompt with the task", async () => {
      const params = makeExecuteParams({ task: "Check memory usage" });
      await runner.execute(params);

      expect(mockSession.prompt).toHaveBeenCalledWith("Check memory usage");
    });

    it("should return ExecuteResult with session_id and response", async () => {
      const result = await runner.execute(makeExecuteParams());

      expect(result.session_id).toBe("mock-session-123");
      expect(result.response).toBe("Analysis complete.");
      expect(result.tokens_used).toBe(1500);
      expect(result.execution_time_ms).toBeGreaterThanOrEqual(0);
      expect(result.error).toBeUndefined();
    });

    it("should stream output via onOutput callback", async () => {
      const onOutput = vi.fn();
      const params = makeExecuteParams({ onOutput });
      await runner.execute(params);

      // Should have received text_delta output
      expect(onOutput).toHaveBeenCalledWith("Analysis complete.");
    });

    it("should forward events to onEvent callback", async () => {
      const onEvent = vi.fn();
      const params = makeExecuteParams({ onEvent });
      await runner.execute(params);

      expect(onEvent).toHaveBeenCalled();
      const eventTypes = onEvent.mock.calls.map((c: any[]) => c[0].type);
      expect(eventTypes).toContain("message_update");
      expect(eventTypes).toContain("turn_end");
    });

    it("should handle execution errors gracefully", async () => {
      mockSession.prompt.mockRejectedValueOnce(new Error("API rate limit exceeded"));

      const result = await runner.execute(makeExecuteParams());

      expect(result.error).toBe("API rate limit exceeded");
      expect(result.session_id).toBe("mock-session-123");
      expect(result.execution_time_ms).toBeGreaterThanOrEqual(0);
    });

    it("should clean up session from active map after completion", async () => {
      const params = makeExecuteParams({ incidentId: "inc-cleanup" });
      await runner.execute(params);

      expect(runner.hasActiveSession("inc-cleanup")).toBe(false);
    });

    it("should clean up session from active map after error", async () => {
      mockSession.prompt.mockRejectedValueOnce(new Error("fail"));

      const params = makeExecuteParams({ incidentId: "inc-err-cleanup" });
      await runner.execute(params);

      expect(runner.hasActiveSession("inc-err-cleanup")).toBe(false);
    });

    it("should use different providers", async () => {
      const params = makeExecuteParams({
        llmSettings: makeLLMSettings({
          provider: "openai",
          api_key: "sk-openai-key",
          model: "gpt-4o",
          thinking_level: "high",
        }),
      });
      await runner.execute(params);

      const opts = createAgentSessionCalls[0];
      expect(opts.model.id).toBe("gpt-4o");
      expect(opts.thinkingLevel).toBe("high");
    });

    it("should set runtime API key on AuthStorage", async () => {
      const { AuthStorage } = await import("@mariozechner/pi-coding-agent");
      const params = makeExecuteParams({
        llmSettings: makeLLMSettings({
          provider: "anthropic",
          api_key: "sk-ant-my-key",
        }),
      });
      await runner.execute(params);

      // AuthStorage.inMemory() was called and setRuntimeApiKey was called on the result
      const authInstance = (AuthStorage as any).inMemory.mock.results[0].value;
      expect(authInstance.setRuntimeApiKey).toHaveBeenCalledWith(
        "anthropic",
        "sk-ant-my-key",
      );
    });

    it("should construct ModelRegistry with AuthStorage and pass to session (getApiKeyAndHeaders not called directly)", async () => {
      const { AuthStorage, ModelRegistry } = await import("@mariozechner/pi-coding-agent");
      const params = makeExecuteParams({
        llmSettings: makeLLMSettings({
          provider: "openai",
          api_key: "sk-openai-key",
        }),
      });
      await runner.execute(params);

      // ModelRegistry should be constructed with the AuthStorage instance
      const authInstance = (AuthStorage as any).inMemory.mock.results[0].value;
      expect(ModelRegistry).toHaveBeenCalledWith(authInstance);

      // The resulting modelRegistry should be passed to createAgentSession
      const opts = createAgentSessionCalls[0];
      expect(opts.modelRegistry).toBeDefined();

      // We never call getApiKey or getApiKeyAndHeaders directly —
      // the SDK handles key resolution internally via the modelRegistry.
      // This verifies our constructor-only usage is compatible with 0.63.0+
      // where getApiKey() was replaced by getApiKeyAndHeaders().
    });

    it("should pass bash tool definition and gateway tools as customTools", async () => {
      const params = makeExecuteParams({ incidentId: "inc-tools" });
      await runner.execute(params);

      const opts = createAgentSessionCalls[0];
      expect(opts.customTools).toBeDefined();
      expect(opts.customTools).toHaveLength(6);

      const toolNames = opts.customTools.map((t: any) => t.name);
      expect(toolNames).toContain("bash");
      expect(toolNames).toContain("gateway_call");
      expect(toolNames).toContain("list_tools_for_tool_type");
      expect(toolNames).toContain("get_tool_detail");
      expect(toolNames).toContain("list_tool_types");
      expect(toolNames).toContain("execute_script");

      // All custom tools must have parameters and execute
      for (const tool of opts.customTools) {
        expect(tool.parameters).toBeDefined();
        expect(typeof tool.execute).toBe("function");
      }
    });

    it("should attach typed promptGuidelines array to the bash tool definition", async () => {
      const params = makeExecuteParams();
      await runner.execute(params);

      const opts = createAgentSessionCalls[0];
      // Bash tool is now in customTools as a proper ToolDefinition
      const bashTool = opts.customTools.find(
        (t: any) => t.name === "bash",
      );
      expect(bashTool).toBeDefined();
      expect(bashTool.promptGuidelines).toBeDefined();
      // promptGuidelines is now a typed string[] (ToolDefinition.promptGuidelines)
      expect(Array.isArray(bashTool.promptGuidelines)).toBe(true);
      expect(bashTool.promptGuidelines.length).toBeGreaterThan(0);

      const allGuidelines = bashTool.promptGuidelines.join("\n");
      expect(allGuidelines).toContain("gateway_call");
      expect(allGuidelines).toContain("SKILL.md");
      expect(allGuidelines).toContain("list_tools_for_tool_type");
      expect(allGuidelines).not.toContain("python3 -c");
      expect(allGuidelines).not.toContain("PYTHONPATH");
      // Verify stronger opening line lists all 5 tools
      expect(allGuidelines).toContain("CRITICAL: You only have 5 tools available");
      expect(allGuidelines).toContain("execute_script");
      expect(allGuidelines).toContain("get_tool_detail");
      expect(allGuidelines).toContain("list_tool_types");
      // Verify "Tool not found" guidance
      expect(allGuidelines).toContain("Tool not found");
      expect(allGuidelines).toContain("you are calling it wrong");
    });

    it("should configure bash spawnHook with MCP env vars via ToolDefinition", async () => {
      const params = makeExecuteParams({ incidentId: "inc-env" });
      await runner.execute(params);

      const opts = createAgentSessionCalls[0];
      // Bash tool definition is in customTools with spawnHook options
      const bashTool = opts.customTools.find(
        (t: any) => t.name === "bash",
      );
      expect(bashTool).toBeDefined();

      // Verify the spawnHook was passed to createBashToolDefinition
      const hookOpts = bashTool._spawnHookOpts;
      expect(hookOpts).toBeDefined();
      expect(hookOpts.spawnHook).toBeTypeOf("function");

      // Call the spawnHook and verify it injects MCP env vars into subprocess env
      const hookResult = hookOpts.spawnHook({ env: { PATH: "/usr/bin" } });
      expect(hookResult.env.MCP_GATEWAY_URL).toBeDefined();
      expect(hookResult.env.INCIDENT_ID).toBe("inc-env");
      // Original env vars should be preserved
      expect(hookResult.env.PATH).toBe("/usr/bin");
    });

    it("should use fallback response from getLastAssistantText when no text_delta events", async () => {
      // Override prompt to emit no text_delta events
      mockSession.prompt.mockImplementationOnce(async () => {
        // No events emitted
      });
      mockSession.getLastAssistantText.mockReturnValueOnce("Fallback response");

      const result = await runner.execute(makeExecuteParams());

      expect(result.response).toBe("Fallback response");
    });
  });

  // -----------------------------------------------------------------------
  // resume
  // -----------------------------------------------------------------------

  describe("resume", () => {
    it("should create a new session and prompt with the message", async () => {
      const params = makeResumeParams({ message: "Also check disk space" });
      const result = await runner.resume(params);

      expect(mockSession.prompt).toHaveBeenCalledWith("Also check disk space");
      expect(result.session_id).toBe("mock-session-123");
    });

    it("should return ExecuteResult with metrics", async () => {
      const result = await runner.resume(makeResumeParams());

      expect(result.tokens_used).toBe(1500);
      expect(result.execution_time_ms).toBeGreaterThanOrEqual(0);
      expect(result.error).toBeUndefined();
    });

    it("should handle resume errors gracefully", async () => {
      mockSession.prompt.mockRejectedValueOnce(new Error("Session expired"));

      const result = await runner.resume(makeResumeParams());

      expect(result.error).toBe("Session expired");
    });
  });

  // -----------------------------------------------------------------------
  // cancel
  // -----------------------------------------------------------------------

  describe("cancel", () => {
    it("should abort active session", async () => {
      // Start an execution that we can cancel
      const session = createMockSession();
      // Make prompt hang so we can cancel it
      session.prompt.mockImplementation(() => new Promise(() => {}));

      // We need to inject the session into activeSessions
      // Do this by starting execute (it won't resolve because prompt hangs)
      mockSession = session;
      const execPromise = runner.execute(makeExecuteParams({ incidentId: "inc-cancel" }));

      // Small delay to ensure session is registered
      await new Promise((resolve) => setTimeout(resolve, 10));

      expect(runner.hasActiveSession("inc-cancel")).toBe(true);

      await runner.cancel("inc-cancel");

      expect(session.abort).toHaveBeenCalled();
      expect(runner.hasActiveSession("inc-cancel")).toBe(false);
    });

    it("should be a no-op for unknown incident ID", async () => {
      // Should not throw
      await runner.cancel("nonexistent-incident");
    });

    it("should trigger signal propagation to active tool calls via session.abort()", async () => {
      // Verify that cancel() calls session.abort() which in pi-mono 0.63.1
      // propagates the AbortSignal to all active tool execute() calls.
      const session = createMockSession();
      let abortCalled = false;
      session.abort.mockImplementation(async () => {
        abortCalled = true;
      });
      session.prompt.mockImplementation(() => new Promise(() => {})); // hang

      mockSession = session;
      const execPromise = runner.execute(makeExecuteParams({ incidentId: "inc-signal-prop" }));

      await new Promise((resolve) => setTimeout(resolve, 10));
      expect(runner.hasActiveSession("inc-signal-prop")).toBe(true);

      await runner.cancel("inc-signal-prop");

      expect(abortCalled).toBe(true);
      expect(session.abort).toHaveBeenCalledTimes(1);
      expect(runner.hasActiveSession("inc-signal-prop")).toBe(false);
    });
  });

  // -----------------------------------------------------------------------
  // dispose
  // -----------------------------------------------------------------------

  describe("dispose", () => {
    it("should abort all active sessions", async () => {
      const session1 = createMockSession();
      const session2 = createMockSession();
      session1.prompt.mockImplementation(() => new Promise(() => {}));
      session2.prompt.mockImplementation(() => new Promise(() => {}));

      // Start two executions
      mockSession = session1;
      runner.execute(makeExecuteParams({ incidentId: "inc-d1" }));
      await new Promise((resolve) => setTimeout(resolve, 10));

      mockSession = session2;
      runner.execute(makeExecuteParams({ incidentId: "inc-d2" }));
      await new Promise((resolve) => setTimeout(resolve, 10));

      await runner.dispose();

      expect(session1.abort).toHaveBeenCalled();
      expect(session2.abort).toHaveBeenCalled();
      expect(runner.hasActiveSession("inc-d1")).toBe(false);
      expect(runner.hasActiveSession("inc-d2")).toBe(false);
    });
  });

  // -----------------------------------------------------------------------
  // Event streaming
  // -----------------------------------------------------------------------

  describe("event streaming", () => {
    it("should format tool execution summary with args and output", async () => {
      const onOutput = vi.fn();
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({
            type: "tool_execution_start",
            toolCallId: "tc-1",
            toolName: "ssh_execute_command",
            args: { command: "uptime" },
          });
          sub({
            type: "tool_execution_update",
            toolCallId: "tc-1",
            toolName: "ssh_execute_command",
            args: { command: "uptime" },
            partialResult: {
              content: [{ type: "text", text: "partial output" }],
            },
          });
          sub({
            type: "tool_execution_end",
            toolCallId: "tc-1",
            toolName: "ssh_execute_command",
            result: {
              content: [{ type: "text", text: "final output" }],
            },
            isError: false,
          });
        }
      });

      await runner.execute(makeExecuteParams({ onOutput }));

      const output = onOutput.mock.calls.map((call: any[]) => call[0]).join("");
      expect(output).toContain("🛠️ Running: ssh_execute_command");
      expect(output).toContain("✅ Ran: ssh_execute_command");
      expect(output).toContain("Args:");
      expect(output).toContain("\"command\": \"uptime\"");
      expect(output).toContain("Output:");
      expect(output).toContain("partial output");
      expect(output).toContain("final output");
    });

    it("should format tool_execution_end error events", async () => {
      const onOutput = vi.fn();
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({ type: "tool_execution_start", toolCallId: "tc-2", toolName: "ssh_execute_command", args: {} });
          sub({ type: "tool_execution_end", toolCallId: "tc-2", toolName: "ssh_execute_command", result: {}, isError: true });
        }
      });

      await runner.execute(makeExecuteParams({ onOutput }));

      const output = onOutput.mock.calls.map((call: any[]) => call[0]).join("");
      expect(output).toContain("❌ Failed: ssh_execute_command");
    });

    it("should emit thinking content to execution log", async () => {
      const onOutput = vi.fn();
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({
            type: "message_update",
            message: {},
            assistantMessageEvent: {
              type: "thinking_start",
              contentIndex: 0,
              partial: {},
            },
          });
          sub({
            type: "message_update",
            message: {},
            assistantMessageEvent: {
              type: "thinking_delta",
              contentIndex: 0,
              delta: "Investigating CPU spike",
              partial: {},
            },
          });
          sub({
            type: "message_update",
            message: {},
            assistantMessageEvent: {
              type: "thinking_end",
              contentIndex: 0,
              content: "Investigating CPU spike",
              partial: {},
            },
          });
        }
      });

      await runner.execute(makeExecuteParams({ onOutput }));

      const output = onOutput.mock.calls.map((call: any[]) => call[0]).join("");
      expect(output).toContain("🤔 Investigating CPU spike");
    });

    it("should stream compaction_start and compaction_end events", async () => {
      const onOutput = vi.fn();
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({ type: "compaction_start", reason: "threshold" });
          sub({ type: "compaction_end", reason: "threshold", aborted: false, willRetry: false });
        }
      });

      await runner.execute(makeExecuteParams({ onOutput }));

      const output = onOutput.mock.calls.map((call: any[]) => call[0]).join("");
      expect(output).toContain("Compacting context");
      expect(output).toContain("threshold");
      expect(output).toContain("compaction complete");
    });

    it("should stream compaction_end with aborted status", async () => {
      const onOutput = vi.fn();
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({ type: "compaction_start", reason: "overflow" });
          sub({ type: "compaction_end", reason: "overflow", aborted: true, willRetry: false });
        }
      });

      await runner.execute(makeExecuteParams({ onOutput }));

      const output = onOutput.mock.calls.map((call: any[]) => call[0]).join("");
      expect(output).toContain("compaction aborted");
    });

    it("should include error message and willRetry in compaction_end when aborted", async () => {
      const onOutput = vi.fn();
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({
            type: "compaction_end",
            reason: "overflow",
            aborted: true,
            willRetry: true,
            errorMessage: "token limit exceeded",
          });
        }
      });

      await runner.execute(makeExecuteParams({ onOutput }));

      const output = onOutput.mock.calls.map((call: any[]) => call[0]).join("");
      expect(output).toContain("compaction aborted");
      expect(output).toContain("token limit exceeded");
      expect(output).toContain("will retry");
    });

    it("should stream auto_retry_start events", async () => {
      const onOutput = vi.fn();
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({
            type: "auto_retry_start",
            attempt: 2,
            maxAttempts: 3,
            delayMs: 1000,
            errorMessage: "server_error",
          });
        }
      });

      await runner.execute(makeExecuteParams({ onOutput }));

      const output = onOutput.mock.calls.map((call: any[]) => call[0]).join("");
      expect(output).toContain("Retrying");
      expect(output).toContain("attempt 2/3");
      expect(output).toContain("server_error");
    });

    it("should stream auto_retry_end failure events with attempt count", async () => {
      const onOutput = vi.fn();
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({
            type: "auto_retry_end",
            success: false,
            attempt: 3,
            finalError: "API quota exceeded",
          });
        }
      });

      await runner.execute(makeExecuteParams({ onOutput }));

      const output = onOutput.mock.calls.map((call: any[]) => call[0]).join("");
      expect(output).toContain("retries exhausted");
      expect(output).toContain("3 attempts");
      expect(output).toContain("API quota exceeded");
    });

    it("should not emit output for successful auto_retry_end", async () => {
      const onOutput = vi.fn();
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({ type: "auto_retry_end", success: true, attempt: 2 });
        }
      });

      await runner.execute(makeExecuteParams({ onOutput }));

      const output = onOutput.mock.calls.map((call: any[]) => call[0]).join("");
      expect(output).not.toContain("retries exhausted");
    });

    it("should accumulate tokens from turn_end events", async () => {
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({
            type: "turn_end",
            message: { role: "assistant", usage: { totalTokens: 500 } },
            toolResults: [],
          });
          sub({
            type: "turn_end",
            message: { role: "assistant", usage: { totalTokens: 300 } },
            toolResults: [],
          });
        }
      });

      const result = await runner.execute(makeExecuteParams());

      expect(result.tokens_used).toBe(800);
    });
  });

  // -----------------------------------------------------------------------
  // Session export
  // -----------------------------------------------------------------------

  describe("session export", () => {
    it("should export session JSONL to workDir/session_export.jsonl on success", async () => {
      const fs = await import("node:fs");
      const path = await import("node:path");
      const tmpDir = fs.mkdtempSync(path.join("/tmp", "agent-export-"));

      // Create a fake session file that getSessionFile will point to
      const fakeSessionFile = path.join(tmpDir, "session.jsonl");
      fs.writeFileSync(fakeSessionFile, '{"type":"header","id":"h1"}\n{"type":"message","role":"user","content":"test"}\n');

      // Make SessionManager.create return a mock with getSessionFile pointing to fake file
      const { SessionManager } = await import("@mariozechner/pi-coding-agent");
      (SessionManager.create as any).mockReturnValueOnce({
        newSession: vi.fn(),
        getSessionId: vi.fn(() => "mock-session-123"),
        getSessionFile: vi.fn(() => fakeSessionFile),
      });

      const result = await runner.execute(makeExecuteParams({ workDir: tmpDir }));

      const expectedExportPath = path.join(tmpDir, "session_export.jsonl");
      expect(result.session_export).toBe(expectedExportPath);
      expect(fs.existsSync(expectedExportPath)).toBe(true);

      const exportedContent = fs.readFileSync(expectedExportPath, "utf-8");
      expect(exportedContent).toContain('"type":"header"');
      expect(exportedContent).toContain('"type":"message"');

      // Cleanup
      fs.rmSync(tmpDir, { recursive: true });
    });

    it("should export session JSONL even on execution error", async () => {
      const fs = await import("node:fs");
      const path = await import("node:path");
      const tmpDir = fs.mkdtempSync(path.join("/tmp", "agent-export-err-"));

      const fakeSessionFile = path.join(tmpDir, "session.jsonl");
      fs.writeFileSync(fakeSessionFile, '{"type":"header","id":"h1"}\n');

      const { SessionManager } = await import("@mariozechner/pi-coding-agent");
      (SessionManager.create as any).mockReturnValueOnce({
        newSession: vi.fn(),
        getSessionId: vi.fn(() => "mock-session-123"),
        getSessionFile: vi.fn(() => fakeSessionFile),
      });

      mockSession.prompt.mockRejectedValueOnce(new Error("API error"));

      const result = await runner.execute(makeExecuteParams({ workDir: tmpDir }));

      expect(result.error).toBe("API error");
      const expectedExportPath = path.join(tmpDir, "session_export.jsonl");
      expect(result.session_export).toBe(expectedExportPath);
      expect(fs.existsSync(expectedExportPath)).toBe(true);

      fs.rmSync(tmpDir, { recursive: true });
    });

    it("should return undefined session_export when no session file exists", async () => {
      const result = await runner.execute(makeExecuteParams());

      // Default mock returns undefined for getSessionFile
      expect(result.session_export).toBeUndefined();
    });

    it("should not fail execution when session export fails", async () => {
      const { SessionManager } = await import("@mariozechner/pi-coding-agent");
      (SessionManager.create as any).mockReturnValueOnce({
        newSession: vi.fn(),
        getSessionId: vi.fn(() => "mock-session-123"),
        // Point to a non-existent file — copyFileSync will throw
        getSessionFile: vi.fn(() => "/nonexistent/path/session.jsonl"),
      });

      const result = await runner.execute(makeExecuteParams());

      // Execution should still succeed
      expect(result.session_id).toBe("mock-session-123");
      expect(result.response).toBe("Analysis complete.");
      expect(result.session_export).toBeUndefined();
      expect(result.error).toBeUndefined();
    });
  });

  // -----------------------------------------------------------------------
  // Proxy configuration
  // -----------------------------------------------------------------------

  describe("proxy configuration", () => {
    it("should set env vars and undici EnvHttpProxyAgent when llm_enabled is true", async () => {
      const proxyConfig: ProxyConfig = {
        url: "http://proxy.example.com:8080",
        no_proxy: "localhost,127.0.0.1",
        llm_enabled: true,
        slack_enabled: false,
        zabbix_enabled: false,
        victoria_metrics_enabled: false,
      };

      let capturedHttpProxy: string | undefined;
      let capturedHttpsProxy: string | undefined;
      let capturedNoProxy: string | undefined;
      let capturedDispatcher: string | undefined;

      // Capture env vars and dispatcher during session creation
      const { createAgentSession } = await import("@mariozechner/pi-coding-agent");
      (createAgentSession as any).mockImplementationOnce(async () => {
        capturedHttpProxy = process.env.HTTP_PROXY;
        capturedHttpsProxy = process.env.HTTPS_PROXY;
        capturedNoProxy = process.env.NO_PROXY;
        const undici = await import("undici");
        capturedDispatcher = undici.getGlobalDispatcher()?.constructor?.name;
        return { session: mockSession, extensionsResult: {} };
      });

      await runner.execute(makeExecuteParams({ proxyConfig }));

      expect(capturedHttpProxy).toBe("http://proxy.example.com:8080");
      expect(capturedHttpsProxy).toBe("http://proxy.example.com:8080");
      expect(capturedNoProxy).toBe("localhost,127.0.0.1");
      expect(capturedDispatcher).toBe("EnvHttpProxyAgent");
    });

    it("should NOT set proxy when llm_enabled is false", async () => {
      const proxyConfig: ProxyConfig = {
        url: "http://proxy.example.com:8080",
        no_proxy: "",
        llm_enabled: false,
        slack_enabled: true,
        zabbix_enabled: true,
        victoria_metrics_enabled: false,
      };

      let capturedHttpProxy: string | undefined;
      let capturedDispatcher: string | undefined;

      const { createAgentSession } = await import("@mariozechner/pi-coding-agent");
      (createAgentSession as any).mockImplementationOnce(async () => {
        capturedHttpProxy = process.env.HTTP_PROXY;
        const undici = await import("undici");
        capturedDispatcher = undici.getGlobalDispatcher()?.constructor?.name;
        return { session: mockSession, extensionsResult: {} };
      });

      await runner.execute(makeExecuteParams({ proxyConfig }));

      expect(capturedHttpProxy).toBe("");
      expect(capturedDispatcher).toBe("Agent");
    });

    it("should clear proxy env vars and reset dispatcher when no proxy config provided", async () => {
      // Set some proxy vars first
      process.env.HTTP_PROXY = "http://old-proxy:1234";
      process.env.HTTPS_PROXY = "http://old-proxy:1234";

      let capturedHttpProxy: string | undefined;
      let capturedDispatcher: string | undefined;

      const { createAgentSession } = await import("@mariozechner/pi-coding-agent");
      (createAgentSession as any).mockImplementationOnce(async () => {
        capturedHttpProxy = process.env.HTTP_PROXY;
        const undici = await import("undici");
        capturedDispatcher = undici.getGlobalDispatcher()?.constructor?.name;
        return { session: mockSession, extensionsResult: {} };
      });

      await runner.execute(makeExecuteParams({ proxyConfig: undefined }));

      expect(capturedHttpProxy).toBe("");
      expect(capturedDispatcher).toBe("Agent");
    });
  });
});
