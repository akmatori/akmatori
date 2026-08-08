import { describe, it, expect, vi } from "vitest";
import {
  applySamplingToPayload,
  createSamplingPayloadHook,
  hasSamplingParams,
  pickSamplingParams,
} from "../src/sampling.js";

describe("pickSamplingParams", () => {
  it("returns undefined when nothing is configured", () => {
    expect(pickSamplingParams(undefined)).toBeUndefined();
    expect(pickSamplingParams({})).toBeUndefined();
  });

  it("keeps 0 — a real setting, not an absent one", () => {
    expect(pickSamplingParams({ temperature: 0 })).toEqual({ temperature: 0 });
    expect(hasSamplingParams({ temperature: 0 })).toBe(true);
  });

  it("ignores non-numeric wire values", () => {
    expect(pickSamplingParams({ temperature: "0.5" as unknown as number })).toBeUndefined();
  });
});

describe("applySamplingToPayload", () => {
  it("leaves the payload untouched when nothing is configured", () => {
    const payload = { model: "m", messages: [] };
    const { payload: next, drops } = applySamplingToPayload(payload, undefined, {
      api: "anthropic-messages",
    });
    expect(next).toBeUndefined();
    expect(drops).toEqual([]);
  });

  it("does not mutate the payload it was given", () => {
    const payload = { model: "m", max_tokens: 4096 };
    applySamplingToPayload(payload, { temperature: 0.3 }, { api: "anthropic-messages" });
    expect(payload).toEqual({ model: "m", max_tokens: 4096 });
  });

  describe("anthropic-messages", () => {
    it("maps all four parameters onto the top level", () => {
      const { payload } = applySamplingToPayload(
        { model: "claude-sonnet-5", max_tokens: 4096 },
        { temperature: 0.2, top_p: 0.9, top_k: 40, max_tokens: 8192 },
        { api: "anthropic-messages", provider: "anthropic" },
      );
      expect(payload).toMatchObject({
        temperature: 0.2,
        top_p: 0.9,
        top_k: 40,
        max_tokens: 8192,
      });
    });

    it("drops temperature/top_p/top_k when extended thinking is on, keeping max_tokens", () => {
      const { payload, drops } = applySamplingToPayload(
        { model: "claude-sonnet-5", max_tokens: 4096, thinking: { type: "enabled", budget_tokens: 2000 } },
        { temperature: 0.2, top_p: 0.9, top_k: 40, max_tokens: 8192 },
        { api: "anthropic-messages", provider: "anthropic" },
      );
      expect(payload).toMatchObject({ max_tokens: 8192 });
      expect(payload).not.toHaveProperty("temperature");
      expect(payload).not.toHaveProperty("top_p");
      expect(payload).not.toHaveProperty("top_k");
      expect(drops.map((d) => d.param).sort()).toEqual(["temperature", "top_k", "top_p"]);
    });
  });

  describe("openai-completions", () => {
    it("reuses whichever output-length field the payload already carries", () => {
      const withLegacy = applySamplingToPayload(
        { model: "m", max_tokens: 100 },
        { max_tokens: 900 },
        { api: "openai-completions", provider: "nvidia" },
      );
      expect(withLegacy.payload).toMatchObject({ max_tokens: 900 });
      expect(withLegacy.payload).not.toHaveProperty("max_completion_tokens");

      const withModern = applySamplingToPayload(
        { model: "m", max_completion_tokens: 100 },
        { max_tokens: 900 },
        { api: "openai-completions", provider: "custom" },
      );
      expect(withModern.payload).toMatchObject({ max_completion_tokens: 900 });
      expect(withModern.payload).not.toHaveProperty("max_tokens");
    });

    it("falls back to the compat field when the payload carries neither", () => {
      const antLing = applySamplingToPayload(
        { model: "m" },
        { max_tokens: 900 },
        { api: "openai-completions", provider: "ant-ling", compat: { maxTokensField: "max_tokens" } },
      );
      expect(antLing.payload).toMatchObject({ max_tokens: 900 });

      const openrouter = applySamplingToPayload(
        { model: "m" },
        { max_tokens: 900 },
        { api: "openai-completions", provider: "openrouter" },
      );
      expect(openrouter.payload).toMatchObject({ max_completion_tokens: 900 });
    });

    it("sends top_k to compatible gateways but not to OpenAI itself", () => {
      const nim = applySamplingToPayload(
        { model: "m" },
        { top_k: 40 },
        { api: "openai-completions", provider: "nvidia" },
      );
      expect(nim.payload).toMatchObject({ top_k: 40 });

      const openai = applySamplingToPayload(
        { model: "m" },
        { top_k: 40 },
        { api: "openai-completions", provider: "openai", baseUrl: "https://api.openai.com/v1" },
      );
      expect(openai.payload).toBeUndefined();
      expect(openai.drops).toHaveLength(1);
      expect(openai.drops[0].param).toBe("top_k");
    });
  });

  describe("openai-responses", () => {
    it("uses max_output_tokens and refuses top_k", () => {
      const { payload, drops } = applySamplingToPayload(
        { model: "gpt-5.5", input: [] },
        { temperature: 0.4, top_p: 0.8, top_k: 40, max_tokens: 2048 },
        { api: "openai-responses", provider: "openai" },
      );
      expect(payload).toMatchObject({
        temperature: 0.4,
        top_p: 0.8,
        max_output_tokens: 2048,
      });
      expect(drops.map((d) => d.param)).toEqual(["top_k"]);
    });

    it("skips sampling controls while reasoning is enabled", () => {
      const { payload, drops } = applySamplingToPayload(
        { model: "gpt-5.5", input: [], reasoning: { effort: "medium" } },
        { temperature: 0.4, max_tokens: 2048 },
        { api: "openai-responses", provider: "openai" },
      );
      expect(payload).toMatchObject({ max_output_tokens: 2048 });
      expect(payload).not.toHaveProperty("temperature");
      expect(drops.map((d) => d.param)).toContain("temperature");
    });
  });

  describe("google-generative-ai", () => {
    it("nests camelCase settings under config without clobbering it", () => {
      const { payload } = applySamplingToPayload(
        { model: "gemini-3-pro", contents: [], config: { systemInstruction: "hi", temperature: 1 } },
        { temperature: 0.2, top_p: 0.9, top_k: 40, max_tokens: 8192 },
        { api: "google-generative-ai", provider: "google" },
      );
      expect(payload).toMatchObject({
        config: {
          systemInstruction: "hi",
          temperature: 0.2,
          topP: 0.9,
          topK: 40,
          maxOutputTokens: 8192,
        },
      });
    });
  });

  describe("bedrock-converse-stream", () => {
    it("splits between inferenceConfig and additionalModelRequestFields", () => {
      const { payload } = applySamplingToPayload(
        { modelId: "m", inferenceConfig: { maxTokens: 4096 } },
        { temperature: 0.2, top_p: 0.9, top_k: 40, max_tokens: 8192 },
        { api: "bedrock-converse-stream", provider: "bedrock" },
      );
      expect(payload).toMatchObject({
        inferenceConfig: { temperature: 0.2, topP: 0.9, maxTokens: 8192 },
        additionalModelRequestFields: { top_k: 40 },
      });
    });
  });

  it("reports every parameter as dropped for an unmapped API", () => {
    const { payload, drops } = applySamplingToPayload(
      { anything: true },
      { temperature: 0.2, top_k: 5 },
      { api: "some-future-api" },
    );
    expect(payload).toBeUndefined();
    expect(drops.map((d) => d.param).sort()).toEqual(["temperature", "top_k"]);
  });
});

describe("createSamplingPayloadHook", () => {
  it("returns undefined when nothing is configured, so the option stays off", () => {
    expect(createSamplingPayloadHook(undefined)).toBeUndefined();
    expect(createSamplingPayloadHook({})).toBeUndefined();
  });

  it("injects parameters and reports each drop reason only once", () => {
    const onDrop = vi.fn();
    const hook = createSamplingPayloadHook({ temperature: 0.3, top_k: 40 }, onDrop);
    expect(hook).toBeDefined();

    const model = { api: "openai-responses", provider: "openai" };
    const first = hook!({ model: "gpt-5.5" }, model);
    const second = hook!({ model: "gpt-5.5" }, model);

    expect(first).toMatchObject({ temperature: 0.3 });
    expect(second).toMatchObject({ temperature: 0.3 });
    expect(onDrop).toHaveBeenCalledTimes(1);
    expect(onDrop.mock.calls[0][0]).toContain("top_k");
  });

  it("tolerates a model object it does not recognise", () => {
    const hook = createSamplingPayloadHook({ temperature: 0.3 });
    expect(hook!({ model: "m" }, null)).toBeUndefined();
  });
});
