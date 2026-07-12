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
