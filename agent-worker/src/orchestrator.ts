/**
 * Orchestrator - message router between the API WebSocket and the AgentRunner.
 *
 * Routes incoming WebSocket messages to the appropriate AgentRunner
 * methods and streams output/completion/errors back through the WebSocket client.
 */

import { WebSocketClient } from "./ws-client.js";
import { AgentRunner, type ExecuteParams, type ResumeParams } from "./agent-runner.js";
import { runOneshotLLM } from "./oneshot-llm.js";
import type {
  WebSocketMessage,
  LLMSettings,
  ProxyConfig,
  ExecuteResult,
  ToolAllowlistEntry,
} from "./types.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface OrchestratorConfig {
  /** WebSocket URL of the API server (e.g. "ws://akmatori-api:3000/ws/agent") */
  apiWsUrl: string;
  /** MCP Gateway base URL (e.g. "http://mcp-gateway:8080") */
  mcpGatewayUrl: string;
  /** Base directory for incident workspaces */
  workspaceDir: string;
  /** Directory containing SKILL.md definitions for pi-mono resource loader */
  skillsDir?: string;
  /** Logger function */
  logger?: (msg: string) => void;
}

// ---------------------------------------------------------------------------
// Orchestrator
// ---------------------------------------------------------------------------

export class Orchestrator {
  private readonly config: OrchestratorConfig;
  private readonly wsClient: WebSocketClient;
  private readonly runner: AgentRunner;
  private readonly log: (msg: string) => void;
  private cachedProxyConfig: ProxyConfig | undefined;
  private stopped = false;
  /**
   * Per-incident launch chain. Each launch installs a deferred promise that
   * resolves when its session has been registered in activeSessions (or the
   * launch failed before getting there). Subsequent launches await the prior
   * promise before running abortInFlightSession + their own bootstrap. This
   * closes the bootstrap-window race where two near-simultaneous handlers
   * would both see an empty activeSessions during each other's bootstrap and
   * start in parallel, plus the queued-cancel race where two handlers awaiting
   * the same session.abort() would both proceed concurrently afterwards. Only
   * the bootstrap is serialized — the long-running session.prompt phase runs
   * concurrently with subsequent launches' chain waits.
   */
  private launchChain = new Map<string, Promise<void>>();

  constructor(config: OrchestratorConfig) {
    this.config = config;
    this.log = config.logger ?? ((msg: string) => console.log(`[orchestrator] ${msg}`));

    this.wsClient = new WebSocketClient({
      url: config.apiWsUrl,
      logger: this.log,
    });

    this.runner = new AgentRunner({
      mcpGatewayUrl: config.mcpGatewayUrl,
      skillsDir: config.skillsDir,
    });
  }

  /**
   * Start the orchestrator: connect WebSocket, register handler, send ready.
   */
  async start(): Promise<void> {
    this.stopped = false;

    this.wsClient.onMessage((msg) => this.handleMessage(msg));

    await this.wsClient.connect();

    // Send initial "ready" status
    this.wsClient.send({
      type: "status",
      data: { status: "ready" },
    });

    this.log("Orchestrator started");
  }

  /**
   * Stop the orchestrator: cancel active runs, close WebSocket.
   */
  async stop(): Promise<void> {
    this.log("Stopping orchestrator...");
    this.stopped = true;
    await this.runner.dispose();
    this.wsClient.close();
    this.log("Orchestrator stopped");
  }

  /**
   * Whether the WebSocket is currently connected.
   */
  isConnected(): boolean {
    return this.wsClient.isConnected();
  }

  /**
   * Whether the orchestrator has been stopped.
   */
  isStopped(): boolean {
    return this.stopped;
  }

  /**
   * Get the underlying WebSocket client (for testing).
   */
  getWsClient(): WebSocketClient {
    return this.wsClient;
  }

  /**
   * Get the underlying agent runner (for testing).
   */
  getRunner(): AgentRunner {
    return this.runner;
  }

  // -------------------------------------------------------------------------
  // Message routing
  // -------------------------------------------------------------------------

  /** Handle an incoming WebSocket message from the API. */
  private handleMessage(msg: WebSocketMessage): void {
    this.log(`Received message: type=${msg.type} incident=${msg.incident_id ?? "N/A"}`);

    switch (msg.type) {
      case "new_incident":
        this.handleNewIncident(msg);
        break;

      case "continue_incident":
        this.handleContinueIncident(msg);
        break;

      case "cancel_incident":
        this.handleCancelIncident(msg);
        break;

      case "proxy_config_update":
        this.handleProxyConfigUpdate(msg);
        break;

      case "oneshot_llm_request":
        this.handleOneshotLLM(msg);
        break;

      default:
        this.log(`Unknown message type: ${msg.type}`);
    }
  }

  // -------------------------------------------------------------------------
  // Message handlers
  // -------------------------------------------------------------------------

