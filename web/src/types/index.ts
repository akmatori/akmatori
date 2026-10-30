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
  schema: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface ToolInstance {
  id: number;
  tool_type_id: number;
  name: string;
  logical_name: string;
  settings: Record<string, unknown>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  tool_type?: ToolType;
}

export type IncidentStatus = 'pending' | 'running' | 'diagnosed' | 'completed' | 'failed' | 'monitor' | 'closed' | 'merged';

export interface Incident {
  id: number;
  uuid: string;
  source: string;
  source_id: string;
  title: string;  // LLM-generated title summarizing the incident
  status: IncidentStatus;
  context: Record<string, unknown>;
  // The agent work comes from the incident's runs (detail endpoint only):
  // the newest answer, every run's log, and the token/time sums.
  full_log?: string;
  response?: string;
  tokens_used: number;
  execution_time_ms: number;
  runs?: AgentRun[];
  started_at: string;
  completed_at?: string;
  monitor_until?: string;
  // Why the incident closed: 'manual' | 'monitor_expired' | 'auto_stale'.
  // Absent on open incidents and on rows closed before the field existed.
  closed_reason?: string;
  alert_count?: number;
  source_kind?: string;
  first_seen?: string;
  last_seen?: string;
  trend?: number[];
  created_at: string;
  updated_at: string;
}

// AgentRun is one agent invocation (listed without its log and answer).
export interface AgentRun {
  run_id: string;
  scope_uuid: string;
  incident_uuid?: string;
  event_uuid?: string;
  source_kind: string;
  source_uuid?: string;
  root_skill?: string;
  provider: string;
  model: string;
  status: 'running' | 'completed' | 'failed' | 'superseded';
  outcome?: string;
  tokens_used: number;
  execution_time_ms: number;
  cost_usd: number;
  error_message?: string;
  started_at: string;
  completed_at?: string;
}

// AgentWork is what the summary and timeline tabs show: an incident's or a
// request's newest answer and its log.
export interface AgentWork {
  uuid: string;
  status: string;
  response?: string;
  full_log?: string;
  tokens_used: number;
  execution_time_ms: number;
}

// RequestDetail is request work: agent work that is not an incident (a Slack
// or API request, a cron run, a proposal chat). Its uuid is the scope id.
export interface RequestDetail extends AgentWork {
  title: string;
  source_kind: string;
  source_uuid?: string;
  status: 'running' | 'completed' | 'failed';
  event_uuid?: string;
  started_at: string;
  completed_at?: string;
  runs: AgentRun[];
}

export interface Alert {
  uuid: string;
  incident_uuid: string;
  status: 'firing' | 'resolved';
  fingerprint?: string;
  source_uuid?: string;
  alert_name: string;
  target_host: string;
  fired_at: string;
  resolved_at?: string;
  correlated: boolean;
  correlation_confidence?: number;
  correlation_reasoning?: string;
  correlation_decision?: string;
  raw_payload: unknown;
  created_at: string;
  updated_at: string;
}

export interface EventSource {
  id: number;
  type: 'slack' | 'zabbix' | 'webhook';
  name: string;
  settings: Record<string, unknown>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateIncidentRequest {
  task: string;
  context?: Record<string, unknown>;
}

// CreateIncidentResponse is the result of POST /api/incidents. The task is
// classified first: a reported problem opens or joins an incident
// (event_kind "alert", uuid = incident), a request starts request work
// (event_kind "request", uuid = request scope), a change or info is only
// stored (status "recorded", no uuid).
export interface CreateIncidentResponse {
  uuid: string;
  status: string;
  working_dir?: string;
  message: string;
  event_uuid?: string;
  event_kind?: EventKind;
}

export type LLMProvider = 'openai' | 'anthropic' | 'google' | 'openrouter' | 'custom' | 'nvidia' | 'minimax' | 'ant-ling';
export type ThinkingLevel = 'off' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max';

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
  // Sampling overrides. null means unset — the request omits the parameter and
  // the provider's own default applies.
  temperature: number | null;
  top_p: number | null;
  top_k: number | null;
  max_tokens: number | null;
  // Subagent model override (same provider). null means the runbook/memory
  // search subagents run on the parent's model and thinking level.
  subagent_model: string | null;
  subagent_thinking_level: ThinkingLevel | null;
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
  temperature?: number;
  top_p?: number;
  top_k?: number;
  max_tokens?: number;
  subagent_model?: string;
  subagent_thinking_level?: string;
}

