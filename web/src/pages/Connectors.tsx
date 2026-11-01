import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { ChevronDown, Plug, Plus, Search, Server } from 'lucide-react';
import { connectorsApi } from '../api/connectors';
import { getErrorMessage } from '../utils/errors';
import { useLatestRef } from '../hooks/useLatestRef';
import { formatRelativeAgo } from '../utils/format';
import type { ConnectorCatalogEntry, ConnectorSummary } from '../types/connectors';
import SegmentedControl from '../components/shared/SegmentedControl';
import EmptyState from '../components/shared/EmptyState';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import ConnectorTile from '../components/connectors/ConnectorTile';
import AddMCPConnectorModal from '../components/connectors/AddMCPConnectorModal';
import {
  CAPABILITY_LABELS,
  KIND_LABELS,
  addPathFor,
  filterConnectors,
  groupCatalog,
  statusBadgeClass,
  statusView,
} from '../components/connectors/connectorHelpers';

type Tab = 'yours' | 'discover';

function ConnectorRow({ c }: { c: ConnectorSummary }) {
  const st = statusView(c.status.state);
  const subtitle = c.name !== c.catalog_name ? c.catalog_name : '';
  return (
    <Link
      to={`/connectors/${c.uuid}`}
      className="grid grid-cols-[1fr_auto] sm:grid-cols-[minmax(0,1fr)_9rem_10rem_9rem] items-center gap-x-4 gap-y-1 px-4 py-3 hover:bg-surface-2 transition-colors"
    >
      <div className="flex items-center gap-3 min-w-0">
        <ConnectorTile name={c.catalog_name || c.name} kind={c.kind} />
        <div className="min-w-0">
          <p className="text-sm text-fg truncate">{c.name}</p>
          {subtitle && <p className="text-xs text-fg-subtle truncate">{subtitle}</p>}
        </div>
      </div>
      <span className="hidden sm:block text-xs text-fg-muted">{KIND_LABELS[c.kind] ?? c.kind}</span>
      <div className="hidden sm:flex flex-wrap gap-1">
        {c.capabilities.map((cap) => (
          <span key={cap} className="badge badge-default">{CAPABILITY_LABELS[cap]}</span>
        ))}
      </div>
      <div className="justify-self-end sm:justify-self-start text-right sm:text-left">
        <span className={statusBadgeClass(st.tone)} title={c.status.detail}>{st.label}</span>
        {c.last_alert_at && (
          <p className="text-[11px] text-fg-subtle mt-0.5">alert {formatRelativeAgo(c.last_alert_at)}</p>
        )}
      </div>
    </Link>
  );
}

function CatalogCard({ e, count }: { e: ConnectorCatalogEntry; count: number }) {
  return (
    <Link
      to={addPathFor(e)}
      className="card p-4 flex gap-3 hover:border-primary-500/50 transition-colors"
    >
      <ConnectorTile name={e.name} kind={e.kind} />
      <div className="min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          <p className="text-sm font-medium text-fg">{e.name}</p>
          {count > 0 && <span className="badge badge-success">{count} added</span>}
          {e.note && <span className="badge badge-default">{e.note}</span>}
        </div>
        <p className="text-xs text-fg-muted mt-1 line-clamp-2">{e.description}</p>
        <div className="flex flex-wrap gap-1 mt-2">
          {e.capabilities.map((cap) => (
            <span key={cap} className="text-[11px] text-fg-subtle">{CAPABILITY_LABELS[cap]}</span>
          ))}
        </div>
      </div>
    </Link>
  );
}

