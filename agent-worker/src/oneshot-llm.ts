/**
 * One-shot LLM call helper.
 *
 * Wraps pi-ai's `complete()` for single-turn, provider-agnostic LLM
 * invocations. Used by the API server (via the worker WebSocket) for
 * tasks like incident-title generation, alert extraction, and Slack
 * summarization, so those callsites do not need their own per-provider
 * HTTP clients.
 */

import { complete, type AssistantMessage, type Context, type Message } from "@mariozechner/pi-ai";
import type { LLMSettings, ProxyConfig } from "./types.js";
import { applyProxyConfig } from "./proxy.js";
import { resolveModel } from "./agent-runner.js";

const DEFAULT_TIMEOUT_MS = 30_000;

export interface OneshotLLMParams {
  requestId: string;
  system?: string;
  user: string;
  maxTokens?: number;
  temperature?: number;
  llmSettings: LLMSettings;
  proxyConfig?: ProxyConfig;
  signal?: AbortSignal;
  /** Override the per-call HTTP timeout (ms). Defaults to 30s. */
  timeoutMs?: number;
}

/**
 * Run a single-turn LLM completion and return the assistant text.
 * Throws on validation errors, provider errors, or non-text responses.
 */
export async function runOneshotLLM(params: OneshotLLMParams): Promise<string> {
  if (!params.requestId) {
    throw new Error("oneshot_llm: missing request_id");
  }
  if (!params.user) {
    throw new Error("oneshot_llm: missing user prompt");
  }
  if (!params.llmSettings || !params.llmSettings.api_key) {
    throw new Error("oneshot_llm: missing LLM settings (no API key)");
  }

  applyProxyConfig(params.proxyConfig);

  const model = resolveModel(
    params.llmSettings.provider,
    params.llmSettings.model,
    params.llmSettings.base_url,
  );

  const messages: Message[] = [
    {
      role: "user",
      content: params.user,
      timestamp: Date.now(),
    },
  ];

  const context: Context = {
    ...(params.system ? { systemPrompt: params.system } : {}),
    messages,
  };

  const result: AssistantMessage = await complete(model, context, {
    apiKey: params.llmSettings.api_key,
    temperature: params.temperature,
    maxTokens: params.maxTokens,
    timeoutMs: params.timeoutMs ?? DEFAULT_TIMEOUT_MS,
    signal: params.signal,
  });

  if (result.stopReason === "error") {
    throw new Error(result.errorMessage ?? "oneshot_llm: provider returned error");
  }
  if (result.stopReason === "aborted") {
    throw new Error("oneshot_llm: request aborted");
  }

  const text = result.content
    .filter((block): block is { type: "text"; text: string } => block.type === "text")
    .map((block) => block.text)
    .join("")
    .trim();

  return text;
}
