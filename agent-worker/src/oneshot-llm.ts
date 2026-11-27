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
import { CODEX_PROVIDER, createCodexRuntime, resolveCodexAccessToken } from "./codex-auth.js";
import { createSamplingPayloadHook } from "./sampling.js";
import {
  isFallbackEligibleError,
  llmKey,
  primaryBreaker,
  shortError,
  switchNotice,
  usedInfo,
  type LLMUsedInfo,
} from "./llm-fallback.js";

// Above every Go caller's own deadline (45 s at most), so the API side decides
// when a call has taken too long and the worker never cuts it off first.
const DEFAULT_TIMEOUT_MS = 60_000;

/**
 * Retries after the connection to the provider broke mid-request. The gcore
 * DeepSeek gateway sometimes closes the stream ("terminated" from undici);
 * the next request usually works, while a failure sends the Go caller to its
 * fallback (no merge, no extraction). Provider errors with a real answer
 * (4xx, "overloaded") are not retried.
 */
const TRANSIENT_RETRIES = 2;
const TRANSIENT_RETRY_DELAY_MS = 500;
const TRANSIENT_ERROR_RE = /\bterminated\b|ECONNRESET|socket hang up|other side closed|fetch failed|UND_ERR_SOCKET/i;

/**
 * A retry starts only this soon after the first attempt. The Go callers wait
 * at most 45 s, so a later retry would answer a request nobody waits for.
 */
const TRANSIENT_RETRY_WINDOW_MS = 30_000;

/**
 * Output tokens added on top of the caller's maxTokens for reasoning models.
 * Callers size maxTokens for the ANSWER (a 50-token title, a 250-token JSON
 * verdict), but providers count reasoning tokens against the same limit. A
 * reasoning model then spends the whole budget thinking and returns no text:
 * DeepSeek-V4.1-Flash used all 250 tokens of a correlation call on reasoning
 * (finish=length, content=""), which every Go caller read as "no answer" and
 * quietly replaced with its fallback. Non-reasoning models stop when the
 * answer is done, so the headroom costs nothing there.
 */
export const REASONING_HEADROOM_TOKENS = 4096;

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
  /** Override the per-call HTTP timeout (ms). Defaults to 60s. */
  timeoutMs?: number;
  /**
   * Cap on the WHOLE call, retries and their delays included (ms). Unset =
   * no cap beyond each request's timeoutMs (the pre-fallback behaviour).
   */
  totalTimeoutMs?: number;
}

/** A retry is not started with less time left than this. */
const MIN_REQUEST_MS = 1_000;

/**
 * Run a single-turn LLM completion and return the assistant text.
 * Throws on validation errors, provider errors, or non-text responses.
 */
