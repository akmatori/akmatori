/**
 * Agent runner wrapping the pi-mono SDK.
 *
 * Creates and manages pi-mono agent sessions for incident analysis and
 * remediation. Handles multi-provider authentication, event streaming,
 * session lifecycle (execute / resume / cancel), and proxy configuration.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import {
  createAgentSession,
  AgentSession,
  AuthStorage,
  ModelRegistry,
  SessionManager,
  SettingsManager,
  DefaultResourceLoader,
  createBashToolDefinition,
  getAgentDir,
  type AgentSessionEvent,
} from "@earendil-works/pi-coding-agent";
import { getModel, type Model, type ThinkingLevel as PiThinkingLevel } from "@earendil-works/pi-ai";
import type { LLMSettings, ExecuteResult, ProxyConfig, ThinkingLevel, ToolAllowlistEntry } from "./types.js";
import { applyProxyConfig } from "./proxy.js";
import {
  formatToolArgs,
  formatToolOutput,
  extractToolText,
  type ToolExecutionTrace,
} from "./tool-output-formatter.js";
import { GatewayClient } from "./gateway-client.js";
import { createGatewayCallTool, createListToolsForToolTypeTool, createGetToolDetailTool, createListToolTypesTool, createExecuteScriptTool } from "./gateway-tools.js";

// ---------------------------------------------------------------------------
// Tool calling guidelines attached to the bash tool definition via typed
// promptGuidelines (ToolDefinition.promptGuidelines: string[]).
// ---------------------------------------------------------------------------

/**
 * Guidelines for infrastructure tool usage, attached to the bash tool
 * ToolDefinition via the typed `promptGuidelines` property (pi-mono 0.59.0+).
 * These appear in the system prompt's Guidelines section automatically when
 * the bash tool is active.
 *
 * Prior to 0.62.0, we used `(bashTool as any).promptGuidelines` on an
 * AgentTool instance. Since 0.62.0, built-in tools carry proper
 * ToolDefinition metadata, so we use `createBashToolDefinition()` which
 * returns a typed ToolDefinition with a `promptGuidelines: string[]` property.
 */
const BASH_TOOL_GUIDELINES: string[] = [
  "ALL infrastructure operations go through gateway_call. NEVER call tool names directly (e.g. victoria_metrics.label_values, ssh.execute_command, zabbix.get_hosts). Infrastructure tools: gateway_call, list_tools_for_tool_type, get_tool_detail, list_tool_types, execute_script.",
  "If you get a \"Tool not found\" error, you are calling it wrong — use gateway_call instead. Example: gateway_call(\"victoria_metrics.instant_query\", {query: \"up\"}, \"my-vm-instance\").",
  "ALWAYS use the read tool to load the relevant SKILL.md file first — it contains your instructions, output format requirements, and gateway_call usage examples for your assigned tools.",
  "Only use list_tools_for_tool_type / get_tool_detail as a fallback if SKILL.md doesn't cover the tool you need.",
  "For batch operations across multiple hosts or complex data processing, use execute_script. It runs JavaScript with built-in gateway_call(), list_tools_for_tool_type(), get_tool_detail(), and synchronous fs (readFileSync, writeFileSync). Do NOT use require() or import() in scripts.",
];

// ---------------------------------------------------------------------------
// Provider retry defaults (pi-mono 0.70.1+ retry.provider.* settings)
// ---------------------------------------------------------------------------

/**
 * Default provider-level retry/timeout settings forwarded to pi-mono via
 * SettingsManager. The 10-minute timeout protects long alert investigations
 * against slow on-prem/OpenRouter models that would otherwise abort mid-stream.
 */
const DEFAULT_PROVIDER_RETRY = {
  timeoutMs: 600_000,
  maxRetries: 3,
  maxRetryDelayMs: 60_000,
} as const;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ExecuteParams {
  incidentId: string;
  task: string;
  llmSettings: LLMSettings;
  proxyConfig?: ProxyConfig;
  workDir: string;
  /** Names of enabled skills — only these will be loaded from the shared skills directory */
  enabledSkills?: string[];
  /** Tool instances the incident is authorized to use. When undefined, the gateway allows all tools (safe default for direct/debug calls). */
  toolAllowlist?: ToolAllowlistEntry[];
  onOutput: (text: string) => void;
  onEvent?: (event: AgentSessionEvent) => void;
  /**
   * Called once the session has been added to activeSessions. The orchestrator
   * uses this to release a per-incident launch chain so the next launch's
   * abortInFlightSession sees the registered session instead of an empty slot
   * during the bootstrap window.
   */
  onRegistered?: () => void;
}

export interface ResumeParams {
  incidentId: string;
  sessionId: string;
  message: string;
  llmSettings: LLMSettings;
  proxyConfig?: ProxyConfig;
  workDir: string;
  /** Names of enabled skills — only these will be loaded from the shared skills directory */
  enabledSkills?: string[];
  /** Tool instances the incident is authorized to use. When undefined, the gateway allows all tools (safe default for direct/debug calls). */
  toolAllowlist?: ToolAllowlistEntry[];
  onOutput: (text: string) => void;
  onEvent?: (event: AgentSessionEvent) => void;
  /** See ExecuteParams.onRegistered. */
  onRegistered?: () => void;
}

export interface AgentRunnerConfig {
  mcpGatewayUrl: string;
  /** Directory containing SKILL.md definitions for pi-mono resource loader */
  skillsDir?: string;
}

// ---------------------------------------------------------------------------
// Thinking level mapping
// ---------------------------------------------------------------------------

/**
 * Map our ThinkingLevel to pi-mono's. pi-mono accepts "off" at the
 * createAgentSession boundary (pi-agent-core ThinkingLevel includes it), even
 * though pi-ai's narrower ThinkingLevel does not. For OpenAI-compatible
 * providers without a thinkingLevelMap, pi-mono omits `reasoning_effort`
 * entirely when level is "off" — which matches what users mean by "off" and
 * avoids OpenAI-compatible gateways that reject "minimal" (some only accept
 * high|medium|low|none).
 */
export function mapThinkingLevel(level: ThinkingLevel): PiThinkingLevel | "off" {
  if (level === "off") return "off";
  const valid: PiThinkingLevel[] = ["minimal", "low", "medium", "high", "xhigh"];
  return valid.includes(level as PiThinkingLevel) ? (level as PiThinkingLevel) : "medium";
}

// ---------------------------------------------------------------------------
// Model resolution
// ---------------------------------------------------------------------------

/**
 * Resolve a Model object from provider + model ID using pi-ai's registry.
 * Falls back to creating a custom model spec if the model isn't in the
 * built-in registry (e.g. custom endpoints or new models).
 */
