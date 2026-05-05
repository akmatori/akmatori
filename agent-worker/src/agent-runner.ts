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
} from "@mariozechner/pi-coding-agent";
import { getModel, type Model, type ThinkingLevel as PiThinkingLevel } from "@mariozechner/pi-ai";
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
 * avoids gateways that reject "minimal" (e.g. ai.tools.gcore.com only accepts
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
      return builtInModel;
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
  };

  // For unknown "custom" endpoints (OpenAI-compatible gateways like Envoy AI Gateway),
  // disable OpenAI-specific extended cache fields. pi-ai's openai-completions provider
  // adds prompt_cache_key / prompt_cache_retention="24h" when cacheRetention is "long"
  // (PI_CACHE_RETENTION=long), and many OpenAI-compatible gateways reject those as
  // unsupported parameters with a 400.
  const compat = provider === "custom" ? { supportsLongCacheRetention: false } : undefined;

  return {
    id: modelId,
    name: modelId,
    api: apiMap[provider] ?? "openai-completions",
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

    // Model
    const model = resolveModel(
      params.llmSettings.provider,
      params.llmSettings.model,
      params.llmSettings.base_url,
    );
    const thinkingLevel = mapThinkingLevel(params.llmSettings.thinking_level);

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
    const enabledSkillNames = params.enabledSkills;
    const resourceLoader = new DefaultResourceLoader({
      cwd: params.workDir,
      agentDir: getAgentDir(),
      additionalSkillPaths: this.skillsDir ? [this.skillsDir] : [],
      noExtensions: true,
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

    // Create a typed bash ToolDefinition with spawnHook to inject MCP Gateway
    // env vars per-session, and promptGuidelines for system prompt inclusion.
    // Using createBashToolDefinition() (pi-mono 0.62.0+) returns a proper
    // ToolDefinition with typed promptGuidelines instead of requiring `as any`.
    // Passed via customTools so AgentSession picks up both the spawnHook and
    // the guidelines (the built-in bash tool is overridden by name match).
    const bashToolDef = createBashToolDefinition(params.workDir, {
      spawnHook: (ctx) => ({
        ...ctx,
        env: {
          ...ctx.env,
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
      customTools: [bashToolDef as unknown as import("@mariozechner/pi-coding-agent").ToolDefinition, gatewayCallTool, listToolsForToolTypeTool, getToolDetailTool, listToolTypesTool, executeScriptTool],
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
