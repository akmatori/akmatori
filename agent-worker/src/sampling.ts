/**
 * Operator-configured sampling parameters.
 *
 * pi-ai models only `temperature` and `maxTokens` in its `StreamOptions`, and
 * the agent loop (`pi-agent-core` `createLoopConfig`) passes neither — so
 * without this module Akmatori sends no sampling parameters at all and every
 * request runs at the provider's own defaults. `top_p` / `top_k` have no
 * representation in pi-ai whatsoever.
 *
 * The one hook that reaches every provider is `onPayload`, which receives the
 * fully-built provider request body immediately before it goes out. This module
 * owns the per-API mapping from Akmatori's provider-neutral parameter names to
 * the field names and nesting each provider actually expects.
 *
 * Anything left unset stays unset: an operator who configures nothing gets
 * byte-identical requests to the pre-feature behaviour.
 */

/** Sampling overrides as they arrive on the wire from the Go API. */
export interface SamplingParams {
  temperature?: number;
  top_p?: number;
  top_k?: number;
  max_tokens?: number;
}

/**
 * The subset of pi-ai's `Model` this module reads. Kept structural rather than
 * importing `Model<Api>` so the payload hook stays assignable to every
 * per-API option type (their `compat` shapes are unrelated closed unions).
 */
export interface SamplingModel {
  api?: string;
  provider?: string;
  baseUrl?: string;
  compat?: { maxTokensField?: string };
}

/** Narrow pi-ai's `Model<Api>` (or anything else) to the fields we read. */
function toSamplingModel(model: unknown): SamplingModel {
  if (!isRecord(model)) return {};
  const compat = isRecord(model.compat) ? model.compat : undefined;
  const maxTokensField = compat?.maxTokensField;
  return {
    api: typeof model.api === "string" ? model.api : undefined,
    provider: typeof model.provider === "string" ? model.provider : undefined,
    baseUrl: typeof model.baseUrl === "string" ? model.baseUrl : undefined,
    compat: typeof maxTokensField === "string" ? { maxTokensField } : undefined,
  };
}

/** Why a configured parameter did not make it into the request. */
export interface SamplingDrop {
  param: keyof SamplingParams;
  reason: string;
}

export interface ApplySamplingResult {
  /** The modified payload, or undefined when nothing was changed. */
  payload: unknown;
  /** Parameters the operator set that this request could not carry. */
  drops: SamplingDrop[];
}

/** True when at least one override is set. */
export function hasSamplingParams(params: SamplingParams | undefined): boolean {
  if (!params) return false;
  return (
    params.temperature !== undefined ||
    params.top_p !== undefined ||
    params.top_k !== undefined ||
    params.max_tokens !== undefined
  );
}

/** Collect the sampling fields off a wire message / settings object. */
export function pickSamplingParams(
  source: SamplingParams | undefined | null,
): SamplingParams | undefined {
  if (!source) return undefined;
  const params: SamplingParams = {};
  if (typeof source.temperature === "number") params.temperature = source.temperature;
  if (typeof source.top_p === "number") params.top_p = source.top_p;
  if (typeof source.top_k === "number") params.top_k = source.top_k;
  if (typeof source.max_tokens === "number") params.max_tokens = source.max_tokens;
  return hasSamplingParams(params) ? params : undefined;
}

/**
 * Mirror of pi-ai's `openai-completions` compat rule (dist/api/openai-completions.js
 * `resolveCompat`): which of the two mutually exclusive output-length fields the
 * endpoint accepts. Only consulted when the outgoing payload carries neither
 * field already, which happens whenever pi itself sent no `maxTokens`.
 */