export function resolveModel(
  provider: string,
  modelId: string,
  baseUrl?: string,
): Model<any> {
  try {
    const builtInModel = getModel(provider as any, modelId as any);
    // pi-ai may return undefined for unknown/custom models instead of throwing.
    // In that case, we must fall back to a custom model spec.
    if (builtInModel) {
      // Some anthropic-messages built-ins (e.g. MiniMax-M3) lack
      // forceAdaptiveThinking in the SDK registry. Merge the flag so they use
      // effort-based adaptive thinking instead of budget-based thinking with
      // the older interleaved-thinking beta header, which MiniMax's endpoint
      // does not support. Scope to ADAPTIVE_THINKING_REQUIRED_PROVIDERS only:
      // native Anthropic models that the SDK does not mark with the flag
      // (e.g. claude-sonnet-4-5, claude-haiku-4-5) must NOT receive it, as
      // they use the older budget-based thinking path intentionally.
      //
      // The compat access is nested inside the api check so TypeScript
      // resolves builtInModel.compat as AnthropicMessagesCompat (not the
      // union). TypeScript 5.5+ Set.has() narrowing can otherwise cause
      // the combined narrowing to evaluate incorrectly when used inline.
      // Some anthropic-messages built-ins (e.g. MiniMax-M3) lack
      // forceAdaptiveThinking in the SDK registry. For providers in
      // ADAPTIVE_THINKING_REQUIRED_PROVIDERS, merge the flag so they use
      // effort-based adaptive thinking instead of the older budget-based
      // interleaved-thinking path, which MiniMax's endpoint does not support.
      // Native Anthropic models must NOT receive the flag: the SDK intentionally
      // leaves it off for models that use budget-based thinking (e.g.
      // claude-sonnet-4-5, claude-haiku-4-5).
      //
      // The compat check uses (model as any) because TypeScript 5.9's
      // Set.has() narrowing interacts with Model<any>'s generic conditional
      // compat type, causing the narrowed compat to evaluate incorrectly
      // when builtInModel.api === "anthropic-messages" is inside a complex
      // && chain. The cast is safe: we only read forceAdaptiveThinking,
      // which is defined on AnthropicMessagesCompat when api is anthropic-messages.
      let resolved: typeof builtInModel = builtInModel;
      if (
        ADAPTIVE_THINKING_REQUIRED_PROVIDERS.has(provider) &&
        builtInModel.api === "anthropic-messages" &&
        !(builtInModel as any).compat?.forceAdaptiveThinking
      ) {
        const existingCompat = (builtInModel as any).compat ?? {};
        resolved = { ...builtInModel, compat: { ...existingCompat, forceAdaptiveThinking: true } } as typeof builtInModel;
      }
      // When the operator configured a custom base URL, apply it even for
      // built-in models so on-prem/private endpoints are used instead of the
      // SDK's public default.
      return baseUrl ? { ...resolved, baseUrl } : resolved;
    }
  } catch {
    // Continue to fallback model spec below.
  }

  // Model not in built-in registry - create a custom model spec.
  // This handles custom providers, openrouter, and newly released models.
  const apiMap: Record<string, string> = {
    openai: "openai-responses",
    anthropic: "anthropic-messages",
    google: "google-generative-ai",
    openrouter: "openai-completions",
    custom: "openai-completions",
    nvidia: "openai-completions",
    minimax: "anthropic-messages",
    "ant-ling": "openai-completions",
  };

  const apiType = apiMap[provider] ?? "openai-completions";

  // Build compat flags for the synthesized model spec:
  // - Disable OpenAI-specific cache fields for custom (OpenAI-compatible) endpoints;
  //   many gateways reject prompt_cache_key / prompt_cache_retention with 400.
  // - Enable adaptive thinking format for any provider using the Anthropic Messages API
  //   (e.g. minimax, on-prem Anthropic-compatible endpoints). The flag tells pi-ai to
  //   use the adaptive thinking wire format even when the model is not in the built-in
  //   registry.
  const compatFlags: Record<string, unknown> = {};
  if (provider === "custom") {
    compatFlags.supportsLongCacheRetention = false;
  }
  if (apiType === "anthropic-messages") {
    compatFlags.forceAdaptiveThinking = true;
  }
  const compat = Object.keys(compatFlags).length > 0 ? compatFlags : undefined;

  return {
    id: modelId,
    name: modelId,
    api: apiType,
    provider,
    baseUrl: baseUrl ?? "",
    reasoning: true,
    input: ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 128_000,
    maxTokens: 16_384,
    ...(compat ? { compat } : {}),
  } as Model<any>;
}

// ---------------------------------------------------------------------------
// Provider env var propagation
// ---------------------------------------------------------------------------

/**
 * Provider → env var name used by pi-ai's env-api-keys resolver. Mirrors the
 * single-key mapping from
 * @earendil-works/pi-ai/dist/env-api-keys.js#getApiKeyEnvVars so spawned
 * subagent subprocesses can authenticate against the same provider/key the
 * parent session uses. Only includes providers Akmatori actually configures.
 */
const PROVIDER_ENV_KEY: Record<string, string> = {
  anthropic: "ANTHROPIC_API_KEY",
  openai: "OPENAI_API_KEY",
  google: "GEMINI_API_KEY",
  openrouter: "OPENROUTER_API_KEY",
  nvidia: "NVIDIA_API_KEY",
  minimax: "MINIMAX_API_KEY",
  "ant-ling": "ANT_LING_API_KEY",
};

/**
 * Copy the active API key into process.env so child `pi` processes spawned by
 * pi-subagents inherit it. For built-in providers we use pi-ai's canonical env
 * var name (ANTHROPIC_API_KEY, OPENAI_API_KEY, etc.) so the child's env-api-keys
 * resolver finds it. For "custom" providers we use a dedicated akmatori env
 * var name that the models.json apiKey field references by name — keeps the
 * literal secret out of on-disk config while still letting the child resolve it.
 */
function propagateApiKeyToEnv(provider: string, apiKey: string): void {
  if (!apiKey) return;
  if (provider === "custom") {
    process.env[AKMATORI_CUSTOM_API_KEY_ENV] = apiKey;
    return;
  }
  const envVar = PROVIDER_ENV_KEY[provider];
  if (envVar) {
    process.env[envVar] = apiKey;
  }
}

// ---------------------------------------------------------------------------
// Subagent child-process config materialization
// ---------------------------------------------------------------------------

/**
 * Marker placed on any provider entry akmatori writes into models.json. Only
 * marker-bearing entries are managed (overwritten or cleaned up) by future
 * runs; entries without the marker are treated as operator-owned and never
 * mutated. TypeBox's Object schema doesn't reject unknown properties, so the
 * marker doesn't break pi's models.json validation.
 */
const AKMATORI_MANAGED_MARKER = "_akmatoriManaged" as const;
const AKMATORI_MANAGED_BASE_URL_MARKER = "_akmatoriManagedBaseUrl" as const;
const AKMATORI_MANAGED_COMPAT_MARKER = "_akmatoriManagedCompat" as const;

/**
 * Providers that use the Anthropic Messages API wire format but are NOT
 * native Anthropic — their endpoints require effort-based adaptive thinking
 * (forceAdaptiveThinking: true) rather than the older budget-based interleaved
 * thinking with the `interleaved-thinking` beta header, which they do not
 * support. Native Anthropic models only carry this flag when the SDK
 * explicitly marks them; we must not add it to unmarked built-ins.
 */
