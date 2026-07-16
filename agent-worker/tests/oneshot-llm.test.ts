import { describe, it, expect, vi, beforeEach } from "vitest";

// ---------------------------------------------------------------------------
// Mock pi-ai's `complete()` (compat entrypoint since 0.80.0) and
// `getBuiltinModel()` (providers/all). agent-runner.resolveModel() calls into
// getBuiltinModel(); we leave it to fall through to the custom model spec
// path so we don't need to mock the whole registry.
// ---------------------------------------------------------------------------

const completeMock = vi.fn();

vi.mock("@earendil-works/pi-ai/compat", () => ({
  complete: (...args: unknown[]) => completeMock(...args),
}));

vi.mock("@earendil-works/pi-ai/providers/all", () => ({
  getBuiltinModel: vi.fn(() => undefined),
}));

// Mock proxy.ts to avoid touching undici's real global dispatcher in tests.
const applyProxyConfigMock = vi.fn();
vi.mock("../src/proxy.js", () => ({
  applyProxyConfig: (...args: unknown[]) => applyProxyConfigMock(...args),
}));

import { runOneshotLLM } from "../src/oneshot-llm.js";
import type { LLMSettings, ProxyConfig } from "../src/types.js";

const validSettings: LLMSettings = {
  provider: "openai",
  api_key: "sk-test",
  model: "gpt-4o-mini",
  thinking_level: "medium",
};

function assistantText(text: string) {
  return {
    role: "assistant" as const,
    content: [{ type: "text" as const, text }],
    api: "openai-completions" as const,
    provider: "openai" as const,
    model: "gpt-4o-mini",
    usage: {
      input: 1,
      output: 1,
      cacheRead: 0,
      cacheWrite: 0,
      totalTokens: 2,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
    },
    stopReason: "stop" as const,
    timestamp: Date.now(),
  };
}

beforeEach(() => {
  completeMock.mockReset();
  applyProxyConfigMock.mockReset();
});

