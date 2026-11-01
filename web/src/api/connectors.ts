import { fetchApi } from './client';
import type {
  ConnectorCatalogEntry,
  ConnectorDetail,
  ConnectorPartKind,
  ConnectorSummary,
  CreateMCPServerRequest,
  MCPAuthConfigInput,
  MCPServer,
  MCPTestResult,
  UpdateMCPServerRequest,
} from '../types/connectors';

export const connectorsApi = {
  list: () => fetchApi<ConnectorSummary[]>('/api/connectors'),
  catalog: () => fetchApi<ConnectorCatalogEntry[]>('/api/connectors/catalog'),
  get: (uuid: string) => fetchApi<ConnectorDetail>(`/api/connectors/${encodeURIComponent(uuid)}`),
  rename: (uuid: string, name: string) =>
    fetchApi<ConnectorDetail>(`/api/connectors/${encodeURIComponent(uuid)}`, {
      method: 'PATCH',
      body: JSON.stringify({ name }),
    }),
  delete: (uuid: string) =>
    fetchApi<void>(`/api/connectors/${encodeURIComponent(uuid)}`, { method: 'DELETE' }),
  attach: (uuid: string, kind: ConnectorPartKind, id: number) =>
    fetchApi<ConnectorDetail>(`/api/connectors/${encodeURIComponent(uuid)}/parts`, {
      method: 'POST',
      body: JSON.stringify({ kind, id }),
    }),
  detach: (uuid: string, kind: ConnectorPartKind, id: number) =>
    fetchApi<{ connector_uuid: string }>(
      `/api/connectors/${encodeURIComponent(uuid)}/parts/${kind}/${id}`,
      { method: 'DELETE' },
    ),
  lookup: (kind: ConnectorPartKind, id: number) =>
    fetchApi<{ connector_uuid: string }>(
      `/api/connectors/lookup?kind=${encodeURIComponent(kind)}&id=${id}`,
    ),
};

export const mcpServersApi = {
  list: () => fetchApi<MCPServer[]>('/api/mcp-servers'),
  get: (id: number) => fetchApi<MCPServer>(`/api/mcp-servers/${id}`),
  create: (req: CreateMCPServerRequest) =>
    fetchApi<MCPServer>('/api/mcp-servers', { method: 'POST', body: JSON.stringify(req) }),
  update: (id: number, req: UpdateMCPServerRequest) =>
    fetchApi<MCPServer>(`/api/mcp-servers/${id}`, { method: 'PUT', body: JSON.stringify(req) }),
  delete: (id: number) => fetchApi<void>(`/api/mcp-servers/${id}`, { method: 'DELETE' }),
  // Test unsaved settings.
  testNew: (url: string, auth_config?: MCPAuthConfigInput) =>
    fetchApi<MCPTestResult>('/api/mcp-servers/test', {
      method: 'POST',
      body: JSON.stringify({ url, transport: 'http', auth_config }),
    }),
  // Test a stored server; omitted fields use the stored values.
  test: (id: number, overrides?: { url?: string; auth_config?: MCPAuthConfigInput }) =>
    fetchApi<MCPTestResult>(`/api/mcp-servers/${id}/test`, {
      method: 'POST',
      body: JSON.stringify(overrides ?? {}),
    }),
  oauthStart: (id: number, www_authenticate?: string) =>
    fetchApi<{ authorize_url: string }>(`/api/mcp-servers/${id}/oauth/start`, {
      method: 'POST',
      body: JSON.stringify({ www_authenticate: www_authenticate ?? '' }),
    }),
  oauthDisconnect: (id: number) =>
    fetchApi<void>(`/api/mcp-servers/${id}/oauth/disconnect`, { method: 'POST' }),
};
