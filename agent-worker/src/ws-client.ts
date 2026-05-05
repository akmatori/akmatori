/**
 * WebSocket client for agent worker.
 *
 * Provides connection management, heartbeat, and typed message helpers
 * for communicating with the Go API server over WebSocket.
 */

import WebSocket from "ws";
import {
  type WebSocketMessage,
  type MessageType,
  serializeMessage,
  deserializeMessage,
} from "./types.js";

export interface WebSocketClientOptions {
  /** WebSocket URL to connect to */
  url: string;
  /** Connection timeout in ms (default: 10000) */
  connectTimeoutMs?: number;
  /** Heartbeat interval in ms (default: 30000) */
  heartbeatIntervalMs?: number;
  /** Logger function (default: console.log) */
  logger?: (msg: string) => void;
}

type MessageHandler = (msg: WebSocketMessage) => void;

export class WebSocketClient {
  private url: string;
  private ws: WebSocket | null = null;
  private connected = false;
  private closed = false;
  private messageHandler: MessageHandler | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private readonly connectTimeoutMs: number;
  private readonly heartbeatIntervalMs: number;
  private readonly log: (msg: string) => void;

  constructor(opts: WebSocketClientOptions) {
    this.url = opts.url;
    this.connectTimeoutMs = opts.connectTimeoutMs ?? 10_000;
    this.heartbeatIntervalMs = opts.heartbeatIntervalMs ?? 30_000;
    this.log = opts.logger ?? ((msg: string) => console.log(`[ws-client] ${msg}`));
  }

  /** Connect to the WebSocket server. Resolves when open, rejects on timeout/error. */
  connect(): Promise<void> {
    return new Promise((resolve, reject) => {
      if (this.closed) {
        reject(new Error("Client has been closed"));
        return;
      }

      const ws = new WebSocket(this.url, {
        handshakeTimeout: this.connectTimeoutMs,
      });

      const timeout = setTimeout(() => {
        ws.terminate();
        reject(new Error(`Connection timeout after ${this.connectTimeoutMs}ms`));
      }, this.connectTimeoutMs);

      ws.on("open", () => {
        clearTimeout(timeout);
        this.ws = ws;
        this.connected = true;
        this.log(`Connected to ${this.url}`);
        this.startHeartbeat();
        resolve();
      });

      ws.on("message", (data: WebSocket.RawData) => {
        try {
          const msg = deserializeMessage(data.toString());
          if (this.messageHandler) {
            this.messageHandler(msg);
          }
        } catch (err) {
          this.log(`Failed to parse message: ${err}`);
        }
      });

      ws.on("close", (code, reason) => {
        clearTimeout(timeout);
        const wasConnected = this.connected;
        this.connected = false;
        this.stopHeartbeat();
        if (wasConnected) {
          this.log(`Connection closed: code=${code} reason=${reason.toString()}`);
        }
      });

      ws.on("error", (err) => {
        clearTimeout(timeout);
        if (!this.connected) {
          reject(err);
        } else {
          this.log(`WebSocket error: ${err.message}`);
        }
      });
    });
  }

  /** Whether the client is currently connected. */
  isConnected(): boolean {
    return this.connected && this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }

  /** Register a handler for incoming messages. */
  onMessage(handler: MessageHandler): void {
    this.messageHandler = handler;
  }

  /** Send a typed WebSocket message. */
  send(msg: WebSocketMessage): void {
    if (!this.isConnected()) {
      this.log("Cannot send: not connected");
      return;
    }
    this.ws!.send(serializeMessage(msg));
  }

  /**
   * Send streaming output for an incident. runId echoes the API-stamped
   * run identifier so the API can drop late frames from a superseded run.
   * Pass undefined for synthetic outputs that have no associated run.
   */
  sendOutput(incidentId: string, runId: string | undefined, output: string): void {
    this.send({
      type: "agent_output",
      incident_id: incidentId,
      output,
      ...(runId ? { run_id: runId } : {}),
    });
  }

  /**
   * Send completion notification with metrics. runId mirrors the API run_id
   * so completion for a superseded run is dropped instead of closing the new
   * waiter's done channel.
   */
  sendCompleted(
    incidentId: string,
    runId: string | undefined,
    sessionId: string,
    response: string,
    tokensUsed: number,
    executionTimeMs: number,
  ): void {
    this.send({
      type: "agent_completed",
      incident_id: incidentId,
      session_id: sessionId,
      output: response,
      tokens_used: tokensUsed,
      execution_time_ms: executionTimeMs,
      ...(runId ? { run_id: runId } : {}),
    });
  }

  /**
   * Send error notification for an incident. runId mirrors the API run_id
   * so a superseded-run error is dropped on the API side rather than firing
   * OnError on the new waiter's callback.
   */
  sendError(incidentId: string, runId: string | undefined, errorMsg: string): void {
    this.send({
      type: "agent_error",
      incident_id: incidentId,
      error: errorMsg,
      ...(runId ? { run_id: runId } : {}),
    });
  }

  /** Send a one-shot LLM response correlated with the originating request. */
  sendOneshotResponse(requestId: string, summary: string, errorMsg?: string): void {
    this.send({
      type: "oneshot_llm_response",
      request_id: requestId,
      summary,
      ...(errorMsg ? { error: errorMsg } : {}),
    });
  }

  /** Send a heartbeat message. */
  sendHeartbeat(): void {
    this.send({ type: "heartbeat" });
  }

  /** Reset client state, allowing a new connect() call. */
  reset(): void {
    this.stopHeartbeat();
    this.connected = false;
    this.closed = false;
    if (this.ws) {
      try {
        this.ws.terminate();
      } catch {
        // ignore
      }
      this.ws = null;
    }
  }

  /** Gracefully close the client. */
  close(): void {
    this.closed = true;
    this.stopHeartbeat();
    if (this.ws) {
      try {
        this.ws.close(1000, "client closing");
      } catch {
        // ignore
      }
      this.ws = null;
    }
    this.connected = false;
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.heartbeatTimer = setInterval(() => {
      if (this.isConnected()) {
        this.sendHeartbeat();
      }
    }, this.heartbeatIntervalMs);
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }
}