const ADAPTIVE_THINKING_REQUIRED_PROVIDERS = new Set(["minimax"]);

/**
 * Provider key in models.json that akmatori owns for "custom" UI selections.
 * We deliberately do NOT write under `providers.custom` because an operator
 * may have placed their own `providers.custom` there; clobbering it would be
 * surprising, and falling back to their entry when its baseUrl/apiKey/models
 * don't match the UI selection silently breaks subagent runs. Using a
 * dedicated key keeps akmatori's child-process config collision-free
 * regardless of operator state.
 */
const AKMATORI_CUSTOM_PROVIDER_KEY = "akmatori-custom" as const;

/**
 * Env var name written into models.json's apiKey field for the akmatori-custom
 * provider. Pi-mono's config resolver (resolveConfigValueOrThrow in
 * resolve-config-value.ts) treats an apiKey string as an env var name first
 * and falls back to literal only if process.env[name] is unset. We set this
 * env var in process.env so the child `pi` process inherits it through the
 * normal env propagation, keeping the raw API key out of the persistent
 * `<agentDir>/models.json` (which lives on the agent's home volume and is
 * readable by future agent/tool executions).
 */
const AKMATORI_CUSTOM_API_KEY_ENV = "AKMATORI_CUSTOM_PROVIDER_API_KEY" as const;

/**
 * Env var names that may carry a provider API key in this process. Bash
 * spawns inherit process.env by default (pi-mono's getShellEnv() spreads it),
 * so without scrubbing, a prompt-injected `env` or `echo $ANTHROPIC_API_KEY`
 * tool call would exfiltrate the operator's key through tool output. We
 * keep the variables in this process's env (pi-subagents spawns its own
 * child `pi` process with `{ ...process.env, ... }` to give the child its
 * model-resolution credentials) and strip them only on the bash boundary.
 *
 * Note: this spawnHook only scrubs env for the parent agent's bash tool.
 * Child `pi` processes spawned by pi-subagents instantiate their own bash
 * tool with the default getShellEnv() and pi-mono offers no config hook to
 * inject our spawnHook into the child. To prevent the same exfiltration
 * path from the subagent side, the system-supplied subagent definitions
 * (akmatori_data/agents/*.md) deliberately omit `bash` from their `tools:`
 * list. Operator-authored subagents that add `bash` re-open this surface.
 */
const PROVIDER_API_KEY_ENV_VARS: readonly string[] = [
  ...Object.values(PROVIDER_ENV_KEY),
  AKMATORI_CUSTOM_API_KEY_ENV,
];

function scrubProviderApiKeysFromEnv(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const out = { ...env };
  for (const name of PROVIDER_API_KEY_ENV_VARS) {
    delete out[name];
  }
  return out;
}

/**
 * Strip `//` line comments and trailing commas from JSON before parsing.
 * Mirrors the JSONC tolerance pi-mono applies to its on-disk config files
 * (see model-registry.js#stripJsonComments and settings-manager). Without
 * this, a valid operator-maintained JSONC file would fail strict JSON.parse
 * and we would otherwise either drop their customizations or refuse to read.
 */
function stripJsonComments(input: string): string {
  return input
    .replace(/"(?:\\.|[^"\\])*"|\/\/[^\n]*/g, (m) => (m[0] === '"' ? m : ""))
    .replace(/"(?:\\.|[^"\\])*"|,(\s*[}\]])/g, (m, tail) => tail ?? (m[0] === '"' ? m : ""));
}

let writeFileAtomicCounter = 0;

function writeFileAtomic(filePath: string, contents: string): void {
  const dir = path.dirname(filePath);
  fs.mkdirSync(dir, { recursive: true });
  // tmp path uses pid + hrtime + an in-process counter so two parallel sessions
  // calling writeFileAtomic on the same final path within the same millisecond
  // cannot collide on the tmp name. Pid + Date.now() alone would let the second
  // writer truncate the first writer's tmp file mid-write (fs.writeFileSync
  // opens with the default 'w' flag — no O_EXCL), corrupting the rename target.
  const unique = `${process.hrtime.bigint()}.${++writeFileAtomicCounter}`;
  const tmp = `${filePath}.tmp.${process.pid}.${unique}`;
  fs.writeFileSync(tmp, contents, { mode: 0o600, flag: "wx" });
  fs.renameSync(tmp, filePath);
}

/**
 * Reports whether `(provider, model)` resolves through pi-ai's built-in
 * model catalogue. The parent's `resolveModel` papers over a registry miss
 * by synthesizing a custom Model spec in memory, but the child `pi` process
 * spawned by pi-subagents only knows what `models.json` + the built-in
 * registry tell it. When this returns false for a built-in provider, the
 * UI-selected model is an unknown id (e.g. a newly released Claude model,
 * an OpenRouter id with vendor prefix, an on-prem deployment) and the child
 * would otherwise skip the saved-default path (`modelRegistry.find` misses)
 * and fall back to a hardcoded `defaultModelPerProvider` entry — running
 * subagents on the wrong model. We materialize the model in models.json so
 * the child's `find()` succeeds.
 */
function isBuiltInModelKnown(provider: string, model: string): boolean {
  try {
    // getModel returns undefined for unknown models in production but throws
    // for unknown providers under some pi-ai versions and in our test mocks.
    // Treat both as "not in the registry."
    return !!getModel(provider as any, model as any);
  } catch {
    return false;
  }
}

/**
 * Write `<agentDir>/models.json` so child `pi` processes spawned by
 * pi-subagents can resolve the parent's UI-selected model. The parent's
 * `ModelRegistry.inMemory(authStorage)` plus `resolveModel(provider, model,
 * baseUrl)` builds the model spec in this process's memory only — the
 * spawned subagent runs `pi` with `ModelRegistry.create(...)`, which reads
 * `<agentDir>/models.json` from disk. Without this file the child cannot
 * find the model id and every `subagent({...})` call (runbook-searcher,
 * memory-searcher, memory-writer) either fails to start or silently runs
 * on the wrong model.
 *
 * Two materialization paths, both gated on whether the UI-selected model
 * is unknown to the child's built-in registry:
 *
 *  1. provider === "custom" → write a dedicated `providers.akmatori-custom`
 *     entry with baseUrl + apiKey + a single model. The custom slot name
 *     decouples akmatori from any operator-supplied `providers.custom`.
 *  2. provider is built-in (anthropic, openai, google, openrouter) and the
 *     model id is NOT in pi-ai's built-in catalogue → write the model into
 *     `providers.<provider>.models[]` as a marker-bearing entry. The child
 *     inherits baseUrl/api/auth from pi-ai's built-in defaults (auth comes
 *     from the env var propagated by propagateApiKeyToEnv); only the model
 *     id needs to be registered. This handles newly released model
 *     versions, custom OpenRouter routes, and on-prem variants — anything
 *     the operator can enter as free text in the UI but isn't shipped in
 *     pi-mono's `defaultModelPerProvider` table.
 *
 * In both cases akmatori-owned entries carry the AKMATORI_MANAGED_MARKER so
 * we can identify them on the next sync. Unmarked entries are treated as
 * operator-owned and never mutated. Stale managed entries (from a prior
 * provider/model selection) are cleared as part of every write, so the
 * file never accumulates dead config across UI changes.
 *
 * Writes are atomic (write-to-tmp + rename) so a concurrent subagent reader
 * cannot observe a half-written file. If the existing models.json is
 * unparseable (even after JSONC stripping), we leave it alone rather than
 * overwriting — operators may keep their own JSONC config there and an
 * overwrite would silently drop their customizations.
 *
 * In practice akmatori deployments use one global LLM config, so concurrent
 * writes by parallel sessions produce identical content. If an operator
 * runs concurrent sessions with different configs, the last writer wins;
 * the child still resolves a valid model, just possibly not the one the
 * parent is using. This matches the existing single-global-config
 * assumption baked into propagateApiKeyToEnv.
 */
