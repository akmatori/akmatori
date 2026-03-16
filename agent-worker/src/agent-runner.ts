/**
 * Agent runner wrapping the pi-mono SDK.
 *
 * Creates and manages pi-mono agent sessions for incident analysis and
 * remediation. Handles multi-provider authentication, event streaming,
 * session lifecycle (execute / resume / cancel), and proxy configuration.
 */

import {
  createAgentSession,
  AgentSession,
  AuthStorage,
  ModelRegistry,
  SessionManager,
  SettingsManager,
  DefaultResourceLoader,
  createCodingTools,
  createBashTool,
  type AgentSessionEvent,
} from "@mariozechner/pi-coding-agent";
import { getModel, type Model, type ThinkingLevel as PiThinkingLevel } from "@mariozechner/pi-ai";
import type { LLMSettings, ExecuteResult, ProxyConfig, ThinkingLevel, ToolAllowlistEntry } from "./types.js";
import {
  formatToolArgs,
  formatToolOutput,
  extractToolText,
  type ToolExecutionTrace,
} from "./tool-output-formatter.js";
import { GatewayClient } from "./gateway-client.js";
import { createGatewayCallTool, createSearchToolsTool, createGetToolDetailTool, createListToolTypesTool, createExecuteScriptTool } from "./gateway-tools.js";

// ---------------------------------------------------------------------------
// Tool calling guidelines attached to the bash tool via promptGuidelines
// ---------------------------------------------------------------------------

/**
 * Attached to the bash tool via `promptGuidelines` (pi-mono 0.55.4+) so it
 * appears in the system prompt's Guidelines section automatically when the
 * bash tool is active. If the tool is ever disabled, the guidelines disappear
 * from the prompt automatically.
 */
const BASH_TOOL_GUIDELINES = `\
- Use the gateway_call tool to invoke tools. It communicates directly with the MCP Gateway and does not require bash.
- For batch operations across multiple hosts or complex data processing, use the execute_script tool. It runs JavaScript with built-in gateway_call(), search_tools(), get_tool_detail(), and synchronous fs (readFileSync, writeFileSync). Do NOT use require() or import() in scripts.
- Use list_tool_types to see available tool types, then search_tools to find specific tools, and get_tool_detail to see full parameter schemas.
- IMPORTANT: Each skill's SKILL.md lists assigned tools with their logical names and the exact call forms available. Read the SKILL.md first, then call tools using only the forms shown there. Do NOT explore the filesystem to discover tools.`;

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
  /** Tool instances the incident is authorized to use (nil = allow all for backward compat) */
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
  /** Tool instances the incident is authorized to use (nil = allow all for backward compat) */
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
  switch (level) {
    case "off":
      return "minimal";
    case "minimal":
      return "minimal";
    case "low":
      return "low";
    case "medium":
      return "medium";
    case "high":
      return "high";
    case "xhigh":
      return "xhigh";
    default:
      return "medium";
  }
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
    const sessionManager = isResume
      ? SessionManager.continueRecent(params.workDir)
      : SessionManager.create(params.workDir);
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

    // Create bash tool with spawnHook to inject MCP Gateway env vars per-session.
    // Attach promptGuidelines (pi-mono 0.55.4+) so infrastructure tool usage
    // instructions appear in the system prompt's Guidelines section automatically.
    const bashTool = createBashTool(params.workDir, {
      spawnHook: (ctx) => ({
        ...ctx,
        env: {
          ...ctx.env,
          MCP_GATEWAY_URL: this.mcpGatewayUrl,
          INCIDENT_ID: params.incidentId,
        },
      }),
    });
    (bashTool as any).promptGuidelines = BASH_TOOL_GUIDELINES;

    // Create coding tools with the same workDir, then replace the default bash tool
    const codingTools = createCodingTools(params.workDir);
    const tools = codingTools.map((t) =>
      t.name === "bash" ? bashTool : t,
    );

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
    const searchToolsTool = createSearchToolsTool(gatewayToolCtx);
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
      tools,
      customTools: [gatewayCallTool, searchToolsTool, getToolDetailTool, listToolTypesTool, executeScriptTool],
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

      // If the SDK reported an API-level error, propagate it
      if (lastErrorMessage && !responseText) {
        return {
          session_id: session.sessionId,
          response: responseText,
          full_log: fullLog,
          error: lastErrorMessage,
          tokens_used: totalTokens,
          execution_time_ms: Date.now() - startTime,
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
      };
    } catch (err) {
      return {
        session_id: session.sessionId,
        response: responseText,
        full_log: fullLog,
        error: (err as Error).message,
        tokens_used: totalTokens,
        execution_time_ms: Date.now() - startTime,
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

      case "auto_compaction_start": {
        const reason = (event as any).reason ?? "context limit";
        const compactLine = `\n📦 Compacting context (${reason})...\n`;
        onOutput(compactLine);
        onLogText(compactLine);
        break;
      }

      case "auto_compaction_end": {
        const aborted = (event as any).aborted;
        const compactResult = aborted
          ? "\n📦 Context compaction aborted\n"
          : "\n📦 Context compaction complete\n";
        onOutput(compactResult);
        onLogText(compactResult);
        break;
      }

      case "auto_retry_start": {
        const retryEvent = event as any;
        const retryLine = `\n🔄 Retrying (attempt ${retryEvent.attempt ?? "?"}/${retryEvent.maxAttempts ?? "?"}): ${retryEvent.errorMessage ?? "unknown error"}\n`;
        onOutput(retryLine);
        onLogText(retryLine);
        break;
      }

      case "auto_retry_end": {
        const retryEndEvent = event as any;
        if (!retryEndEvent.success) {
          const failLine = `\n🔄 All retries exhausted: ${retryEndEvent.finalError ?? "unknown error"}\n`;
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
   * Apply proxy configuration to environment variables.
   * Only sets proxy for LLM API calls when the relevant toggle is enabled.
   *
   * Note: process.env is global state shared by concurrent sessions. We use
   * assignment (not delete) to avoid inconsistent intermediate states. In
   * practice, proxy config is system-global so all sessions receive the same
   * setting.
   */
  private applyProxyConfig(
    proxyConfig: ProxyConfig | undefined,
    provider: string,
  ): void {
    if (proxyConfig?.url && proxyConfig.openai_enabled) {
      process.env.HTTP_PROXY = proxyConfig.url;
      process.env.HTTPS_PROXY = proxyConfig.url;
      process.env.NO_PROXY = proxyConfig.no_proxy || "";
    } else {
      process.env.HTTP_PROXY = "";
      process.env.HTTPS_PROXY = "";
      process.env.NO_PROXY = "";
    }
  }
}
