/**
 * WebSocket client for agent worker.
 *
 * Ports the Go codex-worker WebSocket client (codex-worker/internal/ws/client.go)
 * to TypeScript using the `ws` library. Provides connection management,
 * heartbeat, and typed message helpers.
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

  /** Send streaming output for an incident. */
  sendOutput(incidentId: string, output: string): void {
    this.send({
      type: "codex_output",
      incident_id: incidentId,
      output,
    });
  }

  /** Send completion notification with metrics. */
  sendCompleted(
    incidentId: string,
    sessionId: string,
    response: string,
    tokensUsed: number,
    executionTimeMs: number,
  ): void {
    this.send({
      type: "codex_completed",
      incident_id: incidentId,
      session_id: sessionId,
      output: response,
      tokens_used: tokensUsed,
      execution_time_ms: executionTimeMs,
    });
  }

  /** Send error notification for an incident. */
  sendError(incidentId: string, errorMsg: string): void {
    this.send({
      type: "codex_error",
      incident_id: incidentId,
      error: errorMsg,
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
