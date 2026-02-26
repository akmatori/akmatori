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
import type { LLMSettings, ExecuteResult, ProxyConfig, ThinkingLevel } from "./types.js";

// ---------------------------------------------------------------------------
// Tool calling instructions appended to the system prompt
// ---------------------------------------------------------------------------

/**
 * Injected via `appendSystemPrompt` on DefaultResourceLoader so the agent
 * knows HOW to call tools without wasting turns exploring the filesystem.
 *
 * Key points:
 * - Python wrappers live at /tools/ (PYTHONPATH is pre-set by spawnHook)
 * - The calling pattern is `python3 -c "from <module> import <func>; ..."`
 * - SKILL.md files contain the `tool_instance_id` values to pass
 * - SSH read-only mode constraints and allowed diagnostic commands
 * - Zabbix query tips
 */
const TOOL_CALLING_INSTRUCTIONS = `
## Tool Calling Instructions

You have access to infrastructure tools via Python wrappers. The environment is already
configured — \`PYTHONPATH=/tools\` is set, so you can import directly.

### Calling Pattern

Use the bash tool with \`python3 -c\` one-liners:

\`\`\`bash
python3 -c "from ssh import execute_command; print(execute_command('uptime', tool_instance_id=<ID>))"
python3 -c "from ssh import test_connectivity; print(test_connectivity(tool_instance_id=<ID>))"
python3 -c "from ssh import get_server_info; print(get_server_info(tool_instance_id=<ID>))"
python3 -c "from zabbix import get_problems; print(get_problems(severity_min=3, tool_instance_id=<ID>))"
python3 -c "from zabbix import get_hosts; print(get_hosts(tool_instance_id=<ID>))"
python3 -c "from zabbix import get_items_batch; print(get_items_batch(searches=['cpu','memory'], hostids=['12345'], tool_instance_id=<ID>))"
python3 -c "from zabbix import get_history; print(get_history(itemids=['67890'], limit=10, tool_instance_id=<ID>))"
python3 -c "from zabbix import get_triggers; print(get_triggers(hostids=['12345'], only_true=True, tool_instance_id=<ID>))"
\`\`\`

### How to Find tool_instance_id Values

Each skill's SKILL.md lists assigned tools with their \`tool_instance_id\`. **Read the SKILL.md
first**, then call tools directly. Do NOT explore the filesystem to discover tools.

### Efficient Workflow

1. Read the relevant SKILL.md to learn which tool instances are available and their IDs
2. Call tools directly using the pattern above
3. Do NOT search for Python files, read source code, or trial-and-error imports
4. If you get \`ModuleNotFoundError\`, prefix the command with \`PYTHONPATH=/tools\`

### SSH Tips

- Most servers are in **read-only mode** by default — only diagnostic commands are allowed
- Allowed commands include: cat, head, tail, grep, find, ls, ps, top, df, free, netstat, ss,
  uptime, vmstat, iostat, mpstat, sar, pidstat, journalctl, dmesg, docker ps/logs, systemctl status,
  nproc, lscpu, getconf, and more
- For CPU core count, use \`nproc\` or \`lscpu\` (not \`/proc/cpuinfo\` parsing)
- Use the \`servers\` parameter to target specific hosts: \`execute_command("uptime", servers=["hostname"], tool_instance_id=<ID>)\`

### Zabbix Tips

- Zabbix API calls can be slow — prefer \`get_items_batch\` over multiple \`get_items\` calls
- Use \`hostids\` filter to scope queries to specific hosts
- Severity levels: 0=Not classified, 1=Information, 2=Warning, 3=Average, 4=High, 5=Disaster
- Use \`severity_min=3\` to filter out low-priority noise
`;

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
  onOutput: (text: string) => void;
  onEvent?: (event: AgentSessionEvent) => void;
}

export interface AgentRunnerConfig {
  mcpGatewayUrl: string;
  /** Directory containing SKILL.md definitions for pi-mono resource loader */
  skillsDir?: string;
}

interface ToolExecutionTrace {
  toolName: string;
  args: unknown;
  updates: string[];
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
    const authStorage = new AuthStorage();
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
    const sessionManager = isResume
      ? SessionManager.continueRecent(params.workDir)
      : SessionManager.create(params.workDir);
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
      appendSystemPrompt: TOOL_CALLING_INSTRUCTIONS,
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