function writeCustomProviderModelsJson(
  provider: string,
  model: string,
  baseUrl: string | undefined,
): void {
  const agentDir = getAgentDir();
  const modelsPath = path.join(agentDir, "models.json");

  if (provider === "custom" && !baseUrl) {
    console.warn(
      "[agent-runner] custom provider missing base_url; subagents will fail to resolve model",
    );
    return;
  }

  const fileExists = fs.existsSync(modelsPath);
  let providers: Record<string, unknown> = {};
  if (fileExists) {
    try {
      const raw = fs.readFileSync(modelsPath, "utf-8");
      const parsed = JSON.parse(stripJsonComments(raw)) as { providers?: Record<string, unknown> };
      if (parsed && typeof parsed === "object" && parsed.providers && typeof parsed.providers === "object") {
        providers = { ...parsed.providers };
      }
    } catch (err) {
      // Preserve operator-maintained files rather than blowing them away.
      // Subagent invocations may fail to resolve the custom model, but
      // operator state stays intact for them to repair.
      console.warn(
        `[agent-runner] failed to parse ${modelsPath} (${(err as Error).message}); leaving file unchanged so operator customizations are preserved`,
      );
      return;
    }
  }

  const before = JSON.stringify(providers);

  // Operator-supplied JSONC could legally set a provider slot to `null` (or
  // any non-object). The `as ... | undefined` cast only narrows away the
  // undefined branch — at runtime the value could still be null/string/etc.,
  // so guard with an explicit object check before indexing into it.
  const akmatoriSlotRaw = providers[AKMATORI_CUSTOM_PROVIDER_KEY];
  const existingAkmatoriSlot =
    typeof akmatoriSlotRaw === "object" && akmatoriSlotRaw !== null
      ? (akmatoriSlotRaw as { [AKMATORI_MANAGED_MARKER]?: boolean })
      : undefined;
  const akmatoriSlotIsOperatorOwned =
    existingAkmatoriSlot !== undefined && !existingAkmatoriSlot[AKMATORI_MANAGED_MARKER];

  // Migration: prior versions wrote under `providers.custom` with the
  // marker. Now that we use a dedicated slot, drop any stale marker-bearing
  // `custom` entry so it cannot mislead an operator inspecting the file.
  // Unmarked `custom` entries are operator state and stay untouched.
  const customRaw = providers.custom;
  const existingCustom =
    typeof customRaw === "object" && customRaw !== null
      ? (customRaw as { [AKMATORI_MANAGED_MARKER]?: boolean })
      : undefined;
  if (existingCustom !== undefined && existingCustom[AKMATORI_MANAGED_MARKER] === true) {
    delete providers.custom;
  }

  // Clear stale akmatori-managed *model* entries across every built-in
  // provider. The active model may have moved between providers (e.g. from
  // openai → anthropic), and we don't want the child's registry to keep
  // resolving the previous custom id under the old provider. Operator-
  // supplied models (no marker) are left alone.
  for (const [providerName, providerCfg] of Object.entries(providers)) {
    if (providerName === AKMATORI_CUSTOM_PROVIDER_KEY) continue;
    if (typeof providerCfg !== "object" || providerCfg === null) continue;
    const cfg = providerCfg as { models?: unknown; [key: string]: unknown };
    let changed = false;
    const updated: Record<string, unknown> = { ...(providerCfg as Record<string, unknown>) };
    // Strip managed model entries so a model-id or provider change doesn't
    // leave a stale model in the registry.
    if (Array.isArray(cfg.models)) {
      const filtered = (cfg.models as Array<Record<string, unknown>>).filter(
        (m) => m?.[AKMATORI_MANAGED_MARKER] !== true,
      );
      if (filtered.length !== (cfg.models as unknown[]).length) {
        changed = true;
        if (filtered.length === 0) {
          delete updated.models;
        } else {
          updated.models = filtered;
        }
      }
    }
    // Strip managed baseUrl entries so clearing base_url in the UI removes
    // the stale endpoint from the subagent's inherited models.json.
    if (cfg[AKMATORI_MANAGED_BASE_URL_MARKER] === true) {
      changed = true;
      delete updated.baseUrl;
      delete updated[AKMATORI_MANAGED_BASE_URL_MARKER];
    }
    // Strip managed compat entries so switching away from a provider that
    // needed a compat override (e.g. minimax) does not leave stale compat
    // flags that could affect future subagent runs on a different provider.
    // Only remove the managed forceAdaptiveThinking key rather than the
    // entire compat object so operator-owned compat fields survive across runs.
    if (cfg[AKMATORI_MANAGED_COMPAT_MARKER] === true) {
      changed = true;
      if (typeof updated.compat === "object" && updated.compat !== null) {
        const compat = updated.compat as Record<string, unknown>;
        delete compat.forceAdaptiveThinking;
        if (Object.keys(compat).length === 0) {
          delete updated.compat;
        }
      } else {
        delete updated.compat;
      }
      delete updated[AKMATORI_MANAGED_COMPAT_MARKER];
    }
    // Only mutate the provider config when we actually removed something —
    // keeps the no-op short-circuit (before === after) honest.
    if (changed) {
      // If cleanup emptied the entire provider entry, remove it so the registry
      // doesn't reject an override-only provider with no baseUrl/models/etc.
      if (Object.keys(updated).length === 0) {
        delete providers[providerName];
      } else {
        providers[providerName] = updated;
      }
    }
  }

  if (provider === "custom") {
    if (akmatoriSlotIsOperatorOwned) {
      // An operator placed an entry under our dedicated slot. Don't clobber
      // it; subagents will still resolve through operator config, which is
      // the safer failure mode.
      console.warn(
        `[agent-runner] operator-managed providers.${AKMATORI_CUSTOM_PROVIDER_KEY} found in ${modelsPath}; not overwriting`,
      );
    } else {
      // Write the env var NAME, not the literal apiKey, so the secret stays
      // out of persistent on-disk state. The caller is responsible for
      // setting process.env[AKMATORI_CUSTOM_API_KEY_ENV] to the live key —
      // pi-mono's resolveConfigValueOrThrow reads the env var when resolving.
      providers[AKMATORI_CUSTOM_PROVIDER_KEY] = {
        baseUrl,
        api: "openai-completions",
        apiKey: AKMATORI_CUSTOM_API_KEY_ENV,
        compat: { supportsLongCacheRetention: false },
        models: [
          {
            id: model,
            name: model,
            reasoning: true,
            input: ["text"],
            contextWindow: 128000,
            maxTokens: 16384,
          },
        ],
        [AKMATORI_MANAGED_MARKER]: true,
      };
    }
  } else {
    if (existingAkmatoriSlot !== undefined && !akmatoriSlotIsOperatorOwned) {
      // Provider switched off "custom" — clean up our dedicated slot so the
      // child cannot stumble onto a stale baseUrl/apiKey. Operator-placed
      // entries (unmarked) stay untouched.
      delete providers[AKMATORI_CUSTOM_PROVIDER_KEY];
    }
    // Built-in provider with an unknown model id needs a registry entry on
    // disk so the child's `findInitialModel` saved-default path can resolve
    // it via `modelRegistry.find(provider, model)`. Without this the child
    // would fall through to step 4 (first available model) and run the
    // subagent on a hardcoded default, not the UI-selected model.
    // Additionally, when the operator supplied a custom base URL for a
    // built-in provider (e.g. on-prem NVIDIA NIM), write a provider-level
    // baseUrl override so child processes use the same endpoint.
    // For providers in ADAPTIVE_THINKING_REQUIRED_PROVIDERS (e.g. minimax),
    // always write a provider-level compat override so child processes apply
    // forceAdaptiveThinking even when the model is a known built-in that
    // lacks the flag in the SDK registry (e.g. MiniMax-M3). Without this,
    // the child reads the built-in entry directly and uses budget-based
    // thinking with the interleaved-thinking beta header, which the provider
    // endpoint rejects.
    const modelUnknown = !isBuiltInModelKnown(provider, model);
    const needsCompatOverride = ADAPTIVE_THINKING_REQUIRED_PROVIDERS.has(provider);
    if (modelUnknown || baseUrl || needsCompatOverride) {
      const existing = (providers[provider] as Record<string, unknown> | undefined) ?? {};
      const existingModels = Array.isArray(existing.models)
        ? (existing.models as Array<Record<string, unknown>>)
        : [];
      // Stale-cleanup above already stripped any prior akmatori-managed
      // model entry, so existingModels here contains only operator models.
      // Append our managed entry so an operator-supplied list stays first.
      // Merge forceAdaptiveThinking into any existing operator compat flags
      // so we do not clobber other compat fields the operator may have set.
      const existingCompat =
        typeof existing.compat === "object" && existing.compat !== null
          ? (existing.compat as Record<string, unknown>)
          : {};
      providers[provider] = {
        ...existing,
        ...(baseUrl ? { baseUrl, [AKMATORI_MANAGED_BASE_URL_MARKER]: true } : {}),
        ...(modelUnknown
          ? {
              models: [
                ...existingModels,
                {
                  id: model,
                  name: model,
                  reasoning: true,
                  input: ["text"],
                  contextWindow: 128000,
                  maxTokens: 16384,
                  [AKMATORI_MANAGED_MARKER]: true,
                },
              ],
            }
          : {}),
        ...(needsCompatOverride
          ? {
              compat: { ...existingCompat, forceAdaptiveThinking: true },
              [AKMATORI_MANAGED_COMPAT_MARKER]: true,
            }
          : {}),
      };
    }
  }

  const after = JSON.stringify(providers);
  if (before === after && fileExists) {
    // No changes to persist — avoid touching mtime so operators watching
    // for unexpected config drift don't see spurious writes.
    return;
  }
  if (before === after && !fileExists) {
    // No changes and no file to begin with — don't create an empty file.
    return;
  }

  try {
    writeFileAtomic(modelsPath, JSON.stringify({ providers }, null, 2));
  } catch (err) {
    console.warn(`[agent-runner] failed to write ${modelsPath}: ${(err as Error).message}`);
  }
}

