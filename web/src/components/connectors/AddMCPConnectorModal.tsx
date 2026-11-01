import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AlertTriangle, CheckCircle2, Loader2, X } from 'lucide-react';
import { mcpServersApi } from '../../api/connectors';
import { getErrorMessage } from '../../utils/errors';
import type { MCPAuthConfigInput, MCPAuthType, MCPTestResult } from '../../types/connectors';
import { isValidRemoteURL, slugNamespace } from './connectorHelpers';
import MCPAuthFields from './MCPAuthFields';
import { authInputFromForm, EMPTY_AUTH_FORM, type MCPAuthForm } from './mcpAuthForm';
import MCPTestResultView from './MCPTestResultView';

interface Props {
  onClose: () => void;
}

// AddMCPConnectorModal adds a remote MCP server as a custom connector. Only
// remote servers (an http(s) URL) are supported.
export default function AddMCPConnectorModal({ onClose }: Props) {
  const navigate = useNavigate();
  const [name, setName] = useState('');
  const [url, setUrl] = useState('');
  const [auth, setAuth] = useState<MCPAuthForm>(EMPTY_AUTH_FORM);
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [test, setTest] = useState<MCPTestResult | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !saving) onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [saving, onClose]);

  const urlOK = isValidRemoteURL(url);
  const authInput: MCPAuthConfigInput | undefined = authInputFromForm(auth);
  const canContinue = name.trim() !== '' && urlOK && !saving;

  const runTest = async () => {
    setTesting(true);
    setError('');
    setTest(null);
    try {
      // OAuth needs a saved server first; test without auth to see the challenge.
      setTest(await mcpServersApi.testNew(url.trim(), auth.type === 'oauth' ? undefined : authInput));
    } catch (err) {
      setError(getErrorMessage(err, 'Connection test failed'));
    } finally {
      setTesting(false);
    }
  };

  const save = async () => {
    setSaving(true);
    setError('');
    try {
      const created = await mcpServersApi.create({
        name: name.trim(),
        url: url.trim(),
        transport: 'http',
        auth_config: authInput,
      });
      if (auth.type === 'oauth') {
        const { authorize_url } = await mcpServersApi.oauthStart(created.id, test?.www_authenticate);
        window.location.assign(authorize_url);
        return;
      }
      navigate(created.connector_uuid ? `/connectors/${created.connector_uuid}` : '/connectors');
      onClose();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to add the connector'));
      setSaving(false);
    }
  };

  const setAuthType = (type: MCPAuthType) => setAuth({ ...auth, type });

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="add-mcp-title">
      <div className="fixed inset-0 bg-black/50" onClick={saving ? undefined : onClose} />
      <div className="flex min-h-full items-center justify-center p-4">
        <div className="relative w-full max-w-xl bg-surface rounded-xl shadow-xl border border-line">
          <div className="flex items-center justify-between px-6 py-4 border-b border-line">
            <h2 id="add-mcp-title" className="text-base font-semibold text-fg">Add custom connector</h2>
            <button onClick={onClose} disabled={saving} className="text-fg-subtle hover:text-fg disabled:opacity-50" aria-label="Close">
              <X className="w-5 h-5" />
            </button>
          </div>

          <div className="px-6 py-5 space-y-5">
            <p className="text-sm text-fg-muted">
              Connect the agent to a remote MCP server. After you add it, assign it to the skills (or cron jobs) that may use its tools.
            </p>

            <div>
              <label htmlFor="mcp-name" className="block text-sm font-medium text-fg mb-1">Name</label>
              <input id="mcp-name" className="input-field" value={name} onChange={(e) => setName(e.target.value)} placeholder="GitHub" autoFocus />
              <p className="mt-1 text-xs text-fg-subtle">
                Shown in the connectors list. Tools are named <code>{slugNamespace(name)}.&lt;tool&gt;</code>.
              </p>
            </div>

            <div>
              <label htmlFor="mcp-url" className="block text-sm font-medium text-fg mb-1">MCP server URL</label>
              <input
                id="mcp-url"
                className="input-field"
                value={url}
                onChange={(e) => { setUrl(e.target.value); setTest(null); }}
                placeholder="https://mcp.example.com/mcp"
                inputMode="url"
              />
              <p className={`mt-1 text-xs ${url && !urlOK ? 'text-state-fail' : 'text-fg-subtle'}`}>
                The address where the server accepts MCP requests (Streamable HTTP), for example https://mcp.example.com/mcp.
              </p>
            </div>

            <MCPAuthFields value={auth} onChange={(a) => { setAuth(a); setTest(null); }} onTypeChange={setAuthType} />

            <div className="flex items-center gap-3">
              <button type="button" className="btn btn-secondary" onClick={runTest} disabled={!urlOK || testing}>
                {testing ? <Loader2 className="w-4 h-4 animate-spin" /> : <CheckCircle2 className="w-4 h-4" />}
                Test connection
              </button>
              {auth.type === 'oauth' && (
                <span className="text-xs text-fg-subtle">You sign in to the server after you continue.</span>
              )}
            </div>
            {test && <MCPTestResultView result={test} oauthSelected={auth.type === 'oauth'} onUseOAuth={() => setAuthType('oauth')} />}

            {error && <p className="text-sm text-state-fail">{error}</p>}

            <div className="flex gap-2.5 p-3 rounded-md bg-surface-2 border border-line text-sm text-fg-muted">
              <AlertTriangle className="w-4 h-4 mt-0.5 flex-shrink-0 text-state-need" aria-hidden="true" />
              <p>
                Only use connectors from developers you trust. Akmatori does not control which tools a server makes
                available and cannot verify that they work as intended or that they won't change. The agent can call
                every tool you leave enabled.
              </p>
            </div>
          </div>

          <div className="flex justify-end gap-2 px-6 py-4 border-t border-line">
            <button className="btn btn-secondary" onClick={onClose} disabled={saving}>Cancel</button>
            <button className="btn btn-primary" onClick={save} disabled={!canContinue}>
              {saving && <Loader2 className="w-4 h-4 animate-spin" />}
              {auth.type === 'oauth' ? 'Continue and sign in' : 'Continue'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
