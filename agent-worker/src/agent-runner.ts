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
  type AgentSessionEvent,
} from "@mariozechner/pi-coding-agent";
import { getModel, type Model, type ThinkingLevel as PiThinkingLevel } from "@mariozechner/pi-ai";
import { Agent as UndiciAgent, EnvHttpProxyAgent, setGlobalDispatcher } from "undici";
import type { LLMSettings, ExecuteResult, ProxyConfig, ThinkingLevel, ToolAllowlistEntry } from "./types.js";
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
  "CRITICAL: You only have 5 tools available: gateway_call, list_tools_for_tool_type, get_tool_detail, list_tool_types, execute_script. ALL infrastructure operations go through gateway_call. NEVER call tool names directly (e.g. victoria_metrics.label_values, ssh.execute_command, zabbix.get_hosts).",
  "If you get a \"Tool not found\" error, you are calling it wrong — use gateway_call instead. Example: gateway_call(\"victoria_metrics.instant_query\", {query: \"up\"}, \"my-vm-instance\").",
  "Each skill's SKILL.md contains your assigned tools with parameter schemas and gateway_call examples. ALWAYS read the relevant SKILL.md FIRST — it has everything you need.",
  "Only use list_tools_for_tool_type / get_tool_detail as a fallback if SKILL.md doesn't cover the tool you need.",
  "For batch operations across multiple hosts or complex data processing, use execute_script. It runs JavaScript with built-in gateway_call(), list_tools_for_tool_type(), get_tool_detail(), and synchronous fs (readFileSync, writeFileSync). Do NOT use require() or import() in scripts.",
];

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
 * Map our ThinkingLevel (which includes "off") to pi-mono's ThinkingLevel.
 * pi-mono does not have "off" - we map it to "minimal" as the closest.
 */
export function mapThinkingLevel(level: ThinkingLevel): PiThinkingLevel {
  if (level === "off") return "minimal";
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
    this.applyProxyConfig(params.proxyConfig, params.llmSettings.provider);

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
    const settingsManager = SettingsManager.inMemory();
    const modelRegistry = new ModelRegistry(authStorage);

    // Create resource loader with skills directory for pi-mono's native skill system.
    // This discovers SKILL.md files and includes name+description in the system prompt,
    // loading full content on-demand when the agent invokes a skill.
    // Use skillsOverride to filter to only enabled skills — disabled skills may still
    // have SKILL.md files on disk but should not be available to the agent.
    const enabledSkillNames = "enabledSkills" in params ? params.enabledSkills : undefined;
    const resourceLoader = new DefaultResourceLoader({
      cwd: params.workDir,
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
    const toolAllowlist = "toolAllowlist" in params ? params.toolAllowlist : undefined;
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
      // that are contravariant with ToolDefinition<TSchema, unknown, any> due to
      // renderCall/renderResult generics. The cast is safe — AgentSession only reads
      // the definition's name, execute, promptGuidelines, and promptSnippet fields.
      customTools: [bashToolDef as unknown as import("@mariozechner/pi-coding-agent").ToolDefinition, gatewayCallTool, listToolsForToolTypeTool, getToolDetailTool, listToolTypesTool, executeScriptTool],
      resourceLoader,
      sessionManager,
      settingsManager,
    });

    this.activeSessions.set(params.incidentId, session);

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
        const msg = (event as any).message;
        if (msg?.stopReason === "error" && msg?.errorMessage) {
          lastErrorMessage = msg.errorMessage;
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

      // If the SDK reported an API-level error, propagate it
      if (lastErrorMessage && !responseText) {
        return {
          session_id: session.sessionId,
          response: responseText,
          full_log: fullLog,
          error: lastErrorMessage,
          tokens_used: totalTokens,
          execution_time_ms: Date.now() - startTime,
          session_export: sessionExportPath,
        };
      }

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
      this.activeSessions.delete(params.incidentId);
    }
  }

  /**
   * Cancel an active execution for an incident.
   */
  async cancel(incidentId: string): Promise<void> {
    const session = this.activeSessions.get(incidentId);
    if (session) {
      await session.abort();
      this.activeSessions.delete(incidentId);
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

  /**
   * Apply proxy configuration for LLM API calls.
   *
   * Node.js's built-in fetch (undici) does NOT automatically honour
   * HTTP_PROXY / HTTPS_PROXY env vars. We must explicitly set the global
   * undici dispatcher to an EnvHttpProxyAgent so that all fetch() calls
   * (used by the LLM provider SDKs) route through the configured proxy.
   *
   * We still set the env vars as well — some libraries (e.g. node-fetch,
   * child processes) do read them.
   *
   * Note: the global dispatcher is process-wide state shared by concurrent
   * sessions. In practice, proxy config is system-global so all sessions
   * receive the same setting.
   */
  private applyProxyConfig(
    proxyConfig: ProxyConfig | undefined,
    provider: string,
  ): void {
    if (proxyConfig?.url && proxyConfig.llm_enabled) {
      process.env.HTTP_PROXY = proxyConfig.url;
      process.env.HTTPS_PROXY = proxyConfig.url;
      process.env.NO_PROXY = proxyConfig.no_proxy || "";

      // Set undici's global dispatcher so fetch() uses the proxy
      setGlobalDispatcher(new EnvHttpProxyAgent());
    } else {
      process.env.HTTP_PROXY = "";
      process.env.HTTPS_PROXY = "";
      process.env.NO_PROXY = "";

      // Reset to default dispatcher (no proxy)
      setGlobalDispatcher(new UndiciAgent());
    }
  }
}