  private handleNewIncident(msg: WebSocketMessage): void {
    const incidentId = msg.incident_id;
    if (!incidentId) {
      this.log("new_incident missing incident_id, ignoring");
      return;
    }

    if (!this.isValidIncidentId(incidentId)) {
      this.log(`new_incident has invalid incident_id: ${incidentId}, ignoring`);
      return;
    }

    const llmSettings = this.extractLLMSettings(msg);
    if (!llmSettings) {
      this.wsClient.sendError(incidentId, msg.run_id, "Missing LLM settings (no API key or provider)");
      return;
    }

    const proxyConfig = msg.proxy_config ?? this.cachedProxyConfig;
    const runId = msg.run_id;

    const { resolveRegistered, prevLaunch, registered } = this.enterLaunchChain(incidentId);

    const params: ExecuteParams = {
      incidentId,
      task: msg.task ?? "",
      llmSettings,
      proxyConfig,
      enabledSkills: msg.enabled_skills,
      toolAllowlist: msg.tool_allowlist,
      workDir: `${this.config.workspaceDir}/${incidentId}`,
      onOutput: (text: string) => {
        this.wsClient.sendOutput(incidentId, runId, text);
      },
      onRegistered: resolveRegistered,
    };

    // Serialize bootstrap per-incident: wait until the prior launch has
    // registered its session (or failed) before running our own
    // abortInFlightSession + bootstrap. Without this gate, a second
    // new_incident arriving during the first's bootstrap window would see
    // an empty activeSessions and start in parallel; the API separately
    // drops late frames by run_id, but parallel sessions still share the
    // workDir for tool outputs.
    void (async () => {
      try { await prevLaunch; } catch { /* prior launch's failure is its own concern */ }
      await this.abortInFlightSession(incidentId, "new_incident");
      try {
        await this.runExecution(incidentId, runId, params);
      } catch (err) {
        this.log(`Unhandled error in runExecution for ${incidentId}: ${err}`);
      } finally {
        // Belt-and-suspenders: if bootstrap failed before activeSessions.set
        // (createAgentSession threw, etc.) onRegistered was never called.
        // Resolve the chain here so the next launch isn't deadlocked.
        resolveRegistered();
        if (this.launchChain.get(incidentId) === registered) {
          this.launchChain.delete(incidentId);
        }
      }
    })();
  }

  private handleContinueIncident(msg: WebSocketMessage): void {
    const incidentId = msg.incident_id;
    if (!incidentId) {
      this.log("continue_incident missing incident_id, ignoring");
      return;
    }

    if (!this.isValidIncidentId(incidentId)) {
      this.log(`continue_incident has invalid incident_id: ${incidentId}, ignoring`);
      return;
    }

    const llmSettings = this.extractLLMSettings(msg);
    if (!llmSettings) {
      this.wsClient.sendError(incidentId, msg.run_id, "Missing LLM settings (no API key or provider)");
      return;
    }

    const proxyConfig = msg.proxy_config ?? this.cachedProxyConfig;
    const runId = msg.run_id;

    const { resolveRegistered, prevLaunch, registered } = this.enterLaunchChain(incidentId);

    const params: ResumeParams = {
      incidentId,
      sessionId: msg.session_id ?? "",
      message: msg.message ?? "",
      llmSettings,
      proxyConfig,
      enabledSkills: msg.enabled_skills,
      toolAllowlist: msg.tool_allowlist,
      workDir: `${this.config.workspaceDir}/${incidentId}`,
      onOutput: (text: string) => {
        this.wsClient.sendOutput(incidentId, runId, text);
      },
      onRegistered: resolveRegistered,
    };

    void (async () => {
      try { await prevLaunch; } catch { /* prior launch's failure is its own concern */ }
      await this.abortInFlightSession(incidentId, "continue_incident");
      try {
        await this.runResume(incidentId, runId, params);
      } catch (err) {
        this.log(`Unhandled error in runResume for ${incidentId}: ${err}`);
      } finally {
        resolveRegistered();
        if (this.launchChain.get(incidentId) === registered) {
          this.launchChain.delete(incidentId);
        }
      }
    })();
  }

  private handleCancelIncident(msg: WebSocketMessage): void {
    const incidentId = msg.incident_id;
    if (!incidentId) {
      this.log("cancel_incident missing incident_id, ignoring");
      return;
    }

    this.log(`Cancelling incident: ${incidentId}`);
    this.runner.cancel(incidentId).then(() => {
      this.wsClient.sendError(incidentId, msg.run_id, "Execution cancelled");
    }).catch((err) => {
      this.log(`Failed to cancel incident ${incidentId}: ${err}`);
    });
  }

  /**
   * Install a new entry on the per-incident launch chain. Returns the
   * previous entry (which the caller must await before its own
   * abort+bootstrap), the resolver to release the chain when the new
   * session registers, and the registered promise itself (so the caller
   * can compare-and-delete on cleanup).
   */
  private enterLaunchChain(incidentId: string): {
    prevLaunch: Promise<void>;
    resolveRegistered: () => void;
    registered: Promise<void>;
  } {
    const prevLaunch = this.launchChain.get(incidentId) ?? Promise.resolve();
    let resolveRegistered!: () => void;
    const registered = new Promise<void>((resolve) => {
      resolveRegistered = resolve;
    });
    this.launchChain.set(incidentId, registered);
    return { prevLaunch, resolveRegistered, registered };
  }

