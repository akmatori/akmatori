import type { MCPAuthType } from '../../types/connectors';
import type { MCPAuthForm } from './mcpAuthForm';

interface Props {
  value: MCPAuthForm;
  onChange: (v: MCPAuthForm) => void;
  onTypeChange: (t: MCPAuthType) => void;
  // On edit, secrets are stored: empty fields keep them.
  hasStoredSecret?: boolean;
}

const OPTIONS: { value: MCPAuthType; label: string }[] = [
  { value: 'none', label: 'None' },
  { value: 'oauth', label: 'OAuth (sign in)' },
  { value: 'bearer', label: 'Bearer token' },
  { value: 'header', label: 'API key header' },
];

export default function MCPAuthFields({ value, onChange, onTypeChange, hasStoredSecret }: Props) {
  const keepHint = hasStoredSecret ? 'Leave empty to keep the stored value.' : undefined;
  return (
    <div className="space-y-3">
      <div>
        <label htmlFor="mcp-auth" className="block text-sm font-medium text-fg mb-1">Authentication</label>
        <select id="mcp-auth" className="input-field" value={value.type} onChange={(e) => onTypeChange(e.target.value as MCPAuthType)}>
          {OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>
      </div>

      {value.type === 'bearer' && (
        <div>
          <label htmlFor="mcp-token" className="block text-sm font-medium text-fg mb-1">Token</label>
          <input id="mcp-token" type="password" autoComplete="off" className="input-field" value={value.token}
            onChange={(e) => onChange({ ...value, token: e.target.value })} placeholder={hasStoredSecret ? '••••••••' : ''} />
          <p className="mt-1 text-xs text-fg-subtle">Sent as <code>Authorization: Bearer …</code>. {keepHint}</p>
        </div>
      )}

      {value.type === 'header' && (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div>
            <label htmlFor="mcp-hname" className="block text-sm font-medium text-fg mb-1">Header name</label>
            <input id="mcp-hname" className="input-field" value={value.headerName}
              onChange={(e) => onChange({ ...value, headerName: e.target.value })} />
          </div>
          <div>
            <label htmlFor="mcp-hvalue" className="block text-sm font-medium text-fg mb-1">Value</label>
            <input id="mcp-hvalue" type="password" autoComplete="off" className="input-field" value={value.headerValue}
              onChange={(e) => onChange({ ...value, headerValue: e.target.value })} placeholder={hasStoredSecret ? '••••••••' : ''} />
          </div>
          {keepHint && <p className="sm:col-span-2 -mt-1 text-xs text-fg-subtle">{keepHint}</p>}
        </div>
      )}

      {value.type === 'oauth' && (
        <details className="rounded-md border border-line px-3 py-2">
          <summary className="text-sm text-fg-muted cursor-pointer">Advanced: OAuth client</summary>
          <p className="mt-2 text-xs text-fg-subtle">
            Leave empty to register Akmatori with the server automatically (dynamic client registration). Set a client
            when the server requires one. The redirect URL is <code>&lt;External URL&gt;/api/mcp-servers/oauth/callback</code>.
          </p>
          <div className="mt-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label htmlFor="mcp-cid" className="block text-sm font-medium text-fg mb-1">Client ID</label>
              <input id="mcp-cid" className="input-field" value={value.clientId}
                onChange={(e) => onChange({ ...value, clientId: e.target.value })} />
            </div>
            <div>
              <label htmlFor="mcp-csecret" className="block text-sm font-medium text-fg mb-1">Client secret</label>
              <input id="mcp-csecret" type="password" autoComplete="off" className="input-field" value={value.clientSecret}
                onChange={(e) => onChange({ ...value, clientSecret: e.target.value })} />
            </div>
            <div className="sm:col-span-2">
              <label htmlFor="mcp-scope" className="block text-sm font-medium text-fg mb-1">Scope</label>
              <input id="mcp-scope" className="input-field" value={value.scope}
                onChange={(e) => onChange({ ...value, scope: e.target.value })} placeholder="Server default" />
            </div>
          </div>
        </details>
      )}
    </div>
  );
}
