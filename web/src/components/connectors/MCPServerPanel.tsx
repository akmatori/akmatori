import { useCallback, useEffect, useState } from 'react';
import { AlertTriangle, CheckCircle2, KeyRound, Loader2, LogOut, RefreshCw, Save } from 'lucide-react';
import { mcpServersApi } from '../../api/connectors';
import { getErrorMessage } from '../../utils/errors';
import { formatRelativeAgo } from '../../utils/format';
import type { ConnectorMCPPart, MCPServer, MCPTestResult, MCPTestTool } from '../../types/connectors';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import MCPAuthFields from './MCPAuthFields';
import { authInputFromForm, EMPTY_AUTH_FORM, type MCPAuthForm } from './mcpAuthForm';
import { isValidRemoteURL } from './connectorHelpers';

interface Props {
  part: ConnectorMCPPart;
  onChange: () => void;
}

function formFromServer(s: MCPServer): MCPAuthForm {
  const cfg = s.auth_config ?? {};
  const str = (k: string) => (typeof cfg[k] === 'string' ? (cfg[k] as string) : '');
  return {
    ...EMPTY_AUTH_FORM,
    type: s.auth.type,
    headerName: s.auth.header_name || EMPTY_AUTH_FORM.headerName,
    clientId: str('client_id'),
    scope: str('scope'),
  };
}

