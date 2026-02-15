/**
 * Orchestrator - message router between the API WebSocket and the AgentRunner.
 *
 * Ports the Go codex-worker orchestrator (codex-worker/internal/orchestrator/orchestrator.go)
 * to TypeScript. Routes incoming WebSocket messages to the appropriate AgentRunner
 * methods and streams output/completion/errors back through the WebSocket client.
 */

import { WebSocketClient, type WebSocketClientOptions } from "./ws-client.js";
import { AgentRunner, type ExecuteParams, type ResumeParams } from "./agent-runner.js";
import type {
  WebSocketMessage,
  LLMSettings,
  ProxyConfig,
} from "./types.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface OrchestratorConfig {
  /** WebSocket URL of the API server (e.g. "ws://akmatori-api:3000/ws/codex") */
  apiWsUrl: string;
  /** MCP Gateway base URL (e.g. "http://mcp-gateway:8080") */
  mcpGatewayUrl: string;
  /** Base directory for incident workspaces */
  workspaceDir: string;
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

  constructor(config: OrchestratorConfig) {
    this.config = config;
    this.log = config.logger ?? ((msg: string) => console.log(`[orchestrator] ${msg}`));

    this.wsClient = new WebSocketClient({
      url: config.apiWsUrl,
      logger: this.log,
    });

    this.runner = new AgentRunner({
      mcpGatewayUrl: config.mcpGatewayUrl,
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

    const llmSettings = this.extractLLMSettings(msg);
    if (!llmSettings) {
      this.wsClient.sendError(incidentId, "Missing LLM settings (no API key or provider)");
      return;
    }

    const proxyConfig = msg.proxy_config ?? this.cachedProxyConfig;

    const params: ExecuteParams = {
      incidentId,
      task: msg.task ?? "",
      llmSettings,
      proxyConfig,
      workDir: `${this.config.workspaceDir}/${incidentId}`,
      onOutput: (text: string) => {
        this.wsClient.sendOutput(incidentId, text);
      },
    };

    // Run asynchronously (like Go's goroutine)
    this.runExecution(incidentId, params);
  }

  private handleContinueIncident(msg: WebSocketMessage): void {
    const incidentId = msg.incident_id;
    if (!incidentId) {
      this.log("continue_incident missing incident_id, ignoring");
      return;
    }

    const llmSettings = this.extractLLMSettings(msg);
    if (!llmSettings) {
      this.wsClient.sendError(incidentId, "Missing LLM settings (no API key or provider)");
      return;
    }

    const proxyConfig = msg.proxy_config ?? this.cachedProxyConfig;

    const params: ResumeParams = {
      incidentId,
      sessionId: msg.session_id ?? "",
      message: msg.message ?? "",
      llmSettings,
      proxyConfig,
      workDir: `${this.config.workspaceDir}/${incidentId}`,
      onOutput: (text: string) => {
        this.wsClient.sendOutput(incidentId, text);
      },
    };

    // Run asynchronously
    this.runResume(incidentId, params);
  }

  private handleCancelIncident(msg: WebSocketMessage): void {
    const incidentId = msg.incident_id;
    if (!incidentId) {
      this.log("cancel_incident missing incident_id, ignoring");
      return;
    }

    this.log(`Cancelling incident: ${incidentId}`);
    this.runner.cancel(incidentId).then(() => {
      this.wsClient.sendError(incidentId, "Execution cancelled");
    }).catch((err) => {
      this.log(`Failed to cancel incident ${incidentId}: ${err}`);
    });
  }

  private handleProxyConfigUpdate(msg: WebSocketMessage): void {
    if (msg.proxy_config) {
      this.cachedProxyConfig = msg.proxy_config;
      this.log("Proxy configuration updated");
    }
  }

  // -------------------------------------------------------------------------
  // Async execution helpers
  // -------------------------------------------------------------------------

  private async runExecution(incidentId: string, params: ExecuteParams): Promise<void> {
    this.log(`Starting new incident: ${incidentId}`);

    try {
      const result = await this.runner.execute(params);

      if (result.error) {
        this.log(`Incident ${incidentId} completed with error: ${result.error}`);
        this.wsClient.sendError(incidentId, result.error);
        return;
      }

      this.wsClient.sendCompleted(
        incidentId,
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
      this.wsClient.sendError(incidentId, errorMsg);
    }
  }

  private async runResume(incidentId: string, params: ResumeParams): Promise<void> {
    this.log(`Continuing incident: ${incidentId}`);

    try {
      const result = await this.runner.resume(params);

      if (result.error) {
        this.log(`Continue incident ${incidentId} completed with error: ${result.error}`);
        this.wsClient.sendError(incidentId, result.error);
        return;
      }

      this.wsClient.sendCompleted(
        incidentId,
        result.session_id,
        result.response,
        result.tokens_used,
        result.execution_time_ms,
      );

      this.log(
        `Continue incident ${incidentId} completed (tokens: ${result.tokens_used}, time: ${result.execution_time_ms}ms)`,
      );
    } catch (err) {
      const errorMsg = (err as Error).message ?? String(err);
      this.log(`Continue incident ${incidentId} failed: ${errorMsg}`);
      this.wsClient.sendError(incidentId, errorMsg);
    }
  }

  // -------------------------------------------------------------------------
  // Settings extraction
  // -------------------------------------------------------------------------

  /**
   * Extract LLM settings from a WebSocket message.
   *
   * The current Go API sends fields like openai_api_key, model, reasoning_effort.
   * We map these to our LLMSettings type. Once Task 8 updates the API handler,
   * it will send provider/api_key/thinking_level directly.
   */
  private extractLLMSettings(msg: WebSocketMessage): LLMSettings | null {
    // Current Go API sends openai_api_key, model, reasoning_effort
    const apiKey = msg.openai_api_key;
    if (!apiKey) return null;

    return {
      provider: "openai", // Default to openai until Task 8 adds provider field
      api_key: apiKey,
      model: msg.model ?? "o4-mini",
      thinking_level: this.mapReasoningEffort(msg.reasoning_effort),
      base_url: msg.base_url,
    };
  }

  /**
   * Map Go's reasoning effort string to our ThinkingLevel.
   */
  private mapReasoningEffort(effort: string | undefined): LLMSettings["thinking_level"] {
    switch (effort) {
      case "low":
        return "low";
      case "medium":
        return "medium";
      case "high":
        return "high";
      default:
        return "medium";
    }
  }
}