// On update, an omitted sampling key leaves the stored value alone while an
// explicit null clears the override back to the provider default.
export interface UpdateLLMConfigRequest {
  name?: string;
  api_key?: string;
  model?: string;
  thinking_level?: string;
  base_url?: string;
  temperature?: number | null;
  top_p?: number | null;
  top_k?: number | null;
  max_tokens?: number | null;
  subagent_model?: string | null;
  subagent_thinking_level?: string | null;
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

// Cross-incident memory
export interface Memory {
  id: number;
  scope: string;
  type: string;
  name: string;
  description: string;
  body: string;
  incident_uuid?: string;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

// A resolved [[...]] link in a memory body. GET /api/memories/{id} returns
// these as `references`; the list endpoint does not.
export interface MemoryRef {
  raw: string;
  kind: 'memory' | 'file' | 'invalid';
  scope?: string;
  name?: string;
  memory_id?: number;
  path?: string;
  exists: boolean;
}

export interface MemoryDetail extends Memory {
  references: MemoryRef[];
}

// Fields an operator sets on create/update. On update, an omitted field is
// left unchanged; `body: ''` clears the body.
export interface MemoryInput {
  scope: string;
  type: string;
  name: string;
  description: string;
  body?: string;
  incident_uuid?: string;
}

// Runbooks
export interface Runbook {
  id: number;
  title: string;
  content: string;
  created_at: string;
  updated_at: string;
}

// Self-improvement proposals
export type ProposalKind =
  | 'runbook_new'
  | 'runbook_update'
  | 'memory_new'
  | 'memory_update'
  | 'cron_new'
  | 'cron_update'
  | 'skill_prompt_update';

export type ProposalStatus =
  | 'pending'
  | 'approved'
  | 'rejected'
  | 'apply_failed'
  | 'superseded';

export interface Proposal {
  id: number;
  uuid: string;
  kind: ProposalKind;
  status: ProposalStatus;
  title: string;
  reasoning: string;
  target_ref: string;
  current_snapshot: string; // JSON string, per-kind shape
  proposed_content: string; // JSON string, per-kind shape
  source_incident_uuids?: { uuids?: string[] };
  evaluation_run_uuid: string;
  chat_incident_uuid: string;
  created_by: string;
  apply_error: string;
  applied_at?: string;
  created_at: string;
  updated_at: string;
}

export interface ProposalChatMessage {
  id: number;
  proposal_uuid: string;
  role: 'operator' | 'assistant';
  content: string;
  created_at: string;
}

export interface ProposalChatResponse {
  messages: ProposalChatMessage[];
  chat_incident_uuid: string;
  chat_status: string;
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
  credentials: Record<string, unknown>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateIntegrationRequest {
  provider: MessagingProvider;
  name: string;
  credentials?: Record<string, unknown>;
  enabled?: boolean;
}

export interface UpdateIntegrationRequest {
  name?: string;
  credentials?: Record<string, unknown>;
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
  process_bot_messages: boolean;
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
  process_bot_messages?: boolean;
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
  process_bot_messages?: boolean;
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
  settings: Record<string, unknown>;
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
  settings?: Record<string, unknown>;
  notification_channel_uuid?: string | null;
}

export interface UpdateAlertSourceRequest {
  name?: string;
  description?: string;
  webhook_secret?: string;
  field_mappings?: Record<string, string>;
  settings?: Record<string, unknown>;
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
  post_results: boolean;
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
  post_results?: boolean;
  tool_instance_ids?: number[];
}

export interface UpdateCronJobRequest {
  name?: string;
  schedule?: string;
  prompt?: string;
  channel_uuid?: string;
  enabled?: boolean;
  post_results?: boolean;
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

// Per-flow formatting rules (replaces the global formatting settings)
export interface FormattingRule {
  id: number;
  uuid: string;
  name: string;
  enabled: boolean;
  position: number;
  match_source_kind: string;
  match_source_uuid: string;
  match_channel_uuid: string;
  match_last_skill: string;
  match_expression: string;
  system_prompt: string;
  output_schema_example: string;
  max_tokens: number;
  temperature: number;
  created_at: string;
  updated_at: string;
}

export interface FormattingRuleCreate {
  name: string;
  enabled?: boolean;
  match_source_kind?: string;
  match_source_uuid?: string;
  match_channel_uuid?: string;
  match_last_skill?: string;
  match_expression?: string;
  system_prompt?: string;
  output_schema_example?: string;
  max_tokens?: number;
  temperature?: number;
}

export interface FormattingRuleUpdate {
  name?: string;
  enabled?: boolean;
  match_source_kind?: string;
  match_source_uuid?: string;
  match_channel_uuid?: string;
  match_last_skill?: string;
  match_expression?: string;
  system_prompt?: string;
  output_schema_example?: string;
  max_tokens?: number;
  temperature?: number;
}

// General Settings
export interface GeneralSettings {
  id: number;
  base_url: string;
  created_at: string;
  updated_at: string;
  // Alert correlation gate (non-nullable after GET hydration with effective defaults)
  alert_correlation_enabled: boolean;
  alert_monitor_window_minutes: number;
  // Correlator rules text; "" means the built-in rules, sent alongside as
  // alert_correlation_prompt_default so the form can show and restore them.
  alert_correlation_prompt: string;
  alert_correlation_prompt_default: string;
  // Event classification gate (default ON) and rules; same "" = default
  // convention as the correlation rules.
  event_classification_enabled: boolean;
  event_classification_prompt: string;
  event_classification_prompt_default: string;
  incident_merge_enabled: boolean;
  // Stale-close sweep. Defaults ON, unlike the correlation and merge gates.
  incident_auto_close_enabled: boolean;
  incident_auto_close_minutes: number;
  // Agent-written memory. Defaults ON; off, agents stop writing memory and
  // their memory files are discarded. Operator memory edits still work.
  memory_generation_enabled: boolean;
}

export interface GeneralSettingsUpdate {
  base_url?: string;
  alert_correlation_enabled?: boolean;
  alert_monitor_window_minutes?: number;
  // "" or the default text restores the built-in rules.
  alert_correlation_prompt?: string;
  event_classification_enabled?: boolean;
  // "" or the default text restores the built-in rules.
  event_classification_prompt?: string;
  incident_merge_enabled?: boolean;
  incident_auto_close_enabled?: boolean;
  incident_auto_close_minutes?: number;
  memory_generation_enabled?: boolean;
}

// Pagination
export interface PaginationMeta {
  page: number;
  per_page: number;
  total: number;
  total_pages: number;
}

// GET /api/system/status. Times are absent while no worker is connected.
export interface SystemStatusResponse {
  worker: {
    connected: boolean;
    connected_at?: string;
    last_message_at?: string;
    last_message_age_seconds?: number;
  };
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

// Events feed: one inbound item (webhook alert item, Slack message or
// mention, API task, cron tick, proposal chat) with its class.
export type EventKind = 'alert' | 'change' | 'info' | 'request';

export interface EventFeedItem {
  event_uuid: string;
  kind: EventKind;
  source_kind: string;
  source_type?: string;
  source_uuid?: string;
  classification: string;
  classification_confidence?: number;
  classification_reasoning?: string;
  title: string;
  occurred_at: string;
  // Alert events: the alert's firing/resolved state. Request events: the
  // run's status. Change and info events: empty.
  status: string;
  target_host?: string;
  alert_uuid?: string;
  incident_uuid?: string;
  // Request events: the request work they started or continued.
  scope_uuid?: string;
  correlated: boolean;
  correlation_confidence?: number;
  correlation_reasoning?: string;
  correlation_decision?: string;
  incident_title?: string;
  incident_status?: string;
}