/**
 * Write `<agentDir>/settings.json` (global) AND `<workDir>/.pi/settings.json`
 * (project) so child `pi` processes pick the same provider+model+thinking the
 * parent session uses. Without this, the child's `findInitialModel` falls back
 * to its provider-default catalogue (e.g. `claude-opus-4-7` for anthropic) or
 * the first available model when multiple provider env vars happen to be set —
 * running subagents on the wrong model or, in env-pollution edge cases, the
 * wrong provider entirely.
 *
 * Why both files: pi-mono's SettingsManager deep-merges global with project,
 * project taking precedence (FileSettingsStorage in settings-manager.ts). If
 * only the global file is written, a `<workDir>/.pi/settings.json` carrying
 * `defaultProvider`/`defaultModel`/`enabledModels` would silently override our
 * choice. The project file pins the same values at the higher-precedence
 * scope so neither pre-existing state nor anything written into workDir during
 * the session can shadow the parent's UI selection. The parent itself uses
 * `SettingsManager.inMemory(...)` and does not read either file.
 *
 * For UI-selected "custom" providers we point the child at the dedicated
 * `akmatori-custom` slot we materialize in models.json (see
 * `writeCustomProviderModelsJson`) rather than the unmanaged `custom` slot
 * an operator may own.
 *
 * `defaultThinkingLevel` is pinned so subagents honor the UI-selected thinking
 * mode. This matters most for OpenAI-compatible custom endpoints where the UI
 * set thinking "off" — without the explicit setting the child would fall back
 * to "medium" and send reasoning parameters that the gateway rejects.
 *
 * `enabledModels` is also cleared: pi's CLI scope-resolution
 * (main.ts#buildSessionOptions) applies `enabledModels` *before* falling
 * back to saved defaults, so an operator-set `enabledModels: ["openai/*"]`
 * would override our `defaultProvider: "anthropic"` and pick the first
 * OpenAI model in scope. The cycling restriction makes sense for an
 * operator running `pi` interactively in this agentDir, but subagent
 * subprocesses are headless and must use the UI-selected model.
 *
 * Reads/writes are JSONC-tolerant so operator-maintained settings.json
 * files (with comments) survive a round-trip. Only `defaultProvider`,
 * `defaultModel`, `defaultThinkingLevel`, and `enabledModels` are touched;
 * every other operator preference (theme, keybindings, custom skills, etc.)
 * is preserved. On parse failure we skip the write and warn — overwriting an
 * operator's settings on every incident would be worse than the subagent
 * picking a default.
 */
