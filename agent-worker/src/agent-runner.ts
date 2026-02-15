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
  createCodingTools,
  type AgentSessionEvent,
} from "@mariozechner/pi-coding-agent";
import { getModel, type Model, type ThinkingLevel as PiThinkingLevel } from "@mariozechner/pi-ai";
import type { LLMSettings, ExecuteResult, ProxyConfig, ThinkingLevel } from "./types.js";
import { createMCPTools } from "./tools/mcp-tools.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ExecuteParams {
  incidentId: string;
  task: string;
  llmSettings: LLMSettings;
  proxyConfig?: ProxyConfig;
  workDir: string;
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
  onOutput: (text: string) => void;
  onEvent?: (event: AgentSessionEvent) => void;
}

export interface AgentRunnerConfig {
  mcpGatewayUrl: string;
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
    return getModel(provider as any, modelId as any);
  } catch {
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
}

// ---------------------------------------------------------------------------
// AgentRunner
// ---------------------------------------------------------------------------

export class AgentRunner {
  private readonly mcpGatewayUrl: string;
  private activeSessions = new Map<string, AgentSession>();

  constructor(config: AgentRunnerConfig) {
    this.mcpGatewayUrl = config.mcpGatewayUrl;
  }

  /**
   * Execute a new agent session for an incident.
   */
  async execute(params: ExecuteParams): Promise<ExecuteResult> {
    return this.runSession(params, params.task);
  }

  /**
   * Resume an existing session with a follow-up message.
   */
  async resume(params: ResumeParams): Promise<ExecuteResult> {
    return this.runSession(params, params.message);
  }

  /**
   * Common session setup and execution logic shared by execute() and resume().
   */
  private async runSession(
    params: ExecuteParams | ResumeParams,
    promptText: string,
  ): Promise<ExecuteResult> {
    const startTime = Date.now();

    // Set up proxy env vars before creating session
    this.applyProxyConfig(params.proxyConfig, params.llmSettings.provider);

    // Auth
    const authStorage = new AuthStorage();
    authStorage.setRuntimeApiKey(params.llmSettings.provider, params.llmSettings.api_key);

    // Model
    const model = resolveModel(
      params.llmSettings.provider,
      params.llmSettings.model,
      params.llmSettings.base_url,
    );
    const thinkingLevel = mapThinkingLevel(params.llmSettings.thinking_level);

    // Tools
    const mcpTools = createMCPTools(this.mcpGatewayUrl, params.incidentId);

    // Session management (in-memory since we don't need persistent sessions across restarts)
    const sessionManager = SessionManager.inMemory(params.workDir);
    const settingsManager = SettingsManager.inMemory();
    const modelRegistry = new ModelRegistry(authStorage);

    const { session } = await createAgentSession({
      cwd: params.workDir,
      authStorage,
      modelRegistry,
      model,
      thinkingLevel,
      tools: createCodingTools(params.workDir),
      customTools: mcpTools,
      sessionManager,
      settingsManager,
    });

    this.activeSessions.set(params.incidentId, session);

    // Accumulate response and token usage
    let responseText = "";
    let fullLog = "";
    let totalTokens = 0;

    const unsubscribe = session.subscribe((event: AgentSessionEvent) => {
      params.onEvent?.(event);
      this.handleEvent(event, params.onOutput, (text) => {
        responseText += text;
        fullLog += text;
      }, (text) => {
        fullLog += text;
      }, (tokens) => {
        totalTokens += tokens;
      });
    });

    try {
      await session.prompt(promptText);

      // Extract final response text if we didn't accumulate any
      if (!responseText) {
        responseText = session.getLastAssistantText() ?? "";
      }

      return {
        session_id: session.sessionId,
        response: responseText,
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
  ): void {
    switch (event.type) {
      case "message_update": {
        const assistantEvent = event.assistantMessageEvent;
        if (assistantEvent.type === "text_delta") {
          onOutput(assistantEvent.delta);
          onResponseText(assistantEvent.delta);
        }
        break;
      }

      case "tool_execution_start": {
        const toolLine = `\n[Tool: ${event.toolName}]\n`;
        onOutput(toolLine);
        onLogText(toolLine);
        break;
      }

      case "tool_execution_end": {
        const resultSummary = event.isError
          ? `\n[Tool Error: ${event.toolName}]\n`
          : `\n[Tool Complete: ${event.toolName}]\n`;
        onOutput(resultSummary);
        onLogText(resultSummary);
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

      default:
        // Other events (agent_start, agent_end, turn_start, etc.) - no output needed
        break;
    }
  }

  /**
   * Apply proxy configuration to environment variables.
   * Only sets proxy for LLM API calls when the relevant toggle is enabled.
   */
  private applyProxyConfig(
    proxyConfig: ProxyConfig | undefined,
    provider: string,
  ): void {
    // Clear existing proxy settings first
    delete process.env.HTTP_PROXY;
    delete process.env.HTTPS_PROXY;
    delete process.env.NO_PROXY;

    if (!proxyConfig?.url) return;

    // Only apply proxy for providers that use the "openai_enabled" toggle
    // (historically this was OpenAI-only, but now covers all LLM providers)
    if (proxyConfig.openai_enabled) {
      process.env.HTTP_PROXY = proxyConfig.url;
      process.env.HTTPS_PROXY = proxyConfig.url;

      if (proxyConfig.no_proxy) {
        process.env.NO_PROXY = proxyConfig.no_proxy;
      }
    }
  }
}
