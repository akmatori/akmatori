import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { Plus, RefreshCw, Search, Activity } from 'lucide-react';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import TimeRangePicker from '../components/TimeRangePicker';
import EmptyState from '../components/shared/EmptyState';
import Pagination from '../components/shared/Pagination';
import SegmentedControl from '../components/shared/SegmentedControl';
import IncidentList from '../components/incidents/IncidentList';
import IncidentPanel from '../components/incidents/IncidentPanel';
import CreateIncidentModal from '../components/incidents/CreateIncidentModal';
import { incidentsApi } from '../api/client';
import type { Incident } from '../types';
import { useInterval } from '../hooks/useInterval';
import { getErrorMessage } from '../utils/errors';
import { taskResultPath } from '../components/runs/runHelpers';

// History defaults to the last 24 hours; the list refreshes every minute.
const DEFAULT_TIME_RANGE = 24 * 60 * 60;
const DEFAULT_REFRESH_INTERVAL = 60000;

type IncidentView = 'open' | 'history';

const VIEW_OPTIONS: { value: IncidentView; label: string }[] = [
  { value: 'open', label: 'Open' },
  { value: 'history', label: 'History' },
];

// alert_active = "completed" alert-sourced incidents whose linked alert is
// still firing — still-open work, not history.
const OPEN_STATUS_FILTER = 'pending,running,diagnosed,monitor,alert_active';
const HISTORY_STATUS_FILTER = 'completed,failed,closed';

interface ListQuery {
  view: IncidentView;
  page: number;
  perPage: number;
  from: number;
  to: number;
  // Seconds for a relative range ("last 30 min"); null for an absolute one.
  relative: number | null;
}

interface ListResult {
  key: string;
  incidents: Incident[];
  total: number;
  totalPages: number;
}

async function fetchList(q: ListQuery) {
  let from: number | undefined;
  let to: number | undefined;
  if (q.view === 'history') {
    const now = Math.floor(Date.now() / 1000);
    from = q.relative !== null ? now - q.relative : q.from;
    to = q.relative !== null ? now : q.to;
  }
  const status = q.view === 'open' ? OPEN_STATUS_FILTER : HISTORY_STATUS_FILTER;
  const res = await incidentsApi.list(from, to, q.page, q.perPage, '1h', status);
  return { incidents: res.data, total: res.pagination.total, totalPages: res.pagination.total_pages };
}