export default function Connectors() {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const tab: Tab = params.get('tab') === 'discover' ? 'discover' : 'yours';
  const showMCP = params.get('add') === 'mcp';
  const [query, setQuery] = useState('');
  const [list, setList] = useState<ConnectorSummary[] | null>(null);
  const [catalog, setCatalog] = useState<ConnectorCatalogEntry[]>([]);
  const [error, setError] = useState('');
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  const load = useCallback(async () => {
    try {
      const [l, c] = await Promise.all([connectorsApi.list(), connectorsApi.catalog()]);
      setList(l);
      setCatalog(c);
      setError('');
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load connectors'));
    }
  }, []);

  // Initial load (through a ref so the effect runs once).
  const loadRef = useLatestRef(load);
  useEffect(() => {
    void loadRef.current();
  }, [loadRef]);

  useEffect(() => {
    if (!menuOpen) return;
    const close = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false);
    };
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, [menuOpen]);

  const setTab = (t: Tab) => {
    const next = new URLSearchParams(params);
    if (t === 'discover') next.set('tab', 'discover');
    else next.delete('tab');
    setParams(next, { replace: true });
  };
  const openMCP = () => {
    setMenuOpen(false);
    const next = new URLSearchParams(params);
    next.set('add', 'mcp');
    setParams(next);
  };
  const closeMCP = () => {
    const next = new URLSearchParams(params);
    next.delete('add');
    setParams(next, { replace: true });
  };

  const countByCatalog = new Map<string, number>();
  for (const c of list ?? []) countByCatalog.set(c.catalog_id, (countByCatalog.get(c.catalog_id) ?? 0) + 1);
  const visible = filterConnectors(list ?? [], query);
  const groups = groupCatalog(catalog, query);

  return (
    <div>
      <h1 className="sr-only">Connectors</h1>
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <SegmentedControl<Tab>
          value={tab}
          onChange={setTab}
          options={[
            { value: 'yours', label: 'Yours' },
            { value: 'discover', label: 'Discover' },
          ]}
        />
        <div className="relative flex-1 min-w-[12rem] max-w-md">
          <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-fg-subtle" aria-hidden="true" />
          <input
            className="input-field !pl-9"
            placeholder="Search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Search connectors"
          />
        </div>
        <div className="relative ml-auto" ref={menuRef}>
          <button className="btn btn-primary" onClick={() => setMenuOpen((v) => !v)} aria-haspopup="menu" aria-expanded={menuOpen}>
            <Plus className="w-4 h-4" /> Add <ChevronDown className="w-4 h-4" />
          </button>
          {menuOpen && (
            <div role="menu" className="absolute right-0 mt-1 w-60 rounded-md border border-line bg-surface shadow-lg z-20 py-1">
              <button role="menuitem" className="w-full text-left px-3 py-2 text-sm hover:bg-surface-2 flex items-center gap-2"
                onClick={() => { setMenuOpen(false); setTab('discover'); }}>
                <Plug className="w-4 h-4 text-fg-subtle" /> Browse connectors
              </button>
              <button role="menuitem" className="w-full text-left px-3 py-2 text-sm hover:bg-surface-2 flex items-center gap-2" onClick={openMCP}>
                <Server className="w-4 h-4 text-fg-subtle" /> Add custom connector (MCP)
              </button>
            </div>
          )}
        </div>
      </div>

      {error && <ErrorMessage message={error} />}

      {list === null && !error ? (
        <LoadingSpinner />
      ) : tab === 'yours' ? (
        visible.length === 0 ? (
          query ? (
            <EmptyState icon={Search} title="No connectors match your search" />
          ) : (
            <div className="space-y-3">
              <EmptyState icon={Plug} title="No connectors yet" description="Connect your monitoring, messaging and infrastructure so the agent can investigate." />
              <div className="text-center">
                <button className="btn btn-primary" onClick={() => setTab('discover')}>Browse connectors</button>
              </div>
            </div>
          )
        ) : (
          <div className="card p-0 overflow-hidden">
            <div className="hidden sm:grid grid-cols-[minmax(0,1fr)_9rem_10rem_9rem] gap-x-4 px-4 py-2 border-b border-line text-xs text-fg-subtle">
              <span>Connector</span>
              <span>Type</span>
              <span>Capabilities</span>
              <span>Status</span>
            </div>
            <div className="divide-y divide-line">
              {visible.map((c) => (
                <ConnectorRow key={c.uuid} c={c} />
              ))}
            </div>
          </div>
        )
      ) : groups.length === 0 ? (
        <EmptyState icon={Search} title="Nothing in the catalog matches your search" />
      ) : (
        <div className="space-y-8">
          {groups.map((g) => (
            <section key={g.category}>
              <h2 className="text-xs font-semibold uppercase tracking-wide text-fg-subtle mb-3">{g.label}</h2>
              <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
                {g.entries.map((e) => (
                  <CatalogCard key={e.id} e={e} count={countByCatalog.get(e.id) ?? 0} />
                ))}
              </div>
            </section>
          ))}
        </div>
      )}

      {showMCP && (
        <AddMCPConnectorModal
          onClose={() => {
            closeMCP();
            void load();
          }}
          onCreated={(connectorUUID, oauthError) => {
            if (!connectorUUID) {
              closeMCP();
              void load();
              return;
            }
            const q = oauthError ? `?${new URLSearchParams({ oauth_error: oauthError })}` : '';
            navigate(`/connectors/${connectorUUID}${q}`, { replace: true });
          }}
        />
      )}
    </div>
  );
}
