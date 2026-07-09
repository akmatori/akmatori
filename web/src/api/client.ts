import type {
  Skill,
  ToolType,
  ToolInstance,
  Incident,
  Alert,
  EventFeedItem,
  Integration,
  CreateIntegrationRequest,
  UpdateIntegrationRequest,
  Channel,
  CreateChannelRequest,
  UpdateChannelRequest,
  ListChannelsFilter,
  LLMConfig,
  LLMSettingsListResponse,
  CreateLLMConfigRequest,
  UpdateLLMConfigRequest,
  ProxySettings,
  ProxySettingsUpdate,
  GeneralSettings,
  GeneralSettingsUpdate,
  RetentionSettings,
  RetentionSettingsUpdate,
  FormattingSettings,
  FormattingSettingsUpdate,
  ContextFile,
  ValidateReferencesResponse,
  CreateIncidentRequest,
  CreateIncidentResponse,
  PaginatedResponse,
  ScriptsListResponse,
  ScriptInfo,
  AlertSourceType,
  AlertSourceInstance,
  CreateAlertSourceRequest,
  UpdateAlertSourceRequest,
  SSHKey,
  SSHKeyCreateRequest,
  SSHKeyUpdateRequest,
  Runbook,
  Memory,
  CronJob,
  CreateCronJobRequest,
  UpdateCronJobRequest,
  Proposal,
  ProposalChatResponse,
} from '../types';

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '';
const TOKEN_KEY = 'aiops_auth_token';

class ApiError extends Error {
  status: number;
  // Parsed JSON error body, when the response was JSON (e.g. the
  // requires_confirmation/firing_alert_count shape returned by
  // POST /api/incidents/{uuid}/close).
  body?: unknown;

  constructor(status: number, message: string, body?: unknown) {
    super(message);
    this.status = status;
    this.body = body;
    this.name = 'ApiError';
  }
}

// --- API availability tracking ---------------------------------------------
// A request that fails at the network level (status 0) or returns a gateway
// error (502/503/504) means the API itself is unreachable rather than the
// individual call being invalid. We surface that globally so the UI can show a
// single "API unavailable" banner instead of N scattered error toasts.
type AvailabilityListener = (available: boolean) => void;

let apiAvailable = true;
const availabilityListeners = new Set<AvailabilityListener>();

function setApiAvailable(available: boolean): void {
  if (available === apiAvailable) return;
  apiAvailable = available;
  for (const listener of availabilityListeners) listener(available);
}

function isUnavailableStatus(status: number): boolean {
  // 0 = network/fetch failure; 502/503/504 = reverse proxy can't reach the API.
  return status === 0 || status === 502 || status === 503 || status === 504;
}

export function subscribeApiAvailability(listener: AvailabilityListener): () => void {
  availabilityListeners.add(listener);
  // Emit current state immediately so late subscribers are in sync.
  listener(apiAvailable);
  return () => {
    availabilityListeners.delete(listener);
  };
}

function getAuthHeaders(): Record<string, string> {
  const token = localStorage.getItem(TOKEN_KEY);
  if (token) {
    return { Authorization: `Bearer ${token}` };
  }
  return {};
}

async function fetchApi<T>(endpoint: string, options?: RequestInit): Promise<T> {
  let response: Response;

  try {
    response = await fetch(`${API_BASE_URL}${endpoint}`, {
      ...options,
      headers: {
        'Content-Type': 'application/json',
        ...getAuthHeaders(),
        ...options?.headers,
      },
    });
  } catch (error) {
    // Network errors (DNS, timeout, CORS, offline)
    setApiAvailable(false);
    const message = error instanceof Error ? error.message : 'Network error';
    throw new ApiError(0, `Connection failed: ${message}`);
  }

  // Handle 401 Unauthorized - redirect to login
  if (response.status === 401) {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem('aiops_auth_user');
    window.location.href = '/login';
    throw new ApiError(401, 'Session expired. Please log in again.');
  }

  if (isUnavailableStatus(response.status)) {
    setApiAvailable(false);
    throw new ApiError(response.status, 'The Akmatori API is currently unavailable. Please try again in a moment.');
  }

  // The API answered (even with a 4xx) — it's reachable.
  setApiAvailable(true);

  if (!response.ok) {
    // Try JSON first (new standard format), fall back to text
    let message: string;
    let body: unknown;
    const text = await response.text();
    try {
      const json = JSON.parse(text);
      body = json;
      message = json.error || text || response.statusText;
    } catch {
      message = text || response.statusText;
    }
    throw new ApiError(response.status, message, body);
  }

  // Handle 204 No Content
  if (response.status === 204) {
    return undefined as T;
  }

  return response.json();
}

