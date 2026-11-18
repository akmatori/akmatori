/**
 * Fallback LLM support.
 *
 * The API can send a second LLM config (`fallback_llm`) next to the primary
 * one. When the primary fails with a transient provider error after its own
 * retries (5xx, connection refused, timeout, a dropped stream), the worker runs
 * the request again on the fallback and reports which config answered.
 *
 * A small circuit breaker keeps every request from first spending its retries
 * on a primary that is known to be down: after FAILURE_THRESHOLD primary
 * failures in a row the primary is skipped for OPEN_MS, then one request
 * probes it again.
 */

import { isRetryableAssistantError } from "@earendil-works/pi-ai";
import type { AssistantMessage } from "@earendil-works/pi-ai";
import type { LLMSettings } from "./types.js";

/** Primary failures in a row before the breaker opens. */
export const FAILURE_THRESHOLD = 3;
/** How long an open breaker skips the primary. */
export const OPEN_MS = 5 * 60_000;

/**
 * Errors that need an operator fix, not a second model: a context that does
 * not fit, a bad request. pi's classifier does not retry most of these
 * either; this list makes the rule explicit for the fallback decision.
 */
const NOT_FALLBACK_RE =
  /context.?(length|window)|maximum context|too many tokens|prompt is too long|input is too long|reduce the length|invalid.?api.?key|unauthori[sz]ed|\b401\b|\b403\b/i;

/**
 * Node socket error codes that pi-ai's classifier does not name (it matches
 * the words "connection refused", not the code): an endpoint that is down
 * for an upgrade answers with exactly these.
 */
const NODE_SOCKET_ERROR_RE = /\b(ECONNREFUSED|ECONNRESET|EHOSTUNREACH|ENETUNREACH|ETIMEDOUT|EPIPE|UND_ERR_[A-Z_]+)\b/;

/**
 * True when the error looks like the primary endpoint being down or
 * overloaded, so the same request can succeed on another model. Uses pi-ai's
 * own transient-error classifier (the rule pi uses for its retries), so the
 * fallback starts exactly where pi's retries give up.
 */
export function isFallbackEligibleError(errorMessage: string | undefined): boolean {
  if (!errorMessage) return false;
  if (NOT_FALLBACK_RE.test(errorMessage)) return false;
  if (NODE_SOCKET_ERROR_RE.test(errorMessage)) return true;
  return isRetryableAssistantError({
    stopReason: "error",
    errorMessage,
  } as AssistantMessage);
}

/** Identity of an LLM config for the breaker: same endpoint + model = same key. */
export function llmKey(settings: LLMSettings): string {
  return `${settings.provider}|${settings.model}|${settings.base_url ?? ""}`;
}

/** Human-readable name for log lines: the operator's config name, else provider/model. */
export function describeLLM(settings: LLMSettings): string {
  const id = `${settings.provider}/${settings.model}`;
  return settings.name ? `${settings.name} (${id})` : id;
}

/** Short one-line form of a provider error for the run log. */
export function shortError(errorMessage: string, max = 200): string {
  const line = errorMessage.replace(/\s+/g, " ").trim();
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

interface BreakerState {
  failures: number;
  openUntil: number;
  /** When the half-open probe started; 0 = no probe in flight. */
  probeSince: number;
}

/**
 * Per-primary circuit breaker. Process-lifetime state: a worker restart
 * starts closed, which is the safe default (one request probes the primary).
 *
 * closed → (threshold failures in a row) → open for openMs → half-open: ONE
 * caller gets the primary as a probe, all others keep using the fallback
 * until the probe reports. Probe success closes the breaker; probe failure
 * re-opens it. A probe that never reports (lost frame, crash) is replaced
 * after another openMs.
 */
export class PrimaryCircuitBreaker {
  private readonly states = new Map<string, BreakerState>();

  constructor(
    private readonly threshold = FAILURE_THRESHOLD,
    private readonly openMs = OPEN_MS,
    private readonly now: () => number = Date.now,
  ) {}

  /**
   * Decide whether this request skips the primary. Returns false for the one
   * caller admitted as the half-open probe (and reserves the probe), so call
   * it once per request and report the result with recordSuccess,
   * recordFailure or releaseProbe.
   */
  shouldSkipPrimary(key: string): boolean {
    const state = this.states.get(key);
    if (!state || state.failures < this.threshold) return false;
    const now = this.now();
    if (state.openUntil > now) return true;
    if (state.probeSince > 0 && now - state.probeSince < this.openMs) return true;
    state.probeSince = now;
    return false;
  }

  /** Consecutive primary failures recorded for key. */
  failures(key: string): number {
    return this.states.get(key)?.failures ?? 0;
  }

  /**
   * Record a primary failure. At the threshold the breaker opens; a failed
   * probe re-opens it at once (failures stay above the threshold until a
   * success resets them).
   */
  recordFailure(key: string): void {
    const state = this.states.get(key) ?? { failures: 0, openUntil: 0, probeSince: 0 };
    state.failures += 1;
    state.probeSince = 0;
    if (state.failures >= this.threshold) {
      state.openUntil = this.now() + this.openMs;
    }
    this.states.set(key, state);
  }

  /** Record a primary success: the breaker closes and the count resets. */
  recordSuccess(key: string): void {
    this.states.delete(key);
  }

  /**
   * The primary answered with an error that says nothing about an outage
   * (bad key, bad request): keep the counts, free the probe slot.
   */
  releaseProbe(key: string): void {
    const state = this.states.get(key);
    if (state) state.probeSince = 0;
  }

  /** Test helper. */
  reset(): void {
    this.states.clear();
  }
}

/** Shared by agent runs and one-shot calls: both talk to the same endpoints. */
export const primaryBreaker = new PrimaryCircuitBreaker();

/** Which config answered, sent back to the API on the result frame. */
export interface LLMUsedInfo {
  llm_provider: string;
  llm_model: string;
  llm_thinking_level?: string;
  /** Set only when the fallback answered: "<provider>/<model>" of the primary. */
  llm_fallback_from?: string;
  /** Why the primary was not used (its error, or the open breaker). */
  llm_fallback_reason?: string;
}

export function usedInfo(used: LLMSettings, primary?: LLMSettings, reason?: string): LLMUsedInfo {
  return {
    llm_provider: used.provider,
    llm_model: used.model,
    llm_thinking_level: used.thinking_level,
    ...(primary ? { llm_fallback_from: `${primary.provider}/${primary.model}` } : {}),
    ...(primary && reason ? { llm_fallback_reason: reason } : {}),
  };
}

/** Log line for the switch; the same text goes to the run log and the worker log. */
export function switchNotice(primary: LLMSettings, fallback: LLMSettings, reason: string): string {
  return `Primary LLM ${describeLLM(primary)} failed (${reason}), switched to fallback ${describeLLM(fallback)}`;
}

export function skipNotice(primary: LLMSettings, fallback: LLMSettings, failures: number): string {
  return `Primary LLM ${describeLLM(primary)} skipped (failed ${failures} times in a row; next probe within ${Math.round(OPEN_MS / 60_000)} min), using fallback ${describeLLM(fallback)}`;
}

export function noReplayNotice(primary: LLMSettings, fallback: LLMSettings, reason: string, toolCalls: number): string {
  return `Primary LLM ${describeLLM(primary)} failed (${reason}) after ${toolCalls} tool call(s) that may have changed something; not replaying the run on fallback ${describeLLM(fallback)}. Later runs use the fallback while the primary keeps failing`;
}
