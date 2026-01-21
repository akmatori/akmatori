import type {
  Skill,
  ToolType,
  ToolInstance,
  Incident,
  IncidentAlert,
  AggregationSettings,
  SlackSettings,
  SlackSettingsUpdate,
  OpenAISettings,
  OpenAISettingsUpdate,
  ProxySettings,
  ProxySettingsUpdate,
  DeviceAuthStartResponse,
  DeviceAuthStatusResponse,
  ContextFile,
  ValidateReferencesResponse,
  CreateIncidentRequest,
  CreateIncidentResponse,
  ScriptsListResponse,
  ScriptInfo,
  AlertSourceType,
  AlertSourceInstance,
  CreateAlertSourceRequest,
  UpdateAlertSourceRequest,
  SSHKey,
  SSHKeyCreateRequest,
  SSHKeyUpdateRequest,
} from '../types';

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || '';
const TOKEN_KEY = 'aiops_auth_token';

class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = 'ApiError';
  }
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

  if (!response.ok) {
    const text = await response.text();
    throw new ApiError(response.status, text || response.statusText);
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

  // Update tools assigned to a skill (triggers symlink + SKILL.md regeneration)
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

  create: (tool: { tool_type_id: number; name: string; settings: Record<string, any> }) =>
    fetchApi<ToolInstance>('/api/tools', {
      method: 'POST',
      body: JSON.stringify(tool),
    }),

  update: (id: number, tool: { name: string; settings: Record<string, any>; enabled: boolean }) =>
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
  list: (from?: number, to?: number) => {
    const params = new URLSearchParams();
    if (from !== undefined) params.set('from', String(from));
    if (to !== undefined) params.set('to', String(to));
    const query = params.toString() ? `?${params.toString()}` : '';
    return fetchApi<Incident[]>(`/api/incidents${query}`);
  },

  get: (uuid: string) => fetchApi<Incident>(`/api/incidents/${uuid}`),

  create: (request: CreateIncidentRequest) =>
    fetchApi<CreateIncidentResponse>('/api/incidents', {
      method: 'POST',
      body: JSON.stringify(request),
    }),
};

// Slack Settings API
export const slackSettingsApi = {
  get: () => fetchApi<SlackSettings>('/api/settings/slack'),

  update: (settings: SlackSettingsUpdate) =>
    fetchApi<SlackSettings>('/api/settings/slack', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),
};

// OpenAI Settings API
export const openaiSettingsApi = {
  get: () => fetchApi<OpenAISettings>('/api/settings/openai'),

  update: (settings: OpenAISettingsUpdate) =>
    fetchApi<OpenAISettings>('/api/settings/openai', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),

  // Device Auth for ChatGPT Subscription
  startDeviceAuth: () =>
    fetchApi<DeviceAuthStartResponse>('/api/settings/openai/device-auth/start', {
      method: 'POST',
    }),

  getDeviceAuthStatus: (deviceCode: string) =>
    fetchApi<DeviceAuthStatusResponse>(`/api/settings/openai/device-auth/status?device_code=${encodeURIComponent(deviceCode)}`),

  cancelDeviceAuth: () =>
    fetchApi<{ success: boolean; message: string }>('/api/settings/openai/device-auth/cancel', {
      method: 'POST',
    }),

  disconnectChatGPT: () =>
    fetchApi<{ success: boolean; message: string }>('/api/settings/openai/chatgpt/disconnect', {
      method: 'POST',
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

// Aggregation Settings API
export const aggregationSettingsApi = {
  get: () => fetchApi<AggregationSettings>('/api/settings/aggregation'),

  update: (settings: Partial<AggregationSettings>) =>
    fetchApi<AggregationSettings>('/api/settings/aggregation', {
      method: 'PUT',
      body: JSON.stringify(settings),
    }),
};

// Incident Alerts API (for alert aggregation)
export const incidentAlertsApi = {
  list: (incidentUuid: string) =>
    fetchApi<IncidentAlert[]>(`/api/incidents/${incidentUuid}/alerts`),

  attach: (incidentUuid: string, alert: Partial<IncidentAlert>) =>
    fetchApi<IncidentAlert>(`/api/incidents/${incidentUuid}/alerts`, {
      method: 'POST',
      body: JSON.stringify(alert),
    }),

  detach: (incidentUuid: string, alertId: number) =>
    fetchApi<void>(`/api/incidents/${incidentUuid}/alerts/${alertId}`, {
      method: 'DELETE',
    }),

  merge: (targetUuid: string, sourceUuid: string) =>
    fetchApi<void>(`/api/incidents/${targetUuid}/merge`, {
      method: 'POST',
      body: JSON.stringify({ source_incident_uuid: sourceUuid }),
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
      throw new ApiError(response.status, text || response.statusText);
    }

    return response.json();
  },

  delete: (id: number) =>
    fetchApi<void>(`/api/context/${id}`, {
      method: 'DELETE',
    }),

  getDownloadUrl: (id: number) => `${API_BASE_URL}/api/context/${id}/download`,

  validate: (text: string) =>
    fetchApi<ValidateReferencesResponse>('/api/context/validate', {
      method: 'POST',
      body: JSON.stringify({ text }),
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

export { ApiError };
