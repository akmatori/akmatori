/**
 * One-shot LLM call helper.
 *
 * Wraps pi-ai's `complete()` for single-turn, provider-agnostic LLM
 * invocations. Used by the API server (via the worker WebSocket) for
 * tasks like incident-title generation, alert extraction, and Slack
 * summarization, so those callsites do not need their own per-provider
 * HTTP clients.
 */

// pi-ai 0.80.0 made the root entrypoint core-only: `complete` now lives on the
// temporary /compat entrypoint (types stay at root). Follow-up: when upstream
// removes /compat (after coding-agent's own ModelManager migration), port this
// to per-api `streamSimple` dispatch or a Models collection with a registered
// provider factory.
import { complete } from "@earendil-works/pi-ai/compat";
import type { AssistantMessage, Context, Message } from "@earendil-works/pi-ai";
import type { LLMSettings, ProxyConfig } from "./types.js";
import { applyProxyConfig } from "./proxy.js";
import { resolveModel } from "./agent-runner.js";
import { createSamplingPayloadHook } from "./sampling.js";

const DEFAULT_TIMEOUT_MS = 30_000;

/**
 * Models observed rejecting the temperature parameter (keyed
 * "provider/model"). Once a model lands here, subsequent calls skip
 * temperature up front instead of paying a failed request + retry every
 * time. Process-lifetime only — a worker restart re-probes, which is what we
 * want when providers or pi-ai catalogs change.
 */
const temperatureRejectedModels = new Set<string>();

export interface OneshotLLMParams {
  requestId: string;
  system?: string;
  user: string;
  maxTokens?: number;
  temperature?: number;
  /** Operator-configured nucleus sampling; omitted unless explicitly set. */
  topP?: number;
  /** Operator-configured top-k sampling; omitted unless explicitly set. */
  topK?: number;
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

  // temperature and maxTokens ride pi-ai's native StreamOptions so the
  // temperature-rejection retry below still has a parameter to strip. top_p and
  // top_k have no StreamOptions equivalent at all, so they go in through the
  // payload hook, which is a no-op when neither is configured.
  const samplingHook = createSamplingPayloadHook(
    { top_p: params.topP, top_k: params.topK },
    (message) => console.warn(`oneshot_llm ${params.requestId}: ${message}`),
  );

  const baseOptions = {
    apiKey: params.llmSettings.api_key,
    maxTokens: params.maxTokens,
    timeoutMs: params.timeoutMs ?? DEFAULT_TIMEOUT_MS,
    signal: params.signal,
    ...(samplingHook ? { onPayload: samplingHook } : {}),
  };

  const modelKey = `${params.llmSettings.provider}/${params.llmSettings.model}`;
  const sendTemperature =
    params.temperature !== undefined && !temperatureRejectedModels.has(modelKey);

  let result: AssistantMessage = await complete(
    model,
    context,
    sendTemperature ? { ...baseOptions, temperature: params.temperature } : baseOptions,
  );

  // Newer models reject the temperature parameter outright (e.g. Anthropic
  // claude-sonnet-5: 400 "`temperature` is deprecated for this model"), and
  // provider catalogs don't always carry that metadata (pi-ai 0.80.6 flags
  // claude-opus-4-7/4-8 with supportsTemperature:false but not sonnet-5).
  // Rather than tracking per-model support here, retry once without
  // temperature when the provider names it as the problem, and remember the
  // model so later calls skip the doomed first request. Callers only use
  // temperature to bias determinism, so dropping it beats failing the call
  // and falling back to deterministic non-LLM output (e.g. truncated
  // "Triggered: ..." incident titles).
  if (
    result.stopReason === "error" &&
    sendTemperature &&
    /temperature/i.test(result.errorMessage ?? "")
  ) {
    console.warn(
      `oneshot_llm ${params.requestId}: provider rejected temperature (${result.errorMessage}); retrying without it`,
    );
    temperatureRejectedModels.add(modelKey);
    result = await complete(model, context, baseOptions);
  }

  if (result.stopReason === "error") {
    throw new Error(result.errorMessage ?? "oneshot_llm: provider returned error");
  }
  if (result.stopReason === "aborted") {
    throw new Error("oneshot_llm: request aborted");
  }
  // "pending" (pi 0.83.0, partial streaming message) and "deferred" (pi 0.84.0,
  // response retrievable later through a durable handle) both mean the text is
  // not final. `complete()` should never surface either, but treating one as a
  // finished answer would silently corrupt an incident title or an extraction
  // result — fail loudly so the caller's deterministic fallback takes over.
  if (result.stopReason === "pending" || result.stopReason === "deferred") {
    throw new Error(
      `oneshot_llm: provider returned a non-final response (${result.stopReason})`,
    );
  }

  const text = result.content
    .filter((block): block is { type: "text"; text: string } => block.type === "text")
    .map((block) => block.text)
    .join("")
    .trim();

  return text;
}