// Incidents is a split view: the list on the left, the selected incident
// (/incidents/:uuid) on the right. Below lg only one side shows at a time.
export default function Incidents() {
  const { uuid } = useParams<{ uuid?: string }>();
  const navigate = useNavigate();
  const [query, setQuery] = useState<ListQuery>(() => {
    const now = Math.floor(Date.now() / 1000);
    return { view: 'open', page: 1, perPage: 50, from: now - DEFAULT_TIME_RANGE, to: now, relative: DEFAULT_TIME_RANGE };
  });
  const [result, setResult] = useState<ListResult | null>(null);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState('');
  const [refreshInterval, setRefreshInterval] = useState(DEFAULT_REFRESH_INTERVAL);
  const [refreshing, setRefreshing] = useState(false);
  const [showCreate, setShowCreate] = useState(false);

  const queryKey = JSON.stringify(query);
  const loading = result?.key !== queryKey;

  useEffect(() => {
    let cancelled = false;
    fetchList(query)
      .then((r) => {
        if (cancelled) return;
        setResult({ key: queryKey, ...r });
        setError('');
      })
      .catch((err: unknown) => { if (!cancelled) setError(getErrorMessage(err, 'Failed to load incidents')); });
    return () => { cancelled = true; };
  }, [query, queryKey]);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      setResult({ key: queryKey, ...(await fetchList(query)) });
      setError('');
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load incidents'));
    } finally {
      setRefreshing(false);
    }
  }, [query, queryKey]);
  useInterval(() => { void refresh(); }, refreshInterval);

  // Keep the list row in step with the panel's polling. The list endpoint
  // carries aggregate fields the single-incident endpoint lacks; keep them.
  const handlePanelUpdate = useCallback((updated: Incident) => {
    setResult((prev) => prev && {
      ...prev,
      incidents: prev.incidents.map((i) => (i.uuid !== updated.uuid ? i : {
        ...updated,
        first_seen: updated.first_seen ?? i.first_seen,
        last_seen: updated.last_seen ?? i.last_seen,
        trend: updated.trend ?? i.trend,
        alert_count: updated.alert_count ?? i.alert_count,
      })),
    });
  }, []);

  const visible = useMemo(() => {
    const incidents = result?.incidents ?? [];
    const needle = filter.trim().toLowerCase();
    if (!needle) return incidents;
    return incidents.filter((i) =>
      `${i.title} ${i.uuid} ${i.source}`.toLowerCase().includes(needle),
    );
  }, [result, filter]);

  const listPane = (
    <div className={`${uuid ? 'hidden lg:flex' : 'flex'} flex-col min-h-0 lg:border-r border-line`}>
      <div className="flex-1 min-h-0 overflow-y-auto">
        {loading && !error ? (
          <LoadingSpinner />
        ) : visible.length === 0 ? (
          <div className="p-4">
            <EmptyState
              icon={Activity}
              padding="py-12"
              title={filter ? 'No incident on this page matches the filter' : query.view === 'open' ? 'No open incidents' : 'No incidents in this time range'}
              description={query.view === 'open' && !filter ? 'Nothing is running, firing or in a monitor window.' : undefined}
            />
          </div>
        ) : (
          <IncidentList incidents={visible} selectedUUID={uuid} />
        )}
      </div>
      {result && (
        <Pagination
          page={query.page}
          totalPages={result.totalPages}
          totalItems={result.total}
          onPageChange={(page) => setQuery((q) => ({ ...q, page }))}
          perPage={query.perPage}
          onPerPageChange={(perPage) => setQuery((q) => ({ ...q, perPage, page: 1 }))}
        />
      )}
    </div>
  );

  return (
    <div className="flex flex-col gap-4 lg:h-[calc(100dvh-7rem)]">
      <div className="flex flex-wrap items-center gap-2.5">
        <SegmentedControl
          value={query.view}
          options={VIEW_OPTIONS.map((o) =>
            o.value === query.view && result && !loading ? { ...o, label: `${o.label} ${result.total}` } : o,
          )}
          onChange={(view) => setQuery((q) => ({ ...q, view, page: 1 }))}
        />
        {query.view === 'history' && (
          <TimeRangePicker
            from={query.from}
            to={query.to}
            refreshInterval={refreshInterval}
            onChange={(from, to, relative) => setQuery((q) => ({ ...q, from, to, relative: relative ?? null, page: 1 }))}
            onRefreshIntervalChange={setRefreshInterval}
          />
        )}
        <label className="relative flex-[1_1_220px] max-w-sm">
          <span className="sr-only">Filter incidents on this page</span>
          <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-fg-subtle" aria-hidden="true" />
          <input
            type="search"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter by title, source or ID"
            className="input-field h-9 !pl-8"
          />
        </label>
        <span className="flex-1" />
        <button
          type="button"
          onClick={() => void refresh()}
          disabled={refreshing}
          className="btn btn-ghost h-9 w-9 p-0 inline-flex items-center justify-center"
          title="Refresh"
          aria-label="Refresh"
        >
          <RefreshCw size={15} className={refreshing ? 'animate-spin' : ''} />
        </button>
        <button type="button" onClick={() => setShowCreate(true)} className="btn btn-primary h-9 inline-flex items-center gap-1.5">
          <Plus size={15} aria-hidden="true" />
          New incident
        </button>
      </div>

      {error && <ErrorMessage message={error} />}

      <div className="flex-1 min-h-0 grid lg:grid-cols-[minmax(320px,420px)_1fr] rounded-xl border border-line bg-surface overflow-hidden">
        {listPane}
        <div className={`${uuid ? 'flex' : 'hidden lg:flex'} flex-col min-h-0`}>
          {uuid ? (
            <IncidentPanel key={uuid} uuid={uuid} onUpdate={handlePanelUpdate} />
          ) : (
            <p className="m-auto px-6 text-center text-fg-subtle">Select an incident to see the summary, the agent timeline and the alerts.</p>
          )}
        </div>
      </div>

      {showCreate && (
        <CreateIncidentModal
          onClose={() => setShowCreate(false)}
          onCreated={(res) => {
            setShowCreate(false);
            const path = taskResultPath(res);
            if (path) navigate(path);
            else setError(res.message);
            void refresh();
          }}
          onError={setError}
        />
      )}
    </div>
  );
}