// MCPServerPanel edits a custom MCP connector: URL, authentication, OAuth
// sign-in, and which of the server's tools the agent may use.
export default function MCPServerPanel({ part, onChange }: Props) {
  const [server, setServer] = useState<MCPServer | null>(null);
  const [url, setUrl] = useState('');
  const [auth, setAuth] = useState<MCPAuthForm>(EMPTY_AUTH_FORM);
  const [tools, setTools] = useState<MCPTestTool[] | null>(null);
  const [test, setTest] = useState<MCPTestResult | null>(null);
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [toggling, setToggling] = useState<string | null>(null);

  const runTest = useCallback(async () => {
    setTesting(true);
    try {
      const res = await mcpServersApi.test(part.id);
      setTest(res);
      if (res.ok) setTools(res.tools ?? []);
    } catch (err) {
      setTest({ ok: false, error: getErrorMessage(err, 'Connection test failed') });
    } finally {
      setTesting(false);
    }
  }, [part.id]);

  const load = useCallback(async () => {
    try {
      const s = await mcpServersApi.get(part.id);
      setServer(s);
      setUrl(s.url ?? '');
      setAuth(formFromServer(s));
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load the MCP server'));
    }
  }, [part.id]);

  useEffect(() => {
    void load();
    void runTest();
  }, [load, runTest]);

  if (!server) return error ? <ErrorMessage message={error} /> : <LoadingSpinner />;

  const disabled = new Set(server.disabled_tools);
  const authChanged =
    auth.type !== server.auth.type ||
    auth.token !== '' ||
    auth.headerValue !== '' ||
    auth.clientSecret !== '' ||
    (auth.type === 'header' && auth.headerName !== (server.auth.header_name ?? '')) ||
    (auth.type === 'oauth' && (auth.clientId !== formFromServer(server).clientId || auth.scope !== formFromServer(server).scope));
  const dirty = url.trim() !== (server.url ?? '') || authChanged;

  const save = async () => {
    setSaving(true);
    setError('');
    setNotice('');
    try {
      const next = await mcpServersApi.update(server.id, {
        url: url.trim(),
        ...(authChanged ? { auth_config: auth.type === 'none' ? null : authInputFromForm(auth) } : {}),
      });
      setServer(next);
      setAuth(formFromServer(next));
      setNotice('Saved. The gateway reconnects with the new settings.');
      onChange();
      void runTest();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to save'));
    } finally {
      setSaving(false);
    }
  };

  // One toggle at a time: each save sends the whole list, so a second click
  // before the first answer would overwrite it.
  const toggleTool = async (name: string) => {
    if (toggling) return;
    const next = new Set(disabled);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    setToggling(name);
    try {
      const s = await mcpServersApi.update(server.id, { disabled_tools: [...next].sort() });
      setServer(s);
      onChange();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to update the tool'));
    } finally {
      setToggling(null);
    }
  };

  const toggleEnabled = async () => {
    try {
      const s = await mcpServersApi.update(server.id, { enabled: !server.enabled });
      setServer(s);
      onChange();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to update'));
    }
  };

  const signIn = async () => {
    setError('');
    try {
      const { authorize_url } = await mcpServersApi.oauthStart(server.id, test?.www_authenticate);
      window.location.assign(authorize_url);
    } catch (err) {
      setError(getErrorMessage(err, 'Could not start the sign-in'));
    }
  };

  const signOut = async () => {
    try {
      await mcpServersApi.oauthDisconnect(server.id);
      await load();
      onChange();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to sign out'));
    }
  };

  const oauthError = typeof server.auth_config?.oauth_error === 'string' ? (server.auth_config.oauth_error as string) : '';
  const runtime = part.runtime;

  return (
    <div className="space-y-6">
      {server.unsupported && (
        <div className="flex gap-2.5 p-3 rounded-md border border-state-fail/40 bg-state-fail/5 text-sm text-fg">
          <AlertTriangle className="w-4 h-4 mt-0.5 text-state-fail flex-shrink-0" aria-hidden="true" />
          <p>{server.unsupported}</p>
        </div>
      )}
      {error && <ErrorMessage message={error} />}
      {notice && <p className="text-sm text-state-ok">{notice}</p>}

      <section className="card space-y-4">
        <div className="flex items-center justify-between gap-3 flex-wrap">
          <h2 className="text-sm font-semibold text-fg">Connection</h2>
          <label className="flex items-center gap-2 text-sm text-fg-muted cursor-pointer">
            <input type="checkbox" checked={server.enabled} onChange={toggleEnabled} />
            Enabled
          </label>
        </div>
        <div>
          <label htmlFor="mcp-edit-url" className="block text-sm font-medium text-fg mb-1">MCP server URL</label>
          <input id="mcp-edit-url" className="input-field" value={url} onChange={(e) => setUrl(e.target.value)} />
          {url && !isValidRemoteURL(url) && <p className="mt-1 text-xs text-state-fail">Enter an absolute http(s) URL.</p>}
        </div>
        <MCPAuthFields
          value={auth}
          onChange={setAuth}
          onTypeChange={(t) => setAuth({ ...auth, type: t })}
          hasStoredSecret={server.auth.has_secret && auth.type === server.auth.type}
        />
        {server.auth.type === 'oauth' && auth.type === 'oauth' && (
          <div className="flex items-center gap-3 flex-wrap text-sm">
            {server.auth.oauth_connected ? (
              <>
                <span className="flex items-center gap-1.5 text-fg">
                  <CheckCircle2 className="w-4 h-4 text-state-ok" aria-hidden="true" /> Signed in
                  {server.auth.oauth_scope && <span className="text-fg-subtle">({server.auth.oauth_scope})</span>}
                </span>
                <button type="button" className="btn btn-ghost" onClick={signIn}><RefreshCw className="w-4 h-4" />Sign in again</button>
                <button type="button" className="btn btn-ghost" onClick={signOut}><LogOut className="w-4 h-4" />Sign out</button>
              </>
            ) : (
              <>
                <span className="text-fg-muted">{oauthError || 'Not signed in.'}</span>
                <button type="button" className="btn btn-primary" onClick={signIn}><KeyRound className="w-4 h-4" />Sign in</button>
              </>
            )}
          </div>
        )}
        <div className="flex gap-2">
          <button className="btn btn-primary" onClick={save} disabled={!dirty || saving || !isValidRemoteURL(url)}>
            {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
            Save
          </button>
          <button className="btn btn-secondary" onClick={runTest} disabled={testing}>
            {testing ? <Loader2 className="w-4 h-4 animate-spin" /> : <RefreshCw className="w-4 h-4" />}
            Test connection
          </button>
        </div>
        {test && !test.ok && (
          <p className="text-sm text-state-fail">
            {test.auth_required ? 'The server requires authentication. Choose OAuth or a token above.' : test.error}
          </p>
        )}
        {runtime && (
          <p className="text-xs text-fg-subtle">
            Gateway: {runtime.connected ? `connected, ${runtime.tool_count} tools` : runtime.error || 'not connected'}
            {runtime.checked_at && ` · checked ${formatRelativeAgo(runtime.checked_at)}`}
          </p>
        )}
      </section>

      <section className="card space-y-3">
        <div>
          <h2 className="text-sm font-semibold text-fg">Tools</h2>
          <p className="text-xs text-fg-subtle mt-1">
            Turn off tools the agent must not call. The agent calls them as <code>{server.namespace_prefix}.&lt;tool&gt;</code>,
            and only in skills or cron jobs that this connector is assigned to (Skills → edit → Tools).
          </p>
        </div>
        {tools === null ? (
          testing ? <LoadingSpinner /> : <p className="text-sm text-fg-muted">Connect to the server to see its tools.</p>
        ) : tools.length === 0 ? (
          <p className="text-sm text-fg-muted">The server lists no tools.</p>
        ) : (
          <ul className="divide-y divide-line">
            {tools.map((t) => (
              <li key={t.name} className="flex items-start justify-between gap-4 py-2.5">
                <div className="min-w-0">
                  <code className="text-sm text-fg break-all">{t.name}</code>
                  {t.description && <p className="text-xs text-fg-subtle mt-0.5 line-clamp-2">{t.description}</p>}
                </div>
                <label className="flex items-center gap-2 text-xs text-fg-muted cursor-pointer flex-shrink-0">
                  <input
                    type="checkbox"
                    checked={toggling === t.name ? disabled.has(t.name) : !disabled.has(t.name)}
                    disabled={toggling !== null}
                    onChange={() => toggleTool(t.name)}
                    aria-label={`Allow ${t.name}`}
                  />
                  {disabled.has(t.name) ? 'Off' : 'On'}
                </label>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