function writeSubagentDefaultsSettings(
  provider: string,
  model: string,
  thinkingLevel: PiThinkingLevel | "off",
  workDir: string,
): void {
  // For UI-selected "custom", route the child at the dedicated akmatori
  // slot so the operator's `providers.custom` (if any) cannot intercept.
  const targetProvider = provider === "custom" ? AKMATORI_CUSTOM_PROVIDER_KEY : provider;

  const globalPath = path.join(getAgentDir(), "settings.json");
  writeSubagentSettingsFile(globalPath, targetProvider, model, thinkingLevel);

  // Project scope wins over global. We unconditionally pin the same values
  // at workDir/.pi/settings.json so any project-level override (intentional
  // or accidental) cannot shadow the parent's selection.
  const projectPath = path.join(workDir, ".pi", "settings.json");
  writeSubagentSettingsFile(projectPath, targetProvider, model, thinkingLevel);
}

function writeSubagentSettingsFile(
  settingsPath: string,
  targetProvider: string,
  model: string,
  thinkingLevel: PiThinkingLevel | "off",
): void {
  let settings: Record<string, unknown> = {};
  const fileExists = fs.existsSync(settingsPath);
  if (fileExists) {
    try {
      const raw = fs.readFileSync(settingsPath, "utf-8");
      const parsed = JSON.parse(stripJsonComments(raw));
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
        settings = parsed as Record<string, unknown>;
      }
    } catch (err) {
      console.warn(
        `[agent-runner] failed to parse ${settingsPath} (${(err as Error).message}); leaving file unchanged`,
      );
      return;
    }
  }

  const hadEnabledModels = Object.prototype.hasOwnProperty.call(settings, "enabledModels");
  if (
    settings.defaultProvider === targetProvider &&
    settings.defaultModel === model &&
    settings.defaultThinkingLevel === thinkingLevel &&
    !hadEnabledModels
  ) {
    // Avoid touching mtime when nothing changed; reduces noise for
    // operators watching for unexpected config drift.
    return;
  }

  settings.defaultProvider = targetProvider;
  settings.defaultModel = model;
  settings.defaultThinkingLevel = thinkingLevel;
  if (hadEnabledModels) {
    delete settings.enabledModels;
  }

  try {
    writeFileAtomic(settingsPath, JSON.stringify(settings, null, 2));
  } catch (err) {
    console.warn(`[agent-runner] failed to write ${settingsPath}: ${(err as Error).message}`);
  }
}

// ---------------------------------------------------------------------------
// AgentRunner
// ---------------------------------------------------------------------------

export class AgentRunner {
  private readonly mcpGatewayUrl: string;
  private readonly skillsDir?: string;
  private activeSessions = new Map<string, AgentSession>();

  constructor(config: AgentRunnerConfig) {
    this.mcpGatewayUrl = config.mcpGatewayUrl;
    this.skillsDir = config.skillsDir;
  }

  /**
   * Execute a new agent session for an incident.
   */
  async execute(params: ExecuteParams): Promise<ExecuteResult> {
    return this.runSession(params, params.task, false);
  }

  /**
   * Resume an existing session with a follow-up message.
   */
  async resume(params: ResumeParams): Promise<ExecuteResult> {
    return this.runSession(params, params.message, true);
  }

