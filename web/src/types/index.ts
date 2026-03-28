export interface Skill {
  id: number;
  name: string;
  description: string;
  category: string;
  prompt: string;
  is_system: boolean;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  tools?: ToolInstance[];
}

export interface ToolType {
  id: number;
  name: string;
  description: string;
  schema: Record<string, any>;
  created_at: string;
  updated_at: string;
}

export interface ToolInstance {
  id: number;
  tool_type_id: number;
  name: string;
  logical_name: string;
  settings: Record<string, any>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  tool_type?: ToolType;
}

export type IncidentStatus = 'pending' | 'running' | 'diagnosed' | 'completed' | 'failed';

export interface Incident {
  id: number;
  uuid: string;
  source: string;
  source_id: string;
  title: string;  // LLM-generated title summarizing the incident
  status: IncidentStatus;
  context: Record<string, any>;
  session_id: string;
  working_dir: string;
  full_log: string;
  response: string;  // Final response/output to user
  tokens_used: number;  // Total tokens used (input + output)
  execution_time_ms: number;  // Execution time in milliseconds
  started_at: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface EventSource {
  id: number;
  type: 'slack' | 'zabbix' | 'webhook';
  name: string;
  settings: Record<string, any>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface SlackSettings {
  id: number;
  bot_token: string;  // Masked for display
  signing_secret: string;  // Masked for display
  app_token: string;  // Masked for display
  alerts_channel: string;
  enabled: boolean;
  is_configured: boolean;
  created_at: string;
  updated_at: string;
}

export interface SlackSettingsUpdate {
  bot_token?: string;
  signing_secret?: string;
  app_token?: string;
  alerts_channel?: string;
  enabled?: boolean;
}

export interface CreateIncidentRequest {
  task: string;
  context?: Record<string, any>;
}

export interface CreateIncidentResponse {
  uuid: string;
  status: string;
  working_dir: string;
  message: string;
}

export type LLMProvider = 'openai' | 'anthropic' | 'google' | 'openrouter' | 'custom';
export type ThinkingLevel = 'off' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh';

export interface LLMProviderSettings {
  api_key: string;  // Masked for display
  model: string;
  thinking_level: ThinkingLevel;
  base_url: string;
  is_configured: boolean;
}

export interface LLMSettings {
  id: number;
  provider: LLMProvider;
  api_key: string;  // Masked for display (active provider)
  model: string;
  thinking_level: ThinkingLevel;
  base_url: string;
  is_configured: boolean;
  active_provider: LLMProvider;
  providers: Record<LLMProvider, LLMProviderSettings>;
  created_at: string;
  updated_at: string;
}

export interface LLMSettingsUpdate {
  provider?: LLMProvider;
  api_key?: string;
  model?: string;
  thinking_level?: ThinkingLevel;
  base_url?: string;
}

// Proxy Settings types
export interface ProxyServiceConfig {
  enabled: boolean;
  supported: boolean;
}

export interface ProxySettings {
  proxy_url: string;
  no_proxy: string;
  services: {
    llm: ProxyServiceConfig;
    slack: ProxyServiceConfig;
    zabbix: ProxyServiceConfig;
    victoria_metrics: ProxyServiceConfig;
    catchpoint: ProxyServiceConfig;
    grafana: ProxyServiceConfig;
    pagerduty: ProxyServiceConfig;
    ssh: ProxyServiceConfig;
  };
}

export interface ProxySettingsUpdate {
  proxy_url: string;
  no_proxy: string;
  services: {
    llm: { enabled: boolean };
    slack: { enabled: boolean };
    zabbix: { enabled: boolean };
    victoria_metrics: { enabled: boolean };
    catchpoint: { enabled: boolean };
    grafana: { enabled: boolean };
    pagerduty: { enabled: boolean };
  };
}


// Context Files
export interface ContextFile {
  id: number;
  filename: string;
  original_name: string;
  mime_type: string;
  size: number;
  description?: string;
  created_at: string;
  updated_at: string;
}

// Runbooks
export interface Runbook {
  id: number;
  title: string;
  content: string;
  created_at: string;
  updated_at: string;
}

export interface ValidateReferencesRequest {
  text: string;
}

export interface ValidateReferencesResponse {
  valid: boolean;
  references: string[];
  found: string[];
  missing: string[];
}

// Authentication types
export interface LoginRequest {
  username: string;
  password: string;
}

export interface LoginResponse {
  token: string;
  username: string;
  expires_in: number;
}

export interface AuthUser {
  username: string;
  token: string;
}

export interface SetupStatusResponse {
  setup_required: boolean;
  setup_completed: boolean;
}

export interface SetupRequest {
  password: string;
  confirm_password: string;
}

// Skill Scripts
export interface ScriptsListResponse {
  skill_name: string;
  scripts_dir: string;
  scripts: string[];
}

export interface ScriptInfo {
  filename: string;
  content: string;
  size: number;
  modified_at: string;
}

// Alert Source Types (for webhook configuration)
export interface AlertSourceType {
  id: number;
  name: string;
  display_name: string;
  description: string;
  default_field_mappings: Record<string, string>;
  webhook_secret_header: string;
  created_at: string;
  updated_at: string;
}

export interface AlertSourceInstance {
  id: number;
  uuid: string;
  alert_source_type_id: number;
  name: string;
  description: string;
  webhook_secret: string;
  field_mappings: Record<string, string>;
  settings: Record<string, any>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  alert_source_type?: AlertSourceType;
}

export interface CreateAlertSourceRequest {
  source_type_name: string;
  name: string;
  description?: string;
  webhook_secret?: string;
  field_mappings?: Record<string, string>;
  settings?: Record<string, any>;
}

export interface UpdateAlertSourceRequest {
  name?: string;
  description?: string;
  webhook_secret?: string;
  field_mappings?: Record<string, string>;
  settings?: Record<string, any>;
  enabled?: boolean;
}

// SSH Keys (for SSH tool management)
export interface SSHKey {
  id: string;
  name: string;
  is_default: boolean;
  created_at: string;
}

export interface SSHKeyCreateRequest {
  name: string;
  private_key: string;
  is_default?: boolean;
}

export interface SSHKeyUpdateRequest {
  name?: string;
  is_default?: boolean;
}

// Retention Settings
export interface RetentionSettings {
  id: number;
  enabled: boolean;
  retention_days: number;
  cleanup_interval_hours: number;
  created_at: string;
  updated_at: string;
}

export interface RetentionSettingsUpdate {
  enabled?: boolean;
  retention_days?: number;
  cleanup_interval_hours?: number;
}

// General Settings
export interface GeneralSettings {
  id: number;
  base_url: string;
  created_at: string;
  updated_at: string;
}

export interface GeneralSettingsUpdate {
  base_url?: string;
}

// Pagination
export interface PaginationMeta {
  page: number;
  per_page: number;
  total: number;
  total_pages: number;
}

export interface PaginatedResponse<T> {
  data: T[];
  pagination: PaginationMeta;
}

export interface SSHHostConfig {
  hostname: string;
  address: string;
  user?: string;
  port?: number;
  key_id?: string;  // Override key for this host
  jumphost_address?: string;
  jumphost_user?: string;
  jumphost_port?: number;
  allow_write_commands?: boolean;
}
