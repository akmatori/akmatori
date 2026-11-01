import { useState } from 'react';
import { GitMerge, LogOut } from 'lucide-react';
import { connectorsApi } from '../../api/connectors';
import { getErrorMessage } from '../../utils/errors';
import type { ConnectorDetail, ConnectorPartKind, ConnectorSummary } from '../../types/connectors';
import ErrorMessage from '../ErrorMessage';

interface Props {
  detail: ConnectorDetail;
  // Other connectors of the same catalog entry: merge candidates.
  siblings: ConnectorSummary[];
  onChange: () => void;
  onMovedAway: (newConnectorUUID: string) => void;
}

interface PartRow {
  kind: ConnectorPartKind;
  id: number;
  label: string;
  what: string;
  movable: boolean;
}

function partRows(d: ConnectorDetail): PartRow[] {
  const rows: PartRow[] = [];
  if (d.parts.mcp_server) {
    rows.push({ kind: 'mcp_server', id: d.parts.mcp_server.id, label: d.parts.mcp_server.url, what: 'MCP server', movable: false });
  }
  for (const t of d.parts.tool_instances) {
    rows.push({ kind: 'tool_instance', id: t.id, label: t.name, what: t.managed ? 'Tool (from the MCP server)' : `Tool · ${t.tool_type}`, movable: !t.managed });
  }
  for (const a of d.parts.alert_sources) {
    rows.push({ kind: 'alert_source', id: a.id, label: a.name, what: `Alert webhook · ${a.source_type_label}`, movable: true });
  }
  for (const i of d.parts.integrations) {
    rows.push({ kind: 'integration', id: i.id, label: i.name, what: `Messaging · ${i.provider}`, movable: true });
  }
  return rows;
}

// ConnectorPartsPanel lists what a connector groups. Parts can move out into
// a connector of their own, and another connector of the same kind can be
// merged in (e.g. a Zabbix webhook into "Zabbix prod").
export default function ConnectorPartsPanel({ detail, siblings, onChange, onMovedAway }: Props) {
  const [mergeFrom, setMergeFrom] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const rows = partRows(detail);
  const mergeable = siblings.filter((s) => s.kind !== 'custom_mcp');

  const detach = async (row: PartRow) => {
    setBusy(true);
    setError('');
    try {
      const res = await connectorsApi.detach(detail.uuid, row.kind, row.id);
      if (rows.length === 1) onMovedAway(res.connector_uuid);
      else onChange();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to move'));
    } finally {
      setBusy(false);
    }
  };

  const merge = async () => {
    if (!mergeFrom) return;
    setBusy(true);
    setError('');
    try {
      const other = await connectorsApi.get(mergeFrom);
      for (const r of partRows(other)) {
        if (r.kind === 'mcp_server' || !r.movable) continue;
        await connectorsApi.attach(detail.uuid, r.kind, r.id);
      }
      setMergeFrom('');
      onChange();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to merge'));
      onChange();
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="card space-y-4">
      <div>
        <h2 className="text-sm font-semibold text-fg">What this connector contains</h2>
        <p className="text-xs text-fg-subtle mt-1">
          A connector groups the tools, alert webhooks and messaging of one system. Webhook URLs, tool names and skill
          assignments do not change when you move them.
        </p>
      </div>
      {error && <ErrorMessage message={error} />}
      {rows.length === 0 ? (
        <p className="text-sm text-fg-muted">Nothing yet.</p>
      ) : (
        <ul className="divide-y divide-line">
          {rows.map((r) => (
            <li key={`${r.kind}-${r.id}`} className="flex items-center justify-between gap-3 py-2.5">
              <div className="min-w-0">
                <p className="text-sm text-fg truncate">{r.label}</p>
                <p className="text-xs text-fg-subtle">{r.what}</p>
              </div>
              {r.movable && rows.length > 1 && (
                <button className="btn btn-ghost text-xs flex-shrink-0" onClick={() => detach(r)} disabled={busy} title="Move into a connector of its own">
                  <LogOut className="w-3.5 h-3.5" />Move out
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {mergeable.length > 0 && (
        <div className="pt-2 border-t border-line">
          <label htmlFor="merge-from" className="block text-sm font-medium text-fg mb-1">Merge another {detail.catalog_name} connector into this one</label>
          <div className="flex flex-wrap gap-2">
            <select id="merge-from" className="input-field max-w-sm" value={mergeFrom} onChange={(e) => setMergeFrom(e.target.value)}>
              <option value="">Choose a connector…</option>
              {mergeable.map((s) => (
                <option key={s.uuid} value={s.uuid}>{s.name}</option>
              ))}
            </select>
            <button className="btn btn-secondary" onClick={merge} disabled={!mergeFrom || busy}>
              <GitMerge className="w-4 h-4" />Merge
            </button>
          </div>
        </div>
      )}
    </section>
  );
}