  /**
   * Common session setup and execution logic shared by execute() and resume().
   */
  private async runSession(
    params: ExecuteParams | ResumeParams,
    promptText: string,
    isResume: boolean,
  ): Promise<ExecuteResult> {
    const startTime = Date.now();

    // Set up proxy env vars before creating session
    applyProxyConfig(params.proxyConfig);

    // Auth
    const authStorage = AuthStorage.inMemory();
    authStorage.setRuntimeApiKey(params.llmSettings.provider, params.llmSettings.api_key);
    // pi-subagents spawns each subagent in a child `pi` process whose
    // AuthStorage is independent from this one — `setRuntimeApiKey` lives in
    // the parent's memory only. The child resolves keys from env vars (pi-ai
    // env-api-keys.js), so we mirror the active key into process.env using the
    // provider's canonical variable name. Without this, every `subagent({...})`
    // invocation fails with "no API key configured".
    propagateApiKeyToEnv(params.llmSettings.provider, params.llmSettings.api_key);
    // For "custom" providers the env-var path isn't enough — the child also
    // needs to discover the model id and the operator's baseUrl. We hand
    // those over via `<agentDir>/models.json`, which the child's
    // ModelRegistry.create() reads at startup. The apiKey is referenced by
    // env var name so the literal secret stays out of the persistent file.
    writeCustomProviderModelsJson(
      params.llmSettings.provider,
      params.llmSettings.model,
      params.llmSettings.base_url,
    );

    // Model
    const model = resolveModel(
      params.llmSettings.provider,
      params.llmSettings.model,
      params.llmSettings.base_url,
    );
    const thinkingLevel = mapThinkingLevel(params.llmSettings.thinking_level);

    // Pin the child's default provider+model+thinking so subagents run on the
    // same model/thinking the parent session uses. Without this, the child's
    // `findInitialModel` picks the provider's catalogue default (e.g.
    // `claude-opus-4-7` for anthropic) and can drift to a different
    // provider entirely if multiple provider env vars are set. We write both
    // global (agentDir) and project (workDir/.pi) scopes because pi-mono
    // merges them with project taking precedence — pinning both prevents a
    // pre-existing or in-session project file from shadowing the parent's
    // selection.
    writeSubagentDefaultsSettings(
      params.llmSettings.provider,
      params.llmSettings.model,
      thinkingLevel,
      params.workDir,
    );

    // Session management: persist to disk so resume can restore conversation history.
    // For resume, use continueRecent to load the most recent session from the
    // incident's workspace directory. For new sessions, create a fresh one.
    //
    // Deterministic session IDs (pi-mono 0.58.0): For new sessions, we call
    // newSession({ id: incidentId }) to use the incident UUID as the pi-mono
    // session ID. This eliminates the separate incident_id ↔ session_id mapping
    // and makes debugging/audit simpler (grep by incident UUID finds everything).
    //
    // sessionDir (pi-mono 0.63.0): We pass a dedicated .sessions subdirectory
    // to isolate pi-mono session JSONL files from the agent's workspace files
    // (tool outputs, runbooks, SKILL.md, etc.). This makes cleanup easier and
    // avoids cluttering the workspace root with encoded-cwd session directories.
    const sessionDir = path.join(params.workDir, ".sessions");
    const sessionManager = isResume
      ? SessionManager.continueRecent(params.workDir, sessionDir)
      : SessionManager.create(params.workDir, sessionDir);
    if (!isResume) {
      sessionManager.newSession({ id: params.incidentId });
    }
    const settingsManager = SettingsManager.inMemory({
      retry: { provider: DEFAULT_PROVIDER_RETRY },
    });
    const modelRegistry = ModelRegistry.inMemory(authStorage);

    // Create resource loader with skills directory for pi-mono's native skill system.
    // This discovers SKILL.md files and includes name+description in the system prompt,
    // loading full content on-demand when the agent invokes a skill.
    // Use skillsOverride to filter to only enabled skills — disabled skills may still
    // have SKILL.md files on disk but should not be available to the agent.
    //
    // Extensions are enabled so pi-mono picks up pi-subagents (baked into the image
    // at /opt/pi-extensions/pi-subagents — outside the host-mounted extensions
    // directory so it can't be shadowed by an empty operator mount). That registers
    // the `subagent` tool used by runbook-searcher / memory-searcher / memory-writer.
    const enabledSkillNames = params.enabledSkills;
    const resourceLoader = new DefaultResourceLoader({
      cwd: params.workDir,
      agentDir: getAgentDir(),
      additionalSkillPaths: this.skillsDir ? [this.skillsDir] : [],
      additionalExtensionPaths: ["/opt/pi-extensions/pi-subagents"],
      noExtensions: false,
      noPromptTemplates: true,
      noThemes: true,
      ...(enabledSkillNames && enabledSkillNames.length > 0
        ? {
            skillsOverride: (base) => {
              const allowedSet = new Set(enabledSkillNames);
              return {
                skills: base.skills.filter((s) => allowedSet.has(s.name)),
                diagnostics: base.diagnostics,
              };
            },
          }
        : {}),
    });
    await resourceLoader.reload();
    // Surface extension load failures so a missing pi-subagents (transitive
    // dep break, jiti error, peer-dep mismatch) shows up as a startup signal
    // instead of silently disabling `subagent({...})` calls mid-investigation.
    const extResult = resourceLoader.getExtensions();
    for (const extErr of extResult.errors ?? []) {
      console.warn(`[agent-runner] extension load error path=${extErr.path}: ${extErr.error}`);
    }

    // Create a typed bash ToolDefinition with spawnHook to inject MCP Gateway
    // env vars per-session, and promptGuidelines for system prompt inclusion.
    // Using createBashToolDefinition() (pi-mono 0.62.0+) returns a proper
    // ToolDefinition with typed promptGuidelines instead of requiring `as any`.
    // Passed via customTools so AgentSession picks up both the spawnHook and
    // the guidelines (the built-in bash tool is overridden by name match).
    //
    // We scrub provider API key env vars from the spawned shell so a
    // prompt-injected `env`/`curl $ANTHROPIC_API_KEY ...` cannot exfiltrate
    // the operator's LLM credentials via tool output. The keys still live in
    // this process's env so pi-subagents (which spawns its own child `pi` via
    // node:child_process.spawn with explicit `{ ...process.env, ... }`) can
    // resolve them.
    const bashToolDef = createBashToolDefinition(params.workDir, {
      spawnHook: (ctx) => ({
        ...ctx,
        env: {
          ...scrubProviderApiKeysFromEnv(ctx.env),
          MCP_GATEWAY_URL: this.mcpGatewayUrl,
          INCIDENT_ID: params.incidentId,
        },
      }),
    });
    bashToolDef.promptGuidelines = BASH_TOOL_GUIDELINES;

    // Create gateway client for this session and register gateway tools as custom tools.
    const toolAllowlist = params.toolAllowlist;
    const gatewayClient = new GatewayClient({
      gatewayUrl: this.mcpGatewayUrl,
      incidentId: params.incidentId,
      workDir: params.workDir,
      toolAllowlist,
    });
    const gatewayToolCtx = { client: gatewayClient };
    const gatewayCallTool = createGatewayCallTool(gatewayToolCtx);
    const listToolsForToolTypeTool = createListToolsForToolTypeTool(gatewayToolCtx);
    const getToolDetailTool = createGetToolDetailTool(gatewayToolCtx);
    const listToolTypesTool = createListToolTypesTool(gatewayToolCtx);
    const executeScriptTool = createExecuteScriptTool({
      client: gatewayClient,
      workDir: params.workDir,
    });

    const { session } = await createAgentSession({
      cwd: params.workDir,
      authStorage,
      modelRegistry,
      model,
      thinkingLevel,
      // bashToolDef has specific type parameters (BashToolDetails, BashRenderState)
      // that are contravariant with ToolDefinition<TSchema, unknown, any> via renderCall/renderResult.
      // The cast is safe — AgentSession only reads name, execute, promptGuidelines, promptSnippet.
      customTools: [bashToolDef as unknown as import("@earendil-works/pi-coding-agent").ToolDefinition, gatewayCallTool, listToolsForToolTypeTool, getToolDetailTool, listToolTypesTool, executeScriptTool],
      resourceLoader,
      sessionManager,
      settingsManager,
    });

    this.activeSessions.set(params.incidentId, session);
    // Signal the orchestrator that this launch has registered so it can
    // release the per-incident launch chain. Subsequent launches that were
    // queued behind us now see a populated activeSessions slot and their
    // abortInFlightSession can target this session instead of no-op'ing.
    params.onRegistered?.();

    // Accumulate response and token usage
    let responseText = "";
    let fullLog = "";
    let totalTokens = 0;
    const toolTraces = new Map<string, ToolExecutionTrace>();
    const thinkingBuffers = new Map<number, string>();

    let lastErrorMessage = "";
    const unsubscribe = session.subscribe((event: AgentSessionEvent) => {
      params.onEvent?.(event);

      // Capture API-level errors from message_end / turn_end events.
      // The SDK surfaces provider errors (quota, auth, model not found, etc.)
      // as a message with stopReason "error" and an errorMessage field,
      // rather than throwing an exception.
      if (event.type === "message_end" || event.type === "turn_end") {
        const msg = event.message;
        if (msg && "role" in msg && msg.role === "assistant") {
          if (msg.stopReason === "error" && msg.errorMessage) {
            lastErrorMessage = msg.errorMessage;
          } else if (msg.stopReason !== "error") {
            // Clear any previously latched error — a successful retry or
            // subsequent turn means the session recovered from the transient
            // failure and the error should not be propagated.
            lastErrorMessage = "";
          }
        }
      }

      this.handleEvent(event, params.onOutput, (text) => {
        responseText += text;
        fullLog += text;
      }, (text) => {
        fullLog += text;
      }, (tokens) => {
        totalTokens += tokens;
      }, toolTraces, thinkingBuffers);
    });

    try {
      await session.prompt(promptText);

      const sessionExportPath = this.exportSession(sessionManager, params.workDir);

      // Use SDK's getLastAssistantText() for a clean final response.
      // The accumulated responseText includes text from ALL turns (e.g.
      // "I'll investigate...", "Let me gather data...") which pollutes the
      // response field. We only want the last assistant message — the actual
      // investigation summary.
      const finalResponse = session.getLastAssistantText() ?? responseText;

      return {
        session_id: session.sessionId,
        response: finalResponse,
        full_log: fullLog,
        // Propagate API-level errors (quota, auth, model not found) even when
        // partial response text was collected from earlier turns.
        error: lastErrorMessage || undefined,
        tokens_used: totalTokens,
        execution_time_ms: Date.now() - startTime,
        session_export: sessionExportPath,
      };
    } catch (err) {
      const sessionExportPath = this.exportSession(sessionManager, params.workDir);

      return {
        session_id: session.sessionId,
        response: responseText,
        full_log: fullLog,
        error: (err as Error).message,
        tokens_used: totalTokens,
        execution_time_ms: Date.now() - startTime,
        session_export: sessionExportPath,
      };
    } finally {
      unsubscribe();
      // Only delete from activeSessions if THIS session is still the current
      // entry. When the orchestrator handles a second new_incident for the
      // same incident_id, it aborts the prior session and stores the
      // replacement here; if the prior session's finally still ran an
      // unconditional delete, it would remove the replacement and the next
      // cancel call would no-op. Same-session compare-and-delete keeps the
      // map honest under back-to-back starts.
      if (this.activeSessions.get(params.incidentId) === session) {
        this.activeSessions.delete(params.incidentId);
      }
    }
  }