// Skills API (uses skill names in URLs, not IDs)
export const skillsApi = {
  list: () => fetchApi<Skill[]>('/api/skills'),

  get: (name: string) => fetchApi<Skill>(`/api/skills/${encodeURIComponent(name)}`),

  create: (skill: { name: string; description?: string; category?: string; prompt?: string }) =>
    fetchApi<Skill>('/api/skills', {
      method: 'POST',
      body: JSON.stringify(skill),
    }),

  update: (name: string, skill: Partial<Skill>) =>
    fetchApi<Skill>(`/api/skills/${encodeURIComponent(name)}`, {
      method: 'PUT',
      body: JSON.stringify(skill),
    }),

  delete: (name: string) =>
    fetchApi<void>(`/api/skills/${encodeURIComponent(name)}`, {
      method: 'DELETE',
    }),

  // Get skill prompt (SKILL.md body)
  getPrompt: (name: string) =>
    fetchApi<{ prompt: string }>(`/api/skills/${encodeURIComponent(name)}/prompt`),

  // Update skill prompt
  updatePrompt: (name: string, prompt: string) =>
    fetchApi<{ status: string }>(`/api/skills/${encodeURIComponent(name)}/prompt`, {
      method: 'PUT',
      body: JSON.stringify({ prompt }),
    }),

  // Get tools assigned to a skill
  getTools: (name: string) => fetchApi<ToolInstance[]>(`/api/skills/${encodeURIComponent(name)}/tools`),

  // Update tools assigned to a skill (triggers SKILL.md regeneration)
  updateTools: (name: string, toolInstanceIds: number[]) =>
    fetchApi<Skill>(`/api/skills/${encodeURIComponent(name)}/tools`, {
      method: 'PUT',
      body: JSON.stringify({ tool_instance_ids: toolInstanceIds }),
    }),

  // Sync skills from filesystem to database
  sync: () =>
    fetchApi<{ status: string; message: string }>('/api/skills/sync', {
      method: 'POST',
    }),
};

// Tool Types API
export const toolTypesApi = {
  list: () => fetchApi<ToolType[]>('/api/tool-types'),
};

// Tool Instances API
export const toolsApi = {
  list: () => fetchApi<ToolInstance[]>('/api/tools'),

  get: (id: number) => fetchApi<ToolInstance>(`/api/tools/${id}`),

  create: (tool: { tool_type_id: number; name: string; logical_name?: string; settings: Record<string, any> }) =>
    fetchApi<ToolInstance>('/api/tools', {
      method: 'POST',
      body: JSON.stringify(tool),
    }),

  update: (id: number, tool: { name: string; logical_name?: string; settings: Record<string, any>; enabled: boolean }) =>
    fetchApi<ToolInstance>(`/api/tools/${id}`, {
      method: 'PUT',
      body: JSON.stringify(tool),
    }),

  delete: (id: number) =>
    fetchApi<void>(`/api/tools/${id}`, {
      method: 'DELETE',
    }),
};