    // Create bash tool with spawnHook to inject MCP Gateway env vars per-session
    const bashTool = createBashTool(params.workDir, {
      spawnHook: (ctx) => ({
        ...ctx,
        env: {
          ...ctx.env,
          MCP_GATEWAY_URL: this.mcpGatewayUrl,
          INCIDENT_ID: params.incidentId,
          PYTHONPATH: ctx.env.PYTHONPATH ? `/tools:${ctx.env.PYTHONPATH}` : "/tools",
        },
      }),
    });

    // Create coding tools with the same workDir, then replace the default bash tool
    const codingTools = createCodingTools(params.workDir);
    const tools = codingTools.map((t) =>
      t.name === "bash" ? bashTool : t,
    );

    const { session } = await createAgentSession({
      cwd: params.workDir,
      authStorage,
      modelRegistry,
      model,
      thinkingLevel,
      tools,
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
        const updateText = this.extractToolText(event.partialResult);
        if (updateText) {
          trace.updates.push(updateText);
        }
        toolTraces.set(event.toolCallId, trace);
        break;
      }

      case "tool_execution_end": {
        const trace = toolTraces.get(event.toolCallId);
        const status = event.isError ? "❌ Failed:" : "✅ Ran:";
        const argsText = this.formatToolArgs(trace?.args);
        const outputText = this.formatToolOutput(trace?.updates ?? [], event.result);

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

      default:
        // Other events (agent_start, agent_end, turn_start, etc.) - no output needed
        break;
    }
  }

  private formatToolArgs(args: unknown): string {
    if (args === null || args === undefined) return "";
    if (typeof args === "string") return args.trim();

    if (Array.isArray(args)) {
      if (args.length === 0) return "";
      return this.safeJSONStringify(args);
    }

    if (typeof args === "object") {
      if (Object.keys(args as Record<string, unknown>).length === 0) return "";
      return this.safeJSONStringify(args);
    }

    return String(args);
  }

  private formatToolOutput(updates: string[], result: unknown): string {
    const parts: string[] = [];

    for (const update of updates) {
      const trimmed = update.trim();
      if (trimmed) parts.push(trimmed);
    }

    const resultText = this.extractToolText(result).trim();
    if (resultText) {
      parts.push(resultText);
    } else if (result && typeof result === "object") {
      if (Array.isArray(result)) {
        if (result.length > 0) parts.push(this.safeJSONStringify(result));
      } else if (Object.keys(result as Record<string, unknown>).length > 0) {
        parts.push(this.safeJSONStringify(result));
      }
    }

    const deduped: string[] = [];
    for (const part of parts) {
      if (deduped[deduped.length - 1] !== part) {
        deduped.push(part);
      }
    }

    return deduped.join("\n");
  }

  private extractToolText(value: unknown): string {
    const parts = this.collectTextParts(value).map((part) => part.trim()).filter(Boolean);
    return parts.join("\n");
  }

  private collectTextParts(value: unknown): string[] {
    if (value === null || value === undefined) return [];
    if (typeof value === "string") return [value];
    if (Array.isArray(value)) return value.flatMap((entry) => this.collectTextParts(entry));
    if (typeof value !== "object") return [];

    const obj = value as Record<string, unknown>;
    const parts: string[] = [];

    if (typeof obj.text === "string") parts.push(obj.text);
    if (typeof obj.output === "string") parts.push(obj.output);
    if (typeof obj.message === "string") parts.push(obj.message);
    if (typeof obj.error === "string") parts.push(obj.error);
    if (typeof obj.stderr === "string") parts.push(obj.stderr);

    if (obj.content !== undefined) {
      parts.push(...this.collectContentText(obj.content));
    }
    if (obj.partialResult !== undefined) {
      parts.push(...this.collectTextParts(obj.partialResult));
    }
    if (obj.result !== undefined) {
      parts.push(...this.collectTextParts(obj.result));
    }
    if (obj.details !== undefined) {
      parts.push(...this.collectTextParts(obj.details));
    }

    return parts;
  }

  private collectContentText(content: unknown): string[] {
    if (!Array.isArray(content)) {
      return this.collectTextParts(content);
    }

    const parts: string[] = [];
    for (const item of content) {
      if (!item || typeof item !== "object") {
        if (typeof item === "string") parts.push(item);
        continue;
      }

      const contentItem = item as Record<string, unknown>;
      if (contentItem.type === "text" && typeof contentItem.text === "string") {
        parts.push(contentItem.text);
        continue;
      }
      if (contentItem.type === "thinking" && typeof contentItem.thinking === "string") {
        parts.push(contentItem.thinking);
        continue;
      }

      parts.push(...this.collectTextParts(contentItem));
    }

    return parts;
  }

  private safeJSONStringify(value: unknown): string {
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      return String(value);
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