  /**
   * Cancel an active execution for an incident.
   *
   * Compare-and-delete on the slot: if a replacement run has already
   * installed itself in activeSessions while this cancel awaited
   * session.abort(), do NOT delete — that would untrack the live
   * replacement and let later cancel/supersede requests miss it. The
   * superseded session's own finally block also compare-and-deletes,
   * so the slot still gets cleaned up by whichever owner sees itself
   * still in the map.
   */
  async cancel(incidentId: string): Promise<void> {
    const session = this.activeSessions.get(incidentId);
    if (session) {
      await session.abort();
      if (this.activeSessions.get(incidentId) === session) {
        this.activeSessions.delete(incidentId);
      }
    }
  }

  /**
   * Clean up all active sessions.
   */
  async dispose(): Promise<void> {
    for (const [id, session] of this.activeSessions) {
      try {
        await session.abort();
      } catch {
        // ignore errors during cleanup
      }
    }
    this.activeSessions.clear();
  }

  /**
   * Check if an incident has an active session.
   */
  hasActiveSession(incidentId: string): boolean {
    return this.activeSessions.has(incidentId);
  }

  // -------------------------------------------------------------------------
  // Private helpers
  // -------------------------------------------------------------------------

  /**
   * Export the session as JSONL to {workDir}/session_export.jsonl for
   * post-mortem analysis. Copies the pi-mono session file (already JSONL
   * format) to a well-known location. Returns the export path on success,
   * or undefined if export fails (non-fatal).
   */
  private exportSession(
    sessionManager: SessionManager,
    workDir: string,
  ): string | undefined {
    try {
      const sessionFile = sessionManager.getSessionFile();
      if (!sessionFile) return undefined;

      const exportPath = path.join(workDir, "session_export.jsonl");
      fs.copyFileSync(sessionFile, exportPath);
      return exportPath;
    } catch {
      // Export failure is non-fatal — the investigation result is more important
      return undefined;
    }
  }

  /**
   * Handle a pi-mono session event, dispatching to appropriate callbacks.
   */
  private handleEvent(
    event: AgentSessionEvent,
    onOutput: (text: string) => void,
    onResponseText: (text: string) => void,
    onLogText: (text: string) => void,
    onTokens: (tokens: number) => void,
    toolTraces: Map<string, ToolExecutionTrace>,
    thinkingBuffers: Map<number, string>,
  ): void {
    switch (event.type) {
      case "message_update": {
        const assistantEvent = event.assistantMessageEvent;
        if (assistantEvent.type === "text_delta") {
          onOutput(assistantEvent.delta);
          onResponseText(assistantEvent.delta);
        } else if (assistantEvent.type === "thinking_start") {
          thinkingBuffers.set(assistantEvent.contentIndex, "");
        } else if (assistantEvent.type === "thinking_delta") {
          const current = thinkingBuffers.get(assistantEvent.contentIndex) ?? "";
          thinkingBuffers.set(assistantEvent.contentIndex, current + assistantEvent.delta);
        } else if (assistantEvent.type === "thinking_end") {
          const thought = (thinkingBuffers.get(assistantEvent.contentIndex) ?? assistantEvent.content ?? "").trim();
          thinkingBuffers.delete(assistantEvent.contentIndex);
          if (thought) {
            const thoughtLine = `\n🤔 ${thought}\n`;
            onOutput(thoughtLine);
            onLogText(thoughtLine);
          }
        }
        break;
      }

      case "tool_execution_start": {
        toolTraces.set(event.toolCallId, {
          toolName: event.toolName,
          args: event.args,
          updates: [],
        });
        const startLine = `\n🛠️ Running: ${event.toolName}\n`;
        onOutput(startLine);
        onLogText(startLine);
        break;
      }

      case "tool_execution_update": {
        const trace: ToolExecutionTrace = toolTraces.get(event.toolCallId) ?? {
          toolName: event.toolName,
          args: event.args,
          updates: [],
        };
        const updateText = extractToolText(event.partialResult);
        if (updateText) {
          trace.updates.push(updateText);
        }
        toolTraces.set(event.toolCallId, trace);
        break;
      }

      case "tool_execution_end": {
        const trace = toolTraces.get(event.toolCallId);
        const status = event.isError ? "❌ Failed:" : "✅ Ran:";
        const argsText = formatToolArgs(trace?.args);
        const outputText = formatToolOutput(trace?.updates ?? [], event.result);

        let resultSummary = `\n${status} ${event.toolName}`;
        if (argsText) {
          resultSummary += `\nArgs:\n${argsText}`;
        }
        if (outputText) {
          resultSummary += `\nOutput:\n${outputText}`;
        }
        resultSummary += "\n";

        onOutput(resultSummary);
        onLogText(resultSummary);
        toolTraces.delete(event.toolCallId);
        break;
      }

      case "turn_end": {
        // Extract token usage from the assistant message
        if (event.message && "usage" in event.message && event.message.usage) {
          const usage = event.message.usage as { totalTokens?: number };
          if (usage.totalTokens) {
            onTokens(usage.totalTokens);
          }
        }
        break;
      }

      case "compaction_start": {
        const compactLine = `\n📦 Compacting context (${event.reason})...\n`;
        onOutput(compactLine);
        onLogText(compactLine);
        break;
      }

      case "compaction_end": {
        let compactResult: string;
        if (event.aborted) {
          compactResult = "\n📦 Context compaction aborted";
          if (event.errorMessage) {
            compactResult += `: ${event.errorMessage}`;
          }
          if (event.willRetry) {
            compactResult += " (will retry)";
          }
          compactResult += "\n";
        } else {
          compactResult = "\n📦 Context compaction complete\n";
        }
        onOutput(compactResult);
        onLogText(compactResult);
        break;
      }

      case "auto_retry_start": {
        const retryLine = `\n🔄 Retrying (attempt ${event.attempt}/${event.maxAttempts}): ${event.errorMessage}\n`;
        onOutput(retryLine);
        onLogText(retryLine);
        break;
      }

      case "auto_retry_end": {
        if (!event.success) {
          const failLine = `\n🔄 All retries exhausted after ${event.attempt} attempts: ${event.finalError ?? "unknown error"}\n`;
          onOutput(failLine);
          onLogText(failLine);
        }
        break;
      }

      default:
        // Other events (agent_start, agent_end, turn_start, etc.) - no output needed
        break;
    }
  }

}