// SSH Keys API (for SSH tool instances)
export const sshKeysApi = {
  list: (toolId: number) => fetchApi<SSHKey[]>(`/api/tools/${toolId}/ssh-keys`),

  create: (toolId: number, data: SSHKeyCreateRequest) =>
    fetchApi<SSHKey>(`/api/tools/${toolId}/ssh-keys`, {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  update: (toolId: number, keyId: string, data: SSHKeyUpdateRequest) =>
    fetchApi<SSHKey>(`/api/tools/${toolId}/ssh-keys/${keyId}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  delete: (toolId: number, keyId: string) =>
    fetchApi<void>(`/api/tools/${toolId}/ssh-keys/${keyId}`, {
      method: 'DELETE',
    }),
};

// Incidents API
export const incidentsApi = {
  list: (from?: number, to?: number, page = 1, perPage = 50, trendWindow?: '1h' | '3h', status?: string) => {
    const params = new URLSearchParams();
    if (from !== undefined) params.set('from', String(from));
    if (to !== undefined) params.set('to', String(to));
    params.set('page', String(page));
    params.set('per_page', String(perPage));
    if (trendWindow) params.set('trend_window', trendWindow);
    if (status !== undefined) params.set('status', status);
    return fetchApi<PaginatedResponse<Incident>>(`/api/incidents?${params.toString()}`);
  },

  get: (uuid: string) => fetchApi<Incident>(`/api/incidents/${uuid}`),

  getAlerts: (uuid: string) => fetchApi<Alert[]>(`/api/incidents/${uuid}/alerts`),

  create: (request: CreateIncidentRequest) =>
    fetchApi<CreateIncidentResponse>('/api/incidents', {
      method: 'POST',
      body: JSON.stringify(request),
    }),

  sendFeedback: (uuid: string, text: string) =>
    fetchApi<Memory>(`/api/incidents/${uuid}/feedback`, {
      method: 'POST',
      body: JSON.stringify({ text }),
    }),

  // Manually close an incident. If it still has firing alerts and confirm is
  // false, the request rejects with an ApiError(409) whose `body` is
  // `{ requires_confirmation: true, firing_alert_count: number }` — callers
  // should surface that to the operator and retry with confirm=true.
  close: (uuid: string, confirm = false) =>
    fetchApi<{ status: string }>(`/api/incidents/${uuid}/close`, {
      method: 'POST',
      body: JSON.stringify({ confirm }),
    }),
};

// Self-improvement proposals API
export const proposalsApi = {
  list: (status?: string, kind?: string, page = 1, perPage = 50) => {
    const params = new URLSearchParams();
    if (status) params.set('status', status);
    if (kind) params.set('kind', kind);
    params.set('page', String(page));
    params.set('per_page', String(perPage));
    return fetchApi<PaginatedResponse<Proposal>>(`/api/proposals?${params.toString()}`);
  },

  get: (uuid: string) => fetchApi<Proposal>(`/api/proposals/${uuid}`),

  pendingCount: () => fetchApi<{ pending: number }>('/api/proposals/count'),

  approve: (uuid: string) =>
    fetchApi<Proposal>(`/api/proposals/${uuid}/approve`, { method: 'POST' }),

  reject: (uuid: string) =>
    fetchApi<Proposal>(`/api/proposals/${uuid}/reject`, { method: 'POST' }),

  getChat: (uuid: string) => fetchApi<ProposalChatResponse>(`/api/proposals/${uuid}/chat`),

  sendChat: (uuid: string, message: string) =>
    fetchApi<{ chat_incident_uuid: string; status: string }>(`/api/proposals/${uuid}/chat`, {
      method: 'POST',
      body: JSON.stringify({ message }),
    }),
};

// LLM Settings API
export const llmSettingsApi = {
  list: () => fetchApi<LLMSettingsListResponse>('/api/settings/llm'),

  get: (id: number) => fetchApi<LLMConfig>(`/api/settings/llm/${id}`),

  create: (data: CreateLLMConfigRequest) =>
    fetchApi<LLMConfig>('/api/settings/llm', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  update: (id: number, data: UpdateLLMConfigRequest) =>
    fetchApi<LLMConfig>(`/api/settings/llm/${id}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  delete: (id: number) =>
    fetchApi<void>(`/api/settings/llm/${id}`, {
      method: 'DELETE',
    }),

  activate: (id: number) =>
    fetchApi<LLMConfig>(`/api/settings/llm/${id}/activate`, {
      method: 'PUT',
    }),
};

// Proxy Settings API
export const proxySettingsApi = {
  get: () => fetchApi<ProxySettings>('/api/settings/proxy'),

  update: (settings: ProxySettingsUpdate) =>
    fetchApi<ProxySettings>('/api/settings/proxy', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),
};

// Retention Settings API
export const retentionSettingsApi = {
  get: () => fetchApi<RetentionSettings>('/api/settings/retention'),

  update: (settings: RetentionSettingsUpdate) =>
    fetchApi<RetentionSettings>('/api/settings/retention', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),
};

// Formatting Settings API
export const formattingSettingsApi = {
  get: () => fetchApi<FormattingSettings>('/api/settings/formatting'),

  update: (settings: FormattingSettingsUpdate) =>
    fetchApi<FormattingSettings>('/api/settings/formatting', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),
};

// General Settings API
export const generalSettingsApi = {
  get: () => fetchApi<GeneralSettings>('/api/settings/general'),

  update: (settings: GeneralSettingsUpdate) =>
    fetchApi<GeneralSettings>('/api/settings/general', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),
};

// Context Files API
export const contextApi = {
  list: () => fetchApi<ContextFile[]>('/api/context'),

  get: (id: number) => fetchApi<ContextFile>(`/api/context/${id}`),

  upload: async (file: File, filename: string, description?: string): Promise<ContextFile> => {
    const formData = new FormData();
    formData.append('file', file);
    formData.append('filename', filename);
    if (description) {
      formData.append('description', description);
    }

    const response = await fetch(`${API_BASE_URL}/api/context`, {
      method: 'POST',
      body: formData,
      headers: {
        ...getAuthHeaders(),
        // Note: Don't set Content-Type header - browser will set it with boundary
      },
    });

    // Handle 401 Unauthorized - redirect to login
    if (response.status === 401) {
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem('aiops_auth_user');
      window.location.href = '/login';
      throw new ApiError(401, 'Session expired. Please log in again.');
    }

    if (!response.ok) {
      const text = await response.text();
      let message: string;
      try {
        const json = JSON.parse(text);
        message = json.error || text || response.statusText;
      } catch {
        message = text || response.statusText;
      }
      throw new ApiError(response.status, message);
    }

    return response.json();
  },

  delete: (id: number) =>
    fetchApi<void>(`/api/context/${id}`, {
      method: 'DELETE',
    }),

  getDownloadUrl: (id: number) => {
    const token = localStorage.getItem(TOKEN_KEY);
    const base = `${API_BASE_URL}/api/context/${id}/download`;
    return token ? `${base}?token=${encodeURIComponent(token)}` : base;
  },

  validate: (text: string) =>
    fetchApi<ValidateReferencesResponse>('/api/context/validate', {
      method: 'POST',
      body: JSON.stringify({ text }),
    }),
};

// Runbooks API
export const runbooksApi = {
  list: () => fetchApi<Runbook[]>('/api/runbooks'),

  get: (id: number) => fetchApi<Runbook>(`/api/runbooks/${id}`),

  create: (data: { title: string; content: string }) =>
    fetchApi<Runbook>('/api/runbooks', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  update: (id: number, data: { title: string; content: string }) =>
    fetchApi<Runbook>(`/api/runbooks/${id}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  delete: (id: number) =>
    fetchApi<void>(`/api/runbooks/${id}`, {
      method: 'DELETE',
    }),
};

// Skill Scripts API (uses skill names, not IDs)
export const scriptsApi = {
  // List all scripts for a skill
  list: (skillName: string) =>
    fetchApi<ScriptsListResponse>(`/api/skills/${encodeURIComponent(skillName)}/scripts`),

  // Get script content
  get: (skillName: string, filename: string) =>
    fetchApi<ScriptInfo>(`/api/skills/${encodeURIComponent(skillName)}/scripts/${encodeURIComponent(filename)}`),

  // Update script content
  update: (skillName: string, filename: string, content: string) =>
    fetchApi<{ success: boolean; filename: string }>(`/api/skills/${encodeURIComponent(skillName)}/scripts/${encodeURIComponent(filename)}`, {
      method: 'PUT',
      body: JSON.stringify({ content }),
    }),

  // Delete single script
  delete: (skillName: string, filename: string) =>
    fetchApi<void>(`/api/skills/${encodeURIComponent(skillName)}/scripts/${encodeURIComponent(filename)}`, {
      method: 'DELETE',
    }),

  // Delete all scripts
  deleteAll: (skillName: string) =>
    fetchApi<{ message: string; skill_name: string }>(`/api/skills/${encodeURIComponent(skillName)}/scripts`, {
      method: 'DELETE',
    }),
};

// Integrations API
export const integrationsApi = {
  list: () => fetchApi<Integration[]>('/api/integrations'),

  get: (uuid: string) => fetchApi<Integration>(`/api/integrations/${uuid}`),

  create: (data: CreateIntegrationRequest) =>
    fetchApi<Integration>('/api/integrations', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  update: (uuid: string, data: UpdateIntegrationRequest) =>
    fetchApi<Integration>(`/api/integrations/${uuid}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  delete: (uuid: string) =>
    fetchApi<void>(`/api/integrations/${uuid}`, {
      method: 'DELETE',
    }),
};

// Channels API
export const channelsApi = {
  list: (filter?: ListChannelsFilter) => {
    const params = new URLSearchParams();
    if (filter?.integration_uuid) params.set('integration_uuid', filter.integration_uuid);
    if (filter?.can_post !== undefined) params.set('can_post', String(filter.can_post));
    if (filter?.can_listen !== undefined) params.set('can_listen', String(filter.can_listen));
    const qs = params.toString();
    return fetchApi<Channel[]>(`/api/channels${qs ? `?${qs}` : ''}`);
  },

  get: (uuid: string) => fetchApi<Channel>(`/api/channels/${uuid}`),

  create: (data: CreateChannelRequest) =>
    fetchApi<Channel>('/api/channels', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  update: (uuid: string, data: UpdateChannelRequest) =>
    fetchApi<Channel>(`/api/channels/${uuid}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  delete: (uuid: string) =>
    fetchApi<void>(`/api/channels/${uuid}`, {
      method: 'DELETE',
    }),
};

// Alert Source Types API
export const alertSourceTypesApi = {
  list: () => fetchApi<AlertSourceType[]>('/api/alert-source-types'),
};

// Alert Sources API (instances)
export const alertSourcesApi = {
  list: () => fetchApi<AlertSourceInstance[]>('/api/alert-sources'),

  get: (uuid: string) => fetchApi<AlertSourceInstance>(`/api/alert-sources/${uuid}`),

  create: (data: CreateAlertSourceRequest) =>
    fetchApi<AlertSourceInstance>('/api/alert-sources', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  update: (uuid: string, data: UpdateAlertSourceRequest) =>
    fetchApi<AlertSourceInstance>(`/api/alert-sources/${uuid}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  delete: (uuid: string) =>
    fetchApi<void>(`/api/alert-sources/${uuid}`, {
      method: 'DELETE',
    }),

  getWebhookUrl: (uuid: string) => {
    const baseUrl = API_BASE_URL || window.location.origin;
    return `${baseUrl}/webhook/alert/${uuid}`;
  },
};

// Cron Jobs API
export const cronJobsApi = {
  list: () => fetchApi<CronJob[]>('/api/cron-jobs'),

  get: (uuid: string) => fetchApi<CronJob>(`/api/cron-jobs/${uuid}`),

  create: (data: CreateCronJobRequest) =>
    fetchApi<CronJob>('/api/cron-jobs', {
      method: 'POST',
      body: JSON.stringify(data),
    }),

  update: (uuid: string, data: UpdateCronJobRequest) =>
    fetchApi<CronJob>(`/api/cron-jobs/${uuid}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    }),

  delete: (uuid: string) =>
    fetchApi<void>(`/api/cron-jobs/${uuid}`, {
      method: 'DELETE',
    }),

  run: (uuid: string) =>
    fetchApi<{ status: string }>(`/api/cron-jobs/${uuid}/run`, {
      method: 'POST',
    }),
};

// Memories API
export const memoriesApi = {
  list: (params?: { scope?: string; type?: string }) => {
    const qs = new URLSearchParams();
    if (params?.scope) qs.set('scope', params.scope);
    if (params?.type) qs.set('type', params.type);
    const query = qs.toString();
    return fetchApi<Memory[]>(`/api/memories${query ? '?' + query : ''}`);
  },

  get: (id: number) => fetchApi<Memory>(`/api/memories/${id}`),
};

// Events feed API
export const eventsApi = {
  list: (params: { from?: number; to?: number; page?: number; perPage?: number; type?: string; search?: string }) => {
    const qs = new URLSearchParams();
    if (params.from !== undefined) qs.set('from', String(params.from));
    if (params.to !== undefined) qs.set('to', String(params.to));
    qs.set('page', String(params.page ?? 1));
    qs.set('per_page', String(params.perPage ?? 50));
    if (params.type) qs.set('type', params.type);
    if (params.search) qs.set('search', params.search);
    return fetchApi<PaginatedResponse<EventFeedItem>>(`/api/events?${qs.toString()}`);
  },

  // Lightweight incident response projection for the Feed's inline expansion.
  incidentResponse: (uuid: string) =>
    fetchApi<{ uuid: string; title: string; status: string; response: string }>(
      `/api/incidents/${encodeURIComponent(uuid)}/response`,
    ),

  // Fetch the raw payload / original message for a single feed event on demand.
  raw: (type: string, uuid: string) =>
    fetchApi<{ raw?: unknown; original_message?: string }>(
      `/api/events/raw?type=${encodeURIComponent(type)}&uuid=${encodeURIComponent(uuid)}`,
    ),
};

// Alerts API
export const alertsApi = {
  unlink: (uuid: string) =>
    fetchApi<{ incident_uuid: string }>(`/api/alerts/${encodeURIComponent(uuid)}/unlink`, {
      method: 'POST',
    }),

  // Reassign an alert to a different incident. An empty/omitted target spawns a
  // fresh investigation (equivalent to unlink); a target UUID links the alert to
  // that existing incident.
  move: (uuid: string, targetIncidentUUID?: string) =>
    fetchApi<{ incident_uuid: string }>(`/api/alerts/${encodeURIComponent(uuid)}/move`, {
      method: 'POST',
      body: JSON.stringify({ target_incident_uuid: targetIncidentUUID ?? '' }),
    }),

  // Manually mark a firing alert resolved.
  resolve: (uuid: string) =>
    fetchApi<{ status: string }>(`/api/alerts/${encodeURIComponent(uuid)}/resolve`, {
      method: 'POST',
    }),
};

export { ApiError };
