/**
 * Shared types for the agent worker, mirroring the Go WebSocket message protocol.
 *
 * These types match the JSON wire format used by the Go API server
 * (internal/handlers/agent_ws.go AgentMessage struct). Field names use
 * snake_case to match Go JSON tags exactly.
 */

// ---------------------------------------------------------------------------
// Message types (matches Go AgentMessageType constants)
// ---------------------------------------------------------------------------

/** Messages from API to agent worker */
export type APIToWorkerMessageType =
  | "new_incident"
  | "continue_incident"
  | "cancel_incident"
  | "proxy_config_update";

/** Messages from agent worker to API */
export type WorkerToAPIMessageType =
  | "agent_output"
  | "agent_completed"
  | "agent_error"
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
// Tool allowlist (sent with new_incident to restrict tool access)
// ---------------------------------------------------------------------------

export interface ToolAllowlistEntry {
  instance_id: number;
  logical_name: string;
  tool_type: string;
}

// ---------------------------------------------------------------------------
// WebSocket message (matches Go AgentMessage struct JSON wire format)
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

  // Execution metrics (sent with agent_completed)
  tokens_used?: number;
  execution_time_ms?: number;

  // LLM settings (sent with new_incident)
  provider?: string;
  api_key?: string;
  model?: string;
  thinking_level?: string;
  base_url?: string;

  // Proxy configuration with toggles (sent with new_incident)
  proxy_config?: ProxyConfig;

  // Enabled skill names (sent with new_incident to filter skill discovery)
  enabled_skills?: string[];

  // Tool allowlist (sent with new_incident to restrict tool access)
  tool_allowlist?: ToolAllowlistEntry[];
}

// ---------------------------------------------------------------------------
// Execution result (matches Go agent-worker ExecuteResult)
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
