// Types for /api/connectors and /api/mcp-servers.

export type ConnectorCapability = 'tools' | 'alerts' | 'messaging';
export type ConnectorKind = 'builtin' | 'custom_mcp' | 'custom_http';
export type ConnectorPartKind = 'tool_instance' | 'alert_source' | 'integration' | 'mcp_server';
export type ConnectorState = 'ok' | 'connected' | 'disabled' | 'needs_setup' | 'error' | 'unknown';

export interface ConnectorCatalogEntry {
  id: string;
  name: string;
  description: string;
  category: string;
  kind: ConnectorKind;
  capabilities: ConnectorCapability[];
  tool_types?: string[];
  alert_source_types?: string[];
  messaging_provider?: string;
  listen_supported?: boolean;
  note?: string;
}

export interface ConnectorStatus {
  state: ConnectorState;
  detail?: string;
  observed_at?: string;
}

export interface ConnectorCounts {
  tool_instances: number;
  alert_sources: number;
  integrations: number;
  channels: number;
  mcp_tools: number;
}

export interface ConnectorSummary {
  id: number;
  uuid: string;
  name: string;
  catalog_id: string;
  catalog_name: string;
  kind: ConnectorKind;
  category: string;
  capabilities: ConnectorCapability[];
  enabled: boolean;
  status: ConnectorStatus;
  counts: ConnectorCounts;
  last_alert_at?: string;
  created_at: string;
}

export interface ConnectorToolPart {
  id: number;
  name: string;
  logical_name: string;
  tool_type: string;
  enabled: boolean;
  managed: boolean;
}

export interface ConnectorAlertPart {
  id: number;
  uuid: string;
  name: string;
  source_type: string;
  source_type_label: string;
  enabled: boolean;
  last_alert_at?: string;
}

export interface ConnectorIntegrationPart {
  id: number;
  uuid: string;
  provider: string;
  name: string;
  enabled: boolean;
  channel_count: number;
}

export interface MCPServerRuntimeStatus {
  id: number;
  connected: boolean;
  tool_count: number;
  tools: string[] | null;
  error: string;
  checked_at: string;
}

export interface ConnectorMCPPart {
  id: number;
  url: string;
  namespace_prefix: string;
  enabled: boolean;
  auth_type: string;
  disabled_tools: string[];
  runtime?: MCPServerRuntimeStatus;
}

export interface ConnectorDetail extends ConnectorSummary {
  catalog?: ConnectorCatalogEntry;
  parts: {
    tool_instances: ConnectorToolPart[];
    alert_sources: ConnectorAlertPart[];
    integrations: ConnectorIntegrationPart[];
    mcp_server?: ConnectorMCPPart;
  };
}

export type MCPAuthType = 'none' | 'bearer' | 'header' | 'oauth';

export interface MCPAuthSummary {
  type: MCPAuthType;
  header_name?: string;
  has_secret: boolean;
  oauth_connected?: boolean;
  oauth_expires_at?: string;
  oauth_scope?: string;
}

export interface MCPServer {
  id: number;
  name: string;
  transport: string;
  url?: string;
  namespace_prefix: string;
  auth_config?: Record<string, unknown>;
  auth: MCPAuthSummary;
  disabled_tools: string[];
  enabled: boolean;
  unsupported?: string;
  connector_id?: number;
  connector_uuid?: string;
  created_at: string;
  updated_at: string;
}

export interface MCPAuthConfigInput {
  type: Exclude<MCPAuthType, 'none'>;
  token?: string;
  header_name?: string;
  value?: string;
  client_id?: string;
  client_secret?: string;
  scope?: string;
}

export interface CreateMCPServerRequest {
  name: string;
  url: string;
  transport?: 'http';
  namespace_prefix?: string;
  auth_config?: MCPAuthConfigInput;
  disabled_tools?: string[];
  connector_uuid?: string;
}

export interface UpdateMCPServerRequest {
  name?: string;
  url?: string;
  namespace_prefix?: string;
  // undefined = unchanged, null = remove auth
  auth_config?: MCPAuthConfigInput | null;
  disabled_tools?: string[];
  enabled?: boolean;
}

export interface MCPTestTool {
  name: string;
  description: string;
}

export interface MCPTestResult {
  ok: boolean;
  server_info?: Record<string, string>;
  protocol_version?: string;
  tools?: MCPTestTool[];
  error?: string;
  auth_required?: boolean;
  www_authenticate?: string;
  status_code?: number;
}
