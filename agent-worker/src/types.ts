/**
 * Shared types for the agent worker, mirroring the Go WebSocket message protocol.
 *
 * These types match the JSON wire format used by the Go API server
 * (internal/handlers/codex_ws.go CodexMessage struct). Field names use
 * snake_case to match Go JSON tags exactly.
 *
 * Note: ChatGPT subscription and device auth fields are intentionally omitted -
 * this worker uses multi-provider LLM settings via pi-mono SDK.
 */

// ---------------------------------------------------------------------------
// Message types (matches Go CodexMessageType constants)
// ---------------------------------------------------------------------------

/** Messages from API to agent worker */
export type APIToWorkerMessageType =
  | "new_incident"
  | "continue_incident"
  | "cancel_incident"
  | "proxy_config_update";

/** Messages from agent worker to API */
export type WorkerToAPIMessageType =
  | "codex_output"
  | "codex_completed"
  | "codex_error"
  | "heartbeat"
  | "status";

export type MessageType = APIToWorkerMessageType | WorkerToAPIMessageType;

// ---------------------------------------------------------------------------
// Proxy configuration (matches Go ProxyConfig struct)
// ---------------------------------------------------------------------------

export interface ProxyConfig {
  url: string;
  no_proxy: string;
  openai_enabled: boolean;
  slack_enabled: boolean;
  zabbix_enabled: boolean;
}

// ---------------------------------------------------------------------------
// LLM settings (new multi-provider, replaces OpenAI-only fields)
// ---------------------------------------------------------------------------

export type LLMProvider =
  | "openai"
  | "anthropic"
  | "google"
  | "openrouter"
  | "custom";

export type ThinkingLevel =
  | "off"
  | "minimal"
  | "low"
  | "medium"
  | "high"
  | "xhigh";

export interface LLMSettings {
  provider: LLMProvider;
  api_key: string;
  model: string;
  thinking_level: ThinkingLevel;
  base_url?: string;
}

// ---------------------------------------------------------------------------
// WebSocket message (matches Go CodexMessage struct JSON wire format)
// ---------------------------------------------------------------------------

/**
 * WebSocketMessage is the on-the-wire JSON envelope exchanged between the
 * Go API server and this worker over the WebSocket connection.
 *
 * Field names and omitempty semantics match the Go struct tags exactly so
 * both sides can deserialize each other's messages without changes.
 */
export interface WebSocketMessage {
  type: MessageType;
  incident_id?: string;
  task?: string;
  message?: string;
  output?: string;
  session_id?: string;
  error?: string;
  data?: Record<string, unknown>;

  // Execution metrics (sent with codex_completed)
  tokens_used?: number;
  execution_time_ms?: number;

  // LLM settings (sent with new_incident)
  provider?: string;
  openai_api_key?: string;
  model?: string;
  reasoning_effort?: string;
  base_url?: string;
  proxy_url?: string;
  no_proxy?: string;

  // Proxy configuration with toggles (sent with new_incident)
  proxy_config?: ProxyConfig;
}

// ---------------------------------------------------------------------------
// Execution result (matches Go codex-worker ExecuteResult)
// ---------------------------------------------------------------------------

export interface ExecuteResult {
  session_id: string;
  response: string;
  full_log: string;
  error?: string;
  tokens_used: number;
  execution_time_ms: number;
}

// ---------------------------------------------------------------------------
// Helper: create a typed message with only the relevant fields populated
// ---------------------------------------------------------------------------

export function createMessage(
  type: MessageType,
  fields?: Omit<WebSocketMessage, "type">,
): WebSocketMessage {
  const msg: WebSocketMessage = { type };
  if (fields) {
    Object.assign(msg, fields);
  }
  return msg;
}

// ---------------------------------------------------------------------------
// Serialization helpers
// ---------------------------------------------------------------------------

/** Serialize a WebSocketMessage to JSON, omitting undefined and null values. */
export function serializeMessage(msg: WebSocketMessage): string {
  return JSON.stringify(msg, (_key, value) => {
    if (value === undefined || value === null) return undefined;
    return value;
  });
}

/** Deserialize a JSON string into a WebSocketMessage. */
export function deserializeMessage(json: string): WebSocketMessage {
  return JSON.parse(json) as WebSocketMessage;
}
