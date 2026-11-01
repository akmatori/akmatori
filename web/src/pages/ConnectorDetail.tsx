import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { ArrowLeft, CheckCircle2, Pencil, Trash2, XCircle } from 'lucide-react';
import { connectorsApi } from '../api/connectors';
import { getErrorMessage } from '../utils/errors';
import { useLatestRef } from '../hooks/useLatestRef';
import type { ConnectorDetail as Detail, ConnectorSummary } from '../types/connectors';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import ConnectorTile from '../components/connectors/ConnectorTile';
import ToolsSection from '../components/connectors/ToolsSection';
import AlertSourcesManager from '../components/AlertSourcesManager';
import ChannelsManager from '../components/channels/ChannelsManager';
import MCPServerPanel from '../components/connectors/MCPServerPanel';
import FormattingPanel from '../components/connectors/FormattingPanel';
import IntegrationPanel from '../components/connectors/IntegrationPanel';
import ConnectorPartsPanel from '../components/connectors/ConnectorPartsPanel';
import type { PartScope } from '../components/connectors/partScope';
import { CAPABILITY_LABELS, KIND_LABELS, statusBadgeClass, statusView } from '../components/connectors/connectorHelpers';

type TabID = 'tools' | 'alerts' | 'messaging' | 'formatting' | 'settings';

interface TabDef {
  id: TabID;
  label: string;
}

function tabsFor(d: Detail): TabDef[] {
  const caps = new Set(d.capabilities);
  const tabs: TabDef[] = [];
  if (caps.has('tools')) tabs.push({ id: 'tools', label: d.kind === 'custom_mcp' ? 'Server' : 'Tools' });
  if (caps.has('messaging')) tabs.push({ id: 'messaging', label: 'Channels' });
  if (caps.has('alerts')) tabs.push({ id: 'alerts', label: 'Alerts' });
  if (caps.has('messaging') || caps.has('alerts')) tabs.push({ id: 'formatting', label: 'Formatting' });
  tabs.push({ id: 'settings', label: 'Settings' });
  return tabs;
}