  /**
   * Abort any in-flight session for incidentId and resolve only after the
   * abort has propagated. Callers await this before starting a new run so
   * the prior session's tool calls and workspace writes finish unwinding
   * before the replacement begins. Errors are logged but never propagated —
   * the new run must start regardless of whether the old session aborted
   * cleanly.
   */
  private async abortInFlightSession(incidentId: string, reason: string): Promise<void> {
    if (!this.runner.hasActiveSession(incidentId)) return;
    this.log(`Aborting in-flight session for ${incidentId} before ${reason}`);
    try {
      await this.runner.cancel(incidentId);
    } catch (err) {
      this.log(`Failed to abort prior session for ${incidentId}: ${err}`);
    }
  }

  private handleProxyConfigUpdate(msg: WebSocketMessage): void {
    if (msg.proxy_config) {
      this.cachedProxyConfig = msg.proxy_config;
      this.log("Proxy configuration updated");
    }
  }

  private handleOneshotLLM(msg: WebSocketMessage): void {
    const requestId = msg.request_id;
    if (!requestId) {
      this.log("oneshot_llm_request missing request_id, ignoring");
      return;
    }

    const respond = (summary: string, errorMsg?: string): void => {
      this.wsClient.sendOneshotResponse(requestId, summary, errorMsg);
    };

    const llmSettings = this.extractLLMSettings(msg);
    if (!llmSettings) {
      respond("", "Missing LLM settings (no API key or provider)");
      return;
    }

    if (!msg.user) {
      respond("", "Missing user prompt");
      return;
    }

    const proxyConfig = msg.proxy_config ?? this.cachedProxyConfig;

    runOneshotLLM({
      requestId,
      system: msg.system,
      user: msg.user,
      maxTokens: msg.max_tokens,
      temperature: msg.temperature,
      llmSettings,
      proxyConfig,
    })
      .then((summary) => {
        respond(summary);
      })
      .catch((err: unknown) => {
        const errMsg = (err as Error)?.message ?? String(err);
        this.log(`oneshot_llm_request ${requestId} failed: ${errMsg}`);
        respond("", errMsg);
      });
  }

  // -------------------------------------------------------------------------
  // Validation helpers
  // -------------------------------------------------------------------------

  /** Validate incident ID contains only safe characters (prevents path traversal). */
  private isValidIncidentId(id: string): boolean {
    return /^[a-zA-Z0-9_-]+$/.test(id);
  }

  // -------------------------------------------------------------------------
  // Async execution helpers
  // -------------------------------------------------------------------------

  private async runExecution(incidentId: string, runId: string | undefined, params: ExecuteParams): Promise<void> {
    return this.runWithResultHandling("Starting", incidentId, runId, () => this.runner.execute(params));
  }

  private async runResume(incidentId: string, runId: string | undefined, params: ResumeParams): Promise<void> {
    return this.runWithResultHandling("Continuing", incidentId, runId, () => this.runner.resume(params));
  }

  private async runWithResultHandling(
    label: string,
    incidentId: string,
    runId: string | undefined,
    fn: () => Promise<ExecuteResult>,
  ): Promise<void> {
    this.log(`${label} incident: ${incidentId}`);

    try {
      const result = await fn();

      if (result.error) {
        this.log(`Incident ${incidentId} completed with error: ${result.error}`);
        this.wsClient.sendError(incidentId, runId, result.error);
        return;
      }

      this.wsClient.sendCompleted(
        incidentId,
        runId,
        result.session_id,
        result.response,
        result.tokens_used,
        result.execution_time_ms,
      );

      this.log(
        `Incident ${incidentId} completed (tokens: ${result.tokens_used}, time: ${result.execution_time_ms}ms)`,
      );
    } catch (err) {
      const errorMsg = (err as Error).message ?? String(err);
      this.log(`Incident ${incidentId} failed: ${errorMsg}`);
      this.wsClient.sendError(incidentId, runId, errorMsg);
    }
  }

  // -------------------------------------------------------------------------
  // Settings extraction
  // -------------------------------------------------------------------------

  /**
   * Extract LLM settings from a WebSocket message.
   *
   * The Go API sends provider, api_key, model, thinking_level, and base_url fields.
   */
  private extractLLMSettings(msg: WebSocketMessage): LLMSettings | null {
    const apiKey = msg.api_key;
    if (!apiKey) return null;

    return {
      provider: (msg.provider as LLMSettings["provider"]) ?? "openai",
      api_key: apiKey,
      model: msg.model ?? "gpt-5.5",
      thinking_level: this.mapThinkingLevel(msg.thinking_level),
      base_url: msg.base_url,
    };
  }

  /**
   * Map thinking level string to our ThinkingLevel type.
   */
  private mapThinkingLevel(level: string | undefined): LLMSettings["thinking_level"] {
    switch (level) {
      case "off":
        return "off";
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
}
