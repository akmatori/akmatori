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

export interface LLMConfig {
  id: number;
  name: string;
  provider: LLMProvider;
  model: string;
  thinking_level: ThinkingLevel;
  base_url: string;
  api_key: string;  // Masked for display
  is_configured: boolean;
  enabled: boolean;
  active: boolean;
  created_at: string;
  updated_at: string;
}

export interface LLMSettingsListResponse {
  configs: LLMConfig[];
  active_id: number;
}

export interface CreateLLMConfigRequest {
  provider: string;
  name: string;
  api_key?: string;
  model?: string;
  thinking_level?: string;
  base_url?: string;
}

export interface UpdateLLMConfigRequest {
  name?: string;
  api_key?: string;
  model?: string;
  thinking_level?: string;
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
    netbox: ProxyServiceConfig;
    kubernetes: ProxyServiceConfig;
    jira: ProxyServiceConfig;
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
    netbox: { enabled: boolean };
    kubernetes: { enabled: boolean };
    jira: { enabled: boolean };
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

// Messaging integrations & channels

export type MessagingProvider = 'slack' | 'telegram';

export interface Integration {
  id: number;
  uuid: string;
  provider: MessagingProvider;
  name: string;
  credentials: Record<string, any>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateIntegrationRequest {
  provider: MessagingProvider;
  name: string;
  credentials?: Record<string, any>;
  enabled?: boolean;
}

export interface UpdateIntegrationRequest {
  name?: string;
  credentials?: Record<string, any>;
  enabled?: boolean;
}

export interface Channel {
  id: number;
  uuid: string;
  integration_id: number;
  external_id: string;
  display_name: string;
  can_post: boolean;
  can_listen: boolean;
  is_default_post: boolean;
  extraction_prompt: string;
  process_human_messages: boolean;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  integration?: Integration;
}

export interface CreateChannelRequest {
  integration_uuid: string;
  external_id: string;
  display_name?: string;
  can_post: boolean;
  can_listen: boolean;
  is_default_post?: boolean;
  extraction_prompt?: string;
  process_human_messages?: boolean;
  enabled?: boolean;
}

export interface UpdateChannelRequest {
  external_id?: string;
  display_name?: string;
  can_post?: boolean;
  can_listen?: boolean;
  is_default_post?: boolean;
  extraction_prompt?: string;
  process_human_messages?: boolean;
  enabled?: boolean;
}

export interface ListChannelsFilter {
  integration_uuid?: string;
  can_post?: boolean;
  can_listen?: boolean;
}

// Alert Source Types (for webhook configuration)
export interface AlertSourceType {
  id: number;
  name: string;
  display_name: string;
  description: string;
  default_field_mappings: Record<string, string>;
  webhook_secret_header: string;
  deprecated?: boolean;
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
  notification_channel_id?: number | null;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  alert_source_type?: AlertSourceType;
  notification_channel?: Channel | null;
}

export interface CreateAlertSourceRequest {
  source_type_name: string;
  name: string;
  description?: string;
  webhook_secret?: string;
  field_mappings?: Record<string, string>;
  settings?: Record<string, any>;
  notification_channel_uuid?: string | null;
}

export interface UpdateAlertSourceRequest {
  name?: string;
  description?: string;
  webhook_secret?: string;
  field_mappings?: Record<string, string>;
  settings?: Record<string, any>;
  enabled?: boolean;
  notification_channel_uuid?: string | null;
}

// Cron Jobs

export type CronRunStatus = '' | 'ok' | 'error';

// CronJobTool is the slim per-cron tool view returned on /api/cron-jobs.
// The backend omits Settings (which can hold secrets) and embeds only the
// ToolType name. Mirrors `toolInstanceSummary` in api_cron_jobs.go.
export interface CronJobTool {
  id: number;
  name: string;
  logical_name: string;
  tool_type: string;
  enabled: boolean;
}

export interface CronJob {
  id: number;
  uuid: string;
  name: string;
  schedule: string;
  prompt: string;
  is_system: boolean;
  channel_id?: number | null;
  enabled: boolean;
  last_run_at?: string | null;
  last_run_status: CronRunStatus;
  last_run_error: string;
  next_run_at?: string | null;
  created_at: string;
  updated_at: string;
  channel?: Channel | null;
  tools: CronJobTool[];
}

export interface CreateCronJobRequest {
  name: string;
  schedule: string;
  prompt: string;
  channel_uuid?: string;
  enabled?: boolean;
  tool_instance_ids?: number[];
}

export interface UpdateCronJobRequest {
  name?: string;
  schedule?: string;
  prompt?: string;
  channel_uuid?: string;
  enabled?: boolean;
  tool_instance_ids?: number[];
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

// Formatting Settings
export interface FormattingSettings {
  id: number;
  enabled: boolean;
  system_prompt: string;
  max_tokens: number;
  temperature: number;
  created_at: string;
  updated_at: string;
}

export interface FormattingSettingsUpdate {
  enabled?: boolean;
  system_prompt?: string;
  max_tokens?: number;
  temperature?: number;
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