export default function ConnectorDetail() {
  const { uuid = '' } = useParams<{ uuid: string }>();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [detail, setDetail] = useState<Detail | null>(null);
  const [siblings, setSiblings] = useState<ConnectorSummary[]>([]);
  const [error, setError] = useState('');
  const [renaming, setRenaming] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);

  const load = useCallback(async () => {
    try {
      const [d, all] = await Promise.all([connectorsApi.get(uuid), connectorsApi.list()]);
      setDetail(d);
      setSiblings(all.filter((c) => c.catalog_id === d.catalog_id && c.uuid !== d.uuid));
      setError('');
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load the connector'));
    }
  }, [uuid]);

  // Load on mount and when the route moves to another connector.
  const loadRef = useLatestRef(load);
  useEffect(() => {
    void loadRef.current();
  }, [loadRef, uuid]);

  const tabs = useMemo(() => (detail ? tabsFor(detail) : []), [detail]);
  const requested = params.get('tab') as TabID | null;
  const active: TabID = tabs.find((t) => t.id === requested)?.id ?? tabs[0]?.id ?? 'settings';

  const scope: PartScope | undefined = useMemo(
    () =>
      detail
        ? {
            connectorId: detail.id,
            connectorUUID: detail.uuid,
            allowedTypes: active === 'alerts' ? detail.catalog?.alert_source_types ?? [] : detail.catalog?.tool_types ?? [],
            onChange: () => void load(),
          }
        : undefined,
    [detail, active, load],
  );

  if (error && !detail) {
    return (
      <div className="space-y-4">
        <Link to="/connectors" className="text-sm text-fg-muted hover:text-fg inline-flex items-center gap-1"><ArrowLeft className="w-4 h-4" />Connectors</Link>
        <ErrorMessage message={error} />
      </div>
    );
  }
  if (!detail || !scope) return <LoadingSpinner />;

  const st = statusView(detail.status.state);
  const oauthOK = params.get('oauth') === 'connected';
  const oauthErr = params.get('oauth_error');
  const clearOAuthParams = () => {
    const next = new URLSearchParams(params);
    next.delete('oauth');
    next.delete('oauth_error');
    setParams(next, { replace: true });
  };
  const setTab = (t: TabID) => {
    const next = new URLSearchParams(params);
    next.set('tab', t);
    setParams(next, { replace: true });
  };

  const saveName = async () => {
    if (renaming === null) return;
    try {
      const d = await connectorsApi.rename(detail.uuid, renaming);
      setDetail(d);
      setRenaming(null);
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to rename'));
    }
  };

  const doDelete = async () => {
    setDeleting(true);
    try {
      await connectorsApi.delete(detail.uuid);
      navigate('/connectors');
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to delete the connector'));
      setDeleting(false);
      setConfirmDelete(false);
      void load();
    }
  };

  const partCount =
    detail.counts.tool_instances + detail.counts.alert_sources + detail.counts.integrations + (detail.parts.mcp_server ? 1 : 0);
  const integration = detail.parts.integrations[0];

  return (
    <div className="space-y-5">
      <Link to="/connectors" className="text-sm text-fg-muted hover:text-fg inline-flex items-center gap-1">
        <ArrowLeft className="w-4 h-4" />Connectors
      </Link>

      <header className="flex flex-wrap items-start gap-4">
        <ConnectorTile name={detail.catalog_name || detail.name} kind={detail.kind} />
        <div className="min-w-0 flex-1">
          {renaming === null ? (
            <h1 className="text-xl font-semibold text-fg flex items-center gap-2">
              <span className="truncate">{detail.name}</span>
              <button className="btn btn-ghost p-1" title="Rename" onClick={() => setRenaming(detail.name)}>
                <Pencil className="w-3.5 h-3.5" />
              </button>
            </h1>
          ) : (
            <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); void saveName(); }}>
              <input className="input-field max-w-sm" value={renaming} onChange={(e) => setRenaming(e.target.value)} autoFocus aria-label="Connector name" />
              <button className="btn btn-primary" type="submit" disabled={!renaming.trim()}>Save</button>
              <button className="btn btn-secondary" type="button" onClick={() => setRenaming(null)}>Cancel</button>
            </form>
          )}
          <p className="text-sm text-fg-muted mt-1 flex flex-wrap items-center gap-2">
            <span>{detail.catalog_name}</span>
            <span className="text-fg-subtle">·</span>
            <span>{KIND_LABELS[detail.kind]}</span>
            <span className="text-fg-subtle">·</span>
            <span>{detail.capabilities.map((c) => CAPABILITY_LABELS[c]).join(', ')}</span>
            <span className={statusBadgeClass(st.tone)} title={detail.status.detail}>{st.label}</span>
            {detail.status.detail && detail.status.state !== 'ok' && (
              <span className="text-xs text-fg-subtle truncate max-w-xs" title={detail.status.detail}>{detail.status.detail}</span>
            )}
          </p>
        </div>
      </header>

      {(oauthOK || oauthErr) && (
        <div className={`flex items-start gap-2 p-3 rounded-md border text-sm ${oauthOK ? 'border-state-ok/40 bg-state-ok/5' : 'border-state-fail/40 bg-state-fail/5'}`}>
          {oauthOK ? <CheckCircle2 className="w-4 h-4 mt-0.5 text-state-ok" /> : <XCircle className="w-4 h-4 mt-0.5 text-state-fail" />}
          <p className="flex-1 text-fg">{oauthOK ? 'Signed in. The agent can now use this server.' : `Sign-in failed: ${oauthErr}`}</p>
          <button className="text-xs text-fg-subtle hover:text-fg" onClick={clearOAuthParams}>Dismiss</button>
        </div>
      )}
      {error && <ErrorMessage message={error} />}

      <nav aria-label="Connector sections" className="flex gap-1 border-b border-line overflow-x-auto">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            aria-current={active === t.id ? 'page' : undefined}
            className={`px-3 h-9 text-sm whitespace-nowrap border-b-2 transition-colors ${
              active === t.id ? '!border-primary-500 text-fg' : '!border-transparent text-fg-muted hover:text-fg'
            }`}
          >
            {t.label}
          </button>
        ))}
      </nav>

      <div>
        {active === 'tools' &&
          (detail.parts.mcp_server ? (
            <MCPServerPanel key={detail.parts.mcp_server.id} part={detail.parts.mcp_server} onChange={() => void load()} />
          ) : (
            <ToolsSection scope={scope} />
          ))}
        {active === 'alerts' && <AlertSourcesManager scope={scope} />}
        {active === 'messaging' &&
          (integration ? (
            <div className="space-y-6">
              <IntegrationPanel integrationUUID={integration.uuid} onChange={() => void load()} />
              <ChannelsManager
                integrationUUID={integration.uuid}
                listenSupported={detail.catalog?.listen_supported ?? false}
                onChange={() => void load()}
              />
            </div>
          ) : (
            <p className="text-sm text-fg-muted">This connector has no messaging integration.</p>
          ))}
        {active === 'formatting' && <FormattingPanel connector={detail} />}
        {active === 'settings' && (
          <div className="space-y-6">
            <ConnectorPartsPanel detail={detail} siblings={siblings} onChange={() => void load()} onMovedAway={(u) => navigate(`/connectors/${u}`)} />
            <section className="card space-y-3 border-state-fail/30">
              <h2 className="text-sm font-semibold text-fg">Delete connector</h2>
              <p className="text-sm text-fg-muted">
                Deletes everything in this connector ({partCount} {partCount === 1 ? 'item' : 'items'}): tools and their skill
                and cron assignments, alert webhooks (their URLs stop working), messaging channels, and the MCP server.
              </p>
              {confirmDelete ? (
                <div className="flex gap-2">
                  <button className="btn btn-danger" onClick={doDelete} disabled={deleting}>
                    <Trash2 className="w-4 h-4" />{deleting ? 'Deleting…' : 'Yes, delete'}
                  </button>
                  <button className="btn btn-secondary" onClick={() => setConfirmDelete(false)} disabled={deleting}>Cancel</button>
                </div>
              ) : (
                <button className="btn btn-secondary text-state-fail" onClick={() => setConfirmDelete(true)}>
                  <Trash2 className="w-4 h-4" />Delete connector
                </button>
              )}
            </section>
          </div>
        )}
      </div>
    </div>
  );
}
