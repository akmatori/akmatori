import { describe, expect, it, vi } from "vitest";
import { requireSubscriptionLogin, usesSubscription, validateSubscriptionSettings } from "../src/subscription-auth.js";
import { resolveModel } from "../src/agent-runner.js";
import type { LLMSettings } from "../src/types.js";

const settings: LLMSettings = {
  provider: "openai-codex", api_key: "", model: "gpt-5.5", thinking_level: "medium",
};

describe("subscription authentication", () => {
  it("accepts subscription configuration and resolves the real SDK model", () => {
    expect(() => validateSubscriptionSettings(settings)).not.toThrow();
    expect(resolveModel(settings.provider, settings.model)).toMatchObject({
      provider: "openai-codex", api: "openai-codex-responses",
    });
    expect(usesSubscription("openai")).toBe(false);
    expect(usesSubscription("anthropic")).toBe(false);
  });

  it("rejects keys, endpoints, and missing or unknown models", () => {
    for (const override of [{ api_key: "test" }, { base_url: "https://example.com" }, { model: " " }]) {
      expect(() => validateSubscriptionSettings({ ...settings, ...override })).toThrow();
    }
    expect(() => resolveModel(settings.provider, "unknown-model")).toThrow("Unknown subscription model");
    expect(() => resolveModel(settings.provider, settings.model, "https://example.com")).toThrow("custom endpoints");
  });

  it("resolves stored OAuth with cancellation passed to refresh", async () => {
    const signal = new AbortController().signal;
    const runtime = {
      listCredentials: vi.fn().mockResolvedValue([{ providerId: "openai-codex", type: "oauth" }]),
      getAuth: vi.fn().mockResolvedValue({ auth: { apiKey: "test-access-token" } }),
    };
    await requireSubscriptionLogin(runtime, "openai-codex", signal);
    expect(runtime.getAuth).toHaveBeenCalledWith("openai-codex", { signal });
  });

  it("fails closed for missing login or stored API keys without resolving ambient auth", async () => {
    for (const credentials of [[], [{ providerId: "openai-codex", type: "api_key" }]]) {
      const runtime = { listCredentials: vi.fn().mockResolvedValue(credentials), getAuth: vi.fn() };
      await expect(requireSubscriptionLogin(runtime, "openai-codex")).rejects.toThrow("sign-in required");
      expect(runtime.getAuth).not.toHaveBeenCalled();
    }
  });

  it("does not disclose refresh error bodies", async () => {
    const runtime = {
      listCredentials: vi.fn().mockResolvedValue([{ providerId: "openai-codex", type: "oauth" }]),
      getAuth: vi.fn().mockRejectedValue(new Error("sensitive-provider-response")),
    };
    await expect(requireSubscriptionLogin(runtime, "openai-codex")).rejects.toThrow("Sign in again");
    await expect(requireSubscriptionLogin(runtime, "openai-codex")).rejects.not.toThrow("sensitive-provider-response");
  });
});