export async function runOneshotLLM(params: OneshotLLMParams): Promise<string> {
  if (params.totalTimeoutMs === undefined) {
    return runOneshotBody(params);
  }
  // A per-request timeoutMs does not bound a stream that stalls after the
  // headers (the OpenAI SDK clears its timer then; the Google adapter has
  // none), so the whole call is also bounded by an abort signal.
  const deadline = new AbortController();
  const timer = setTimeout(() => deadline.abort(), params.totalTimeoutMs);
  const signal = params.signal ? AbortSignal.any([params.signal, deadline.signal]) : deadline.signal;
  try {
    return await runOneshotBody({ ...params, signal });
  } catch (err) {
    if (deadline.signal.aborted && !params.signal?.aborted) {
      throw new Error(BUDGET_TIMEOUT_MESSAGE);
    }
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

/** Fallback-eligible ("timed out") error for a call that used its whole budget. */
const BUDGET_TIMEOUT_MESSAGE = "oneshot_llm: request timed out (time budget used up)";

async function runOneshotBody(params: OneshotLLMParams): Promise<string> {
  if (!params.requestId) {
    throw new Error("oneshot_llm: missing request_id");
  }
  if (!params.user) {
    throw new Error("oneshot_llm: missing user prompt");
  }
  const isCodex = params.llmSettings?.provider === CODEX_PROVIDER;
  if (!params.llmSettings || (!isCodex && !params.llmSettings.api_key)) {
    throw new Error("oneshot_llm: missing LLM settings (no API key)");
  }

  applyProxyConfig(params.proxyConfig);

  const model = resolveModel(
    params.llmSettings.provider,
    params.llmSettings.model,
    params.llmSettings.base_url,
  );
  const apiKey = isCodex
    ? await resolveCodexAccessToken(await createCodexRuntime(), model, params.signal)
    : params.llmSettings.api_key;

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
  const samplingHook = isCodex ? undefined : createSamplingPayloadHook(
    { top_p: params.topP, top_k: params.topK },
    (message) => console.warn(`oneshot_llm ${params.requestId}: ${message}`),
  );

  const maxTokens = isCodex ? undefined : params.maxTokens;
  const baseOptions = {
    apiKey,
    maxTokens: maxTokens !== undefined && model.reasoning
      ? maxTokens + REASONING_HEADROOM_TOKENS
      : maxTokens,
    timeoutMs: params.timeoutMs ?? DEFAULT_TIMEOUT_MS,
    signal: params.signal,
    ...(isCodex ? { transport: "sse" as const } : {}),
    ...(samplingHook ? { onPayload: samplingHook } : {}),
  };

  const modelKey = `${params.llmSettings.provider}/${params.llmSettings.model}`;
  const sendTemperature =
    !isCodex && params.temperature !== undefined && !temperatureRejectedModels.has(modelKey);

  const startedAt = Date.now();
  // totalTimeoutMs caps the whole call: every request and retry sleep. Each
  // request's HTTP timeout shrinks to the time left, so retries cannot run
  // past the budget a fallback attempt depends on.
  const deadline = params.totalTimeoutMs !== undefined ? startedAt + params.totalTimeoutMs : undefined;
  const timeLeft = () => (deadline === undefined ? Infinity : deadline - Date.now());
  const within = <T extends { timeoutMs: number }>(opts: T): T => {
    const left = timeLeft();
    if (left <= 0) {
      throw new Error(BUDGET_TIMEOUT_MESSAGE);
    }
    return left < opts.timeoutMs ? { ...opts, timeoutMs: Math.floor(left) } : opts;
  };

  let result: AssistantMessage = await complete(
    model,
    context,
    within(sendTemperature ? { ...baseOptions, temperature: params.temperature } : baseOptions),
  );
  for (
    let retry = 1;
    retry <= TRANSIENT_RETRIES &&
    result.stopReason === "error" &&
    TRANSIENT_ERROR_RE.test(result.errorMessage ?? "") &&
    Date.now() - startedAt < TRANSIENT_RETRY_WINDOW_MS &&
    timeLeft() > TRANSIENT_RETRY_DELAY_MS * retry + MIN_REQUEST_MS &&
    !params.signal?.aborted;
    retry++
  ) {
    console.warn(
      `oneshot_llm ${params.requestId}: connection failed (${result.errorMessage}); retry ${retry}/${TRANSIENT_RETRIES}`,
    );
    await new Promise((resolve) => setTimeout(resolve, TRANSIENT_RETRY_DELAY_MS * retry));
    result = await complete(
      model,
      context,
      within(sendTemperature ? { ...baseOptions, temperature: params.temperature } : baseOptions),
    );
  }

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
    result = await complete(model, context, within(baseOptions));
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

  // Hitting the limit before any answer text is a failure, not an empty
  // answer: return "" and the caller cannot tell it from a model that chose
  // to say nothing, so the failure never shows up in its error logs.
  if (text === "" && result.stopReason === "length") {
    throw new Error(
      "oneshot_llm: output token limit reached before any answer text (reasoning used the budget)",
    );
  }

  return text;
}

export interface OneshotFallbackParams extends OneshotLLMParams {
  /**
   * Fallback config from the frame's `fallback_llm`. Its sampling fields are
   * already merged with the call site's values on the Go side, so the
   * fallback call uses them as they are.
   */
  fallbackLlmSettings?: LLMSettings;
  /**
   * How long the API waits for the answer (frame `timeout_ms`). With a
   * fallback, the primary gets PRIMARY_BUDGET_SHARE of it and the fallback
   * the rest, so a hanging primary cannot use the whole wait.
   */
  budgetMs?: number;
}

/** Share of the caller's wait the primary may use when a fallback is set. */
export const PRIMARY_BUDGET_SHARE = 0.6;
/** Below this, a fallback attempt cannot finish before the caller gives up. */
const MIN_FALLBACK_MS = 3_000;

export interface OneshotResult {
  text: string;
  used: LLMUsedInfo;
}

/**
 * runOneshotLLM on the primary config; on a transient provider error, the
 * same call on the fallback config. Shares the circuit breaker with agent
 * runs, so a primary that is down is skipped here too. Without a fallback
 * config this is runOneshotLLM.
 */
export async function runOneshotWithFallback(params: OneshotFallbackParams): Promise<OneshotResult> {
  const { fallbackLlmSettings: fallback, budgetMs, ...baseParams } = params;
  const primary = params.llmSettings;
  if (!fallback) {
    return { text: await runOneshotLLM(baseParams), used: usedInfo(primary) };
  }
  const startedAt = Date.now();
  const budget = budgetMs !== undefined && budgetMs > 0 ? Math.min(budgetMs, DEFAULT_TIMEOUT_MS) : DEFAULT_TIMEOUT_MS;
  const primaryParams: OneshotLLMParams = {
    ...baseParams,
    timeoutMs: Math.min(baseParams.timeoutMs ?? DEFAULT_TIMEOUT_MS, Math.floor(budget * PRIMARY_BUDGET_SHARE)),
    totalTimeoutMs: Math.floor(budget * PRIMARY_BUDGET_SHARE),
  };
  // Skipped primary: the fallback may use the whole wait.
  const fallbackParams: OneshotLLMParams = {
    ...baseParams,
    timeoutMs: budget,
    totalTimeoutMs: budget,
    llmSettings: fallback,
    maxTokens: fallback.max_tokens,
    temperature: fallback.temperature,
    topP: fallback.top_p,
    topK: fallback.top_k,
  };
  const key = llmKey(primary);
  if (primaryBreaker.shouldSkipPrimary(key)) {
    return {
      text: await runOneshotLLM(fallbackParams),
      used: usedInfo(fallback, primary, "primary skipped: circuit breaker open"),
    };
  }
  try {
    const text = await runOneshotLLM(primaryParams);
    primaryBreaker.recordSuccess(key);
    return { text, used: usedInfo(primary) };
  } catch (err) {
    const errMsg = (err as Error)?.message ?? String(err);
    if (!isFallbackEligibleError(errMsg) || params.signal?.aborted) {
      primaryBreaker.releaseProbe(key);
      throw err;
    }
    primaryBreaker.recordFailure(key);
    const reason = shortError(errMsg);
    const left = budget - (Date.now() - startedAt);
    if (left < MIN_FALLBACK_MS) {
      // The caller stops waiting before a fallback answer could arrive.
      throw err;
    }
    console.warn(`oneshot_llm ${params.requestId}: ${switchNotice(primary, fallback, reason)}`);
    return {
      text: await runOneshotLLM({ ...fallbackParams, timeoutMs: left, totalTimeoutMs: left }),
      used: usedInfo(fallback, primary, reason),
    };
  }
}