describe("runOneshotLLM", () => {
  it("returns assistant text on happy path", async () => {
    completeMock.mockResolvedValue(assistantText("hello world"));

    const out = await runOneshotLLM({
      requestId: "req-1",
      user: "ping",
      llmSettings: validSettings,
    });
    expect(out).toBe("hello world");
    expect(completeMock).toHaveBeenCalledTimes(1);
  });

  it("propagates temperature and maxTokens to pi-ai complete()", async () => {
    completeMock.mockResolvedValue(assistantText("ok"));

    await runOneshotLLM({
      requestId: "req-2",
      user: "ping",
      maxTokens: 64,
      temperature: 0.42,
      llmSettings: validSettings,
    });

    const opts = completeMock.mock.calls[0][2];
    expect(opts.temperature).toBe(0.42);
    expect(opts.maxTokens).toBe(64);
    expect(opts.apiKey).toBe("sk-test");
    // Default 30s timeout when unset
    expect(opts.timeoutMs).toBe(30_000);
  });

  it("forwards systemPrompt when provided", async () => {
    completeMock.mockResolvedValue(assistantText("ok"));

    await runOneshotLLM({
      requestId: "req-3",
      system: "You are concise.",
      user: "summarize",
      llmSettings: validSettings,
    });

    const ctx = completeMock.mock.calls[0][1];
    expect(ctx.systemPrompt).toBe("You are concise.");
    expect(ctx.messages).toEqual([
      expect.objectContaining({ role: "user", content: "summarize" }),
    ]);
  });

  it("omits systemPrompt when not provided", async () => {
    completeMock.mockResolvedValue(assistantText("ok"));

    await runOneshotLLM({
      requestId: "req-3b",
      user: "summarize",
      llmSettings: validSettings,
    });

    const ctx = completeMock.mock.calls[0][1];
    expect(ctx.systemPrompt).toBeUndefined();
  });

  it("forwards AbortSignal to pi-ai complete()", async () => {
    completeMock.mockResolvedValue(assistantText("ok"));
    const ctrl = new AbortController();

    await runOneshotLLM({
      requestId: "req-4",
      user: "ping",
      llmSettings: validSettings,
      signal: ctrl.signal,
    });

    const opts = completeMock.mock.calls[0][2];
    expect(opts.signal).toBe(ctrl.signal);
  });

  it("applies proxy config before calling complete()", async () => {
    completeMock.mockResolvedValue(assistantText("ok"));
    const proxy: ProxyConfig = {
      url: "http://proxy:8080",
      no_proxy: "localhost",
      llm_enabled: true,
      slack_enabled: false,
      zabbix_enabled: false,
      victoria_metrics_enabled: false,
    };

    await runOneshotLLM({
      requestId: "req-5",
      user: "ping",
      llmSettings: validSettings,
      proxyConfig: proxy,
    });

    expect(applyProxyConfigMock).toHaveBeenCalledWith(proxy);
  });

  it("throws when assistant message stopReason is error", async () => {
    completeMock.mockResolvedValue({
      ...assistantText(""),
      stopReason: "error" as const,
      errorMessage: "rate limited",
    });

    await expect(
      runOneshotLLM({
        requestId: "req-6",
        user: "ping",
        llmSettings: validSettings,
      }),
    ).rejects.toThrow(/rate limited/);
  });

  it("retries once without temperature when the provider rejects it, then remembers the model", async () => {
    // Anthropic claude-sonnet-5 rejects the parameter outright; pi-ai 0.80.6's
    // catalog lacks supportsTemperature:false for it, so the 400 reaches us.
    // NOTE: the rejection cache is module-level, so this test uses a dedicated
    // model name to avoid leaking state into sibling tests.
    const sonnetSettings: LLMSettings = {
      ...validSettings,
      provider: "anthropic",
      model: "claude-sonnet-5",
    };
    completeMock
      .mockResolvedValueOnce({
        ...assistantText(""),
        stopReason: "error" as const,
        errorMessage: '400 {"type":"error","error":{"type":"invalid_request_error","message":"`temperature` is deprecated for this model."}}',
      })
      .mockResolvedValueOnce(assistantText("Nginx local request connection timeout on sg1-hw-edge1"));

    const out = await runOneshotLLM({
      requestId: "req-temp-retry",
      user: "ping",
      temperature: 0.3,
      llmSettings: sonnetSettings,
    });

    expect(out).toBe("Nginx local request connection timeout on sg1-hw-edge1");
    expect(completeMock).toHaveBeenCalledTimes(2);
    expect(completeMock.mock.calls[0][2].temperature).toBe(0.3);
    expect(completeMock.mock.calls[1][2].temperature).toBeUndefined();
    // Everything else is preserved on the retry.
    expect(completeMock.mock.calls[1][2].apiKey).toBe("sk-test");

    // Second call for the same model: skip temperature up front — one
    // request, no doomed probe.
    completeMock.mockClear();
    completeMock.mockResolvedValue(assistantText("second title"));
    const out2 = await runOneshotLLM({
      requestId: "req-temp-cached",
      user: "ping again",
      temperature: 0.3,
      llmSettings: sonnetSettings,
    });
    expect(out2).toBe("second title");
    expect(completeMock).toHaveBeenCalledTimes(1);
    expect(completeMock.mock.calls[0][2].temperature).toBeUndefined();
  });

  it("does not retry temperature-rejection when no temperature was sent", async () => {
    completeMock.mockResolvedValue({
      ...assistantText(""),
      stopReason: "error" as const,
      errorMessage: "`temperature` is deprecated for this model.",
    });

    await expect(
      runOneshotLLM({
        requestId: "req-temp-no-retry",
        user: "ping",
        llmSettings: validSettings,
      }),
    ).rejects.toThrow(/temperature/);
    expect(completeMock).toHaveBeenCalledTimes(1);
  });

  it("does not retry on unrelated provider errors", async () => {
    completeMock.mockResolvedValue({
      ...assistantText(""),
      stopReason: "error" as const,
      errorMessage: "overloaded",
    });

    await expect(
      runOneshotLLM({
        requestId: "req-no-retry",
        user: "ping",
        temperature: 0.3,
        llmSettings: validSettings,
      }),
    ).rejects.toThrow(/overloaded/);
    expect(completeMock).toHaveBeenCalledTimes(1);
  });

  it("throws when assistant message stopReason is aborted", async () => {
    completeMock.mockResolvedValue({
      ...assistantText(""),
      stopReason: "aborted" as const,
    });

    await expect(
      runOneshotLLM({
        requestId: "req-7",
        user: "ping",
        llmSettings: validSettings,
      }),
    ).rejects.toThrow(/aborted/);
  });

  it("rejects when request_id missing", async () => {
    await expect(
      runOneshotLLM({
        requestId: "",
        user: "ping",
        llmSettings: validSettings,
      }),
    ).rejects.toThrow(/request_id/);
    expect(completeMock).not.toHaveBeenCalled();
  });

  it("rejects when user prompt missing", async () => {
    await expect(
      runOneshotLLM({
        requestId: "req-8",
        user: "",
        llmSettings: validSettings,
      }),
    ).rejects.toThrow(/user prompt/);
    expect(completeMock).not.toHaveBeenCalled();
  });

  it("rejects when LLM settings missing api_key", async () => {
    await expect(
      runOneshotLLM({
        requestId: "req-9",
        user: "ping",
        llmSettings: { ...validSettings, api_key: "" },
      }),
    ).rejects.toThrow(/API key/);
    expect(completeMock).not.toHaveBeenCalled();
  });

  it("trims whitespace and concatenates multiple text blocks", async () => {
    completeMock.mockResolvedValue({
      ...assistantText(""),
      content: [
        { type: "text" as const, text: "  hello " },
        { type: "thinking" as const, thinking: "internal reasoning" },
        { type: "text" as const, text: "world  " },
      ],
    });

    const out = await runOneshotLLM({
      requestId: "req-10",
      user: "ping",
      llmSettings: validSettings,
    });
    expect(out).toBe("hello world");
  });
});