function openAIMaxTokensField(model: SamplingModel): "max_tokens" | "max_completion_tokens" {
  const configured = model.compat?.maxTokensField;
  if (configured === "max_tokens" || configured === "max_completion_tokens") {
    return configured;
  }
  const provider = model.provider ?? "";
  const baseUrl = model.baseUrl ?? "";
  const usesLegacyField =
    provider === "nvidia" ||
    provider === "ant-ling" ||
    provider === "moonshot" ||
    baseUrl.includes("chutes.ai") ||
    baseUrl.includes("moonshot") ||
    baseUrl.includes("together") ||
    baseUrl.includes("gateway.ai.cloudflare.com");
  return usesLegacyField ? "max_tokens" : "max_completion_tokens";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

/**
 * Inject the configured sampling parameters into a provider request payload.
 *
 * Returns `{payload: undefined}` when the payload is left untouched — the shape
 * `onPayload` uses to mean "send what you built". Parameters the target API (or
 * the current thinking mode) cannot carry are reported in `drops` instead of
 * being forced into a request the provider would reject.
 */
export function applySamplingToPayload(
  payload: unknown,
  params: SamplingParams | undefined,
  model: SamplingModel,
): ApplySamplingResult {
  if (!hasSamplingParams(params) || !isRecord(payload)) {
    return { payload: undefined, drops: [] };
  }
  const p = params as SamplingParams;
  const drops: SamplingDrop[] = [];
  const next: Record<string, unknown> = { ...payload };
  let changed = false;

  const set = (key: string, value: number | undefined) => {
    if (value === undefined) return;
    next[key] = value;
    changed = true;
  };
  const drop = (param: keyof SamplingParams, reason: string) => {
    if (p[param] === undefined) return;
    drops.push({ param, reason });
  };

  switch (model.api) {
    case "anthropic-messages": {
      // Anthropic rejects temperature/top_p/top_k outright once extended
      // thinking is on (and pi-ai already suppresses temperature for the same
      // reason), so honour the same constraint rather than failing the run.
      const thinking = next.thinking;
      const thinkingOn = isRecord(thinking) && thinking.type === "enabled";
      if (thinkingOn) {
        drop("temperature", "Anthropic does not accept temperature with extended thinking enabled");
        drop("top_p", "Anthropic does not accept top_p with extended thinking enabled");
        drop("top_k", "Anthropic does not accept top_k with extended thinking enabled");
      } else {
        set("temperature", p.temperature);
        set("top_p", p.top_p);
        set("top_k", p.top_k);
      }
      // max_tokens is always present on this API (pi defaults it to
      // model.maxTokens), and is accepted alongside thinking.
      set("max_tokens", p.max_tokens);
      break;
    }

    case "openai-completions": {
      set("temperature", p.temperature);
      set("top_p", p.top_p);
      if (p.max_tokens !== undefined) {
        const field =
          "max_completion_tokens" in next
            ? "max_completion_tokens"
            : "max_tokens" in next
              ? "max_tokens"
              : openAIMaxTokensField(model);
        set(field, p.max_tokens);
      }
      // top_k is not part of the OpenAI chat-completions schema. Most
      // OpenAI-compatible self-hosted gateways (vLLM, NIM, Ant Ling) do accept
      // it, and OpenAI proper 400s on unknown fields — so send it only when the
      // endpoint is not OpenAI itself.
      if (model.provider === "openai" || (model.baseUrl ?? "").includes("api.openai.com")) {
        drop("top_k", "OpenAI's chat completions API has no top_k parameter");
      } else {
        set("top_k", p.top_k);
      }
      break;
    }

    case "openai-responses":
    case "azure-openai-responses":
    case "openai-codex-responses": {
      // Reasoning models on the Responses API reject sampling controls.
      if (next.reasoning !== undefined) {
        drop("temperature", "the Responses API rejects temperature while reasoning is enabled");
        drop("top_p", "the Responses API rejects top_p while reasoning is enabled");
      } else {
        set("temperature", p.temperature);
        set("top_p", p.top_p);
      }
      set("max_output_tokens", p.max_tokens);
      drop("top_k", "the OpenAI Responses API has no top_k parameter");
      break;
    }

    case "google-generative-ai":
    case "google-vertex": {
      // Google nests generation settings under `config` and uses camelCase.
      const config = isRecord(next.config) ? { ...next.config } : {};
      if (p.temperature !== undefined) config.temperature = p.temperature;
      if (p.top_p !== undefined) config.topP = p.top_p;
      if (p.top_k !== undefined) config.topK = p.top_k;
      if (p.max_tokens !== undefined) config.maxOutputTokens = p.max_tokens;
      next.config = config;
      changed = true;
      break;
    }

    case "bedrock-converse-stream": {
      const inference = isRecord(next.inferenceConfig) ? { ...next.inferenceConfig } : {};
      if (p.temperature !== undefined) inference.temperature = p.temperature;
      if (p.top_p !== undefined) inference.topP = p.top_p;
      if (p.max_tokens !== undefined) inference.maxTokens = p.max_tokens;
      next.inferenceConfig = inference;
      changed = true;
      // Bedrock routes model-specific knobs through a separate bag.
      if (p.top_k !== undefined) {
        const extra = isRecord(next.additionalModelRequestFields)
          ? { ...next.additionalModelRequestFields }
          : {};
        extra.top_k = p.top_k;
        next.additionalModelRequestFields = extra;
      }
      break;
    }

    case "mistral-conversations": {
      set("temperature", p.temperature);
      set("top_p", p.top_p);
      set("maxTokens", p.max_tokens);
      drop("top_k", "the Mistral conversations API has no top_k parameter");
      break;
    }

    default: {
      for (const param of ["temperature", "top_p", "top_k", "max_tokens"] as const) {
        drop(param, `no sampling mapping for API "${String(model.api)}"`);
      }
      break;
    }
  }

  return { payload: changed ? next : undefined, drops };
}

/**
 * Build an `onPayload` handler that injects `params` and reports each distinct
 * drop reason once via `onDrop`. Returns undefined when nothing is configured,
 * so callers can leave the option off entirely.
 */
export function createSamplingPayloadHook(
  params: SamplingParams | undefined,
  onDrop?: (message: string) => void,
): ((payload: unknown, model: unknown) => unknown) | undefined {
  if (!hasSamplingParams(params)) return undefined;
  const reported = new Set<string>();
  return (payload: unknown, model: unknown) => {
    const { payload: next, drops } = applySamplingToPayload(payload, params, toSamplingModel(model));
    if (onDrop) {
      for (const d of drops) {
        const message = `sampling parameter ${d.param} ignored: ${d.reason}`;
        if (!reported.has(message)) {
          reported.add(message);
          onDrop(message);
        }
      }
    }
    return next;
  };
}
