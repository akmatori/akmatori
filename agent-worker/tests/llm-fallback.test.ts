import { describe, it, expect } from "vitest";
import {
  PrimaryCircuitBreaker,
  describeLLM,
  isFallbackEligibleError,
  shortError,
} from "../src/llm-fallback.js";

describe("isFallbackEligibleError", () => {
  it.each([
    "500 status code (no body)",
    "502 Bad Gateway",
    "503 Service Unavailable",
    "Connection error.",
    "connect ECONNREFUSED 10.0.0.1:443",
    "fetch failed",
    "terminated",
    "Request timed out.",
    "429 Too Many Requests",
  ])("accepts %s", (err) => {
    expect(isFallbackEligibleError(err)).toBe(true);
  });

  it.each([
    "",
    undefined,
    "401 Incorrect API key provided",
    "403 Forbidden",
    "400 This model's maximum context length is 128000 tokens",
    "400 prompt is too long: 210000 tokens > 200000 maximum",
    "insufficient_quota: You exceeded your current quota",
    "404 model not found",
    "Execution cancelled",
  ])("rejects %s", (err) => {
    expect(isFallbackEligibleError(err)).toBe(false);
  });
});

describe("PrimaryCircuitBreaker", () => {
  it("opens after the threshold, probes after the window, closes on success", () => {
    let now = 1_000;
    const breaker = new PrimaryCircuitBreaker(3, 60_000, () => now);
    breaker.recordFailure("k");
    breaker.recordFailure("k");
    expect(breaker.isOpen("k")).toBe(false);
    breaker.recordFailure("k");
    expect(breaker.isOpen("k")).toBe(true);
    expect(breaker.isOpen("other")).toBe(false);

    now += 60_001;
    expect(breaker.isOpen("k")).toBe(false); // probe allowed
    breaker.recordFailure("k"); // failed probe re-opens at once
    expect(breaker.isOpen("k")).toBe(true);

    now += 60_001;
    breaker.recordSuccess("k");
    expect(breaker.isOpen("k")).toBe(false);
    expect(breaker.failures("k")).toBe(0);
  });
});

describe("describeLLM / shortError", () => {
  it("prefers the operator's config name", () => {
    expect(describeLLM({ provider: "openai", model: "gpt-5.5", api_key: "k", thinking_level: "medium" })).toBe(
      "openai/gpt-5.5",
    );
    expect(
      describeLLM({ provider: "openai", model: "gpt-5.5", api_key: "k", thinking_level: "medium", name: "Main" }),
    ).toBe("Main (openai/gpt-5.5)");
  });

  it("keeps the error on one short line", () => {
    expect(shortError("a\n  b")).toBe("a b");
    expect(shortError("x".repeat(500)).length).toBe(200);
  });
});
