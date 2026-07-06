import { useEffect, useState, useRef, useCallback, Fragment } from 'react';
import { RefreshCw, Activity, Rss, Link as LinkIcon, Shuffle, FileCode, Loader2 } from 'lucide-react';
import { Link } from 'react-router-dom';
import { JsonView, darkStyles } from 'react-json-view-lite';
import 'react-json-view-lite/dist/index.css';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage } from '../components/ErrorMessage';
import TimeRangePicker from '../components/TimeRangePicker';
import MoveIncidentModal from '../components/MoveIncidentModal';
import { eventsApi } from '../api/client';
import type { EventFeedItem } from '../types';
import { ChevronLeft, ChevronRight } from 'lucide-react';

const jsonViewerStyles = {
  ...darkStyles,
  container: 'bg-transparent font-mono text-xs',
  basicChildStyle: 'pl-4',
  label: 'text-purple-400 mr-1',
  nullValue: 'text-gray-500',
  undefinedValue: 'text-gray-500',
  stringValue: 'text-green-400',
  booleanValue: 'text-red-400',
  numberValue: 'text-orange-400',
  otherValue: 'text-gray-300',
  punctuation: 'text-gray-500',
  expandIcon: 'text-gray-400 cursor-pointer select-none',
  collapseIcon: 'text-gray-400 cursor-pointer select-none',
};

// RawEventContent renders an event's raw payload / original message on demand.
function RawEventContent({ state }: { state?: RawState }) {
  if (!state || state.loading) {
    return (
      <div className="flex items-center gap-2 text-gray-400 text-xs">
        <Loader2 className="w-3.5 h-3.5 animate-spin" /> Loading raw message…
      </div>
    );
  }
  if (state.error) {
    return <p className="text-red-400 text-xs">{state.error}</p>;
  }
  if (state.original_message) {
    return (
      <pre className="whitespace-pre-wrap text-gray-300 font-mono text-xs max-h-80 overflow-y-auto">
        {state.original_message}
      </pre>
    );
  }
  if (state.raw !== undefined && state.raw !== null) {
    if (typeof state.raw === 'object') {
      return (
        <div className="max-h-80 overflow-y-auto">
          <JsonView data={state.raw as object} style={jsonViewerStyles} shouldExpandNode={() => true} />
        </div>
      );
    }
    return <pre className="whitespace-pre-wrap text-gray-300 font-mono text-xs">{String(state.raw)}</pre>;
  }
  return <p className="text-gray-500 text-xs">No raw message available for this event</p>;
}

interface RawState {
  loading: boolean;
  raw?: unknown;
  original_message?: string;
  error?: string;
}

const DEFAULT_TIME_RANGE = 3 * 60 * 60; // 3 hours
const DEFAULT_REFRESH_INTERVAL = 60000;

type TypeFilter = 'all' | 'alert' | 'cron' | 'slack_mention' | 'manual';

const formatRelative = (iso: string): string => {
  const diffMs = Date.now() - new Date(iso).getTime();
  const diffSec = Math.max(0, Math.floor(diffMs / 1000));
  if (diffSec < 60) return `${diffSec}s ago`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 7) return `${diffDay}d ago`;
  const diffWeek = Math.floor(diffDay / 7);
  return `${diffWeek}w ago`;
};

const formatAbsolute = (iso: string): string => {
  const d = new Date(iso);
  return d.toLocaleString();
};

function TypeChip({ type }: { type: string }) {
  switch (type) {
    case 'alert':
      return (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
          Alert
        </span>
      );
    case 'cron':
      return (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400">
          Cron
        </span>
      );
    case 'slack_mention':
      return (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400">
          Slack
        </span>
      );
    default:
      return (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400">
          Manual
        </span>
      );
  }
}

function StatusBadge({ status }: { status: string }) {
  switch (status) {
    case 'completed':
      return <span className="badge badge-success">Resolved</span>;
    case 'monitor':
      return <span className="badge badge-success">Monitoring</span>;
    case 'closed':
      return <span className="badge badge-default">Closed</span>;
    case 'pending':
    case 'running':
      return <span className="badge badge-primary">Ongoing</span>;
    case 'diagnosed':
      return <span className="badge badge-purple">Ongoing</span>;
    case 'failed':
      return <span className="badge badge-error">Failed</span>;
    case 'firing':
      return <span className="badge badge-primary">Firing</span>;
    case 'resolved':
      return <span className="badge badge-success">Resolved</span>;
    default:
      return <span className="badge badge-default">{status}</span>;
  }
}

function CorrelationChip({
  item,
  onToggle,
  expanded,
}: {
  item: EventFeedItem;
  onToggle: () => void;
  expanded: boolean;
}) {
  const { correlation_confidence } = item;
  // Historical/seeded alerts can carry correlated=true (with confidence and
  // reasoning) but a NULL correlation_decision. Derive the display state from
  // `correlated` in that case so the reasoning is not silently hidden.
  const correlation_decision = item.correlation_decision || (item.correlated ? 'linked' : '');
  if (!correlation_decision) return null;

  if (correlation_decision === 'linked') {
    const pct = correlation_confidence !== undefined ? Math.round(correlation_confidence * 100) : null;
    const label = `Correlated${pct !== null ? ` ${pct}%` : ''}`;
    if (!item.correlation_reasoning) {
      return (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400">
          {label}
        </span>
      );
    }
    return (
      <button
        onClick={onToggle}
        className={`inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium cursor-pointer transition-colors ${
          expanded
            ? 'bg-green-200 text-green-800 dark:bg-green-800/40 dark:text-green-300'
            : 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400 hover:bg-green-200 dark:hover:bg-green-800/40'
        }`}
        title="Click to see correlation reasoning"
      >
        {label}
      </button>
    );
  }

  if (correlation_decision === 'new_incident') {
    if (!item.correlation_reasoning) {
      return (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400">
          New incident
        </span>
      );
    }
    return (
      <button
        onClick={onToggle}
        className={`inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium cursor-pointer transition-colors ${
          expanded
            ? 'bg-orange-200 text-orange-800 dark:bg-orange-800/40 dark:text-orange-300'
            : 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400 hover:bg-orange-200 dark:hover:bg-orange-800/40'
        }`}
        title="Click to see reasoning"
      >
        New incident
      </button>
    );
  }

  // not_evaluated
  return (
    <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-500">
      Not evaluated
    </span>
  );
}

export default function Feed() {
  const [items, setItems] = useState<EventFeedItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [successMsg, setSuccessMsg] = useState<string | null>(null);
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all');
  const [expandedUUIDs, setExpandedUUIDs] = useState<Set<string>>(new Set());
  // Raw-message expansion + per-event fetch cache.
  const [rawExpanded, setRawExpanded] = useState<Set<string>>(new Set());
  const [rawData, setRawData] = useState<Record<string, RawState>>({});
  // The alert currently being reassigned via the Move modal.
  const [moveTarget, setMoveTarget] = useState<EventFeedItem | null>(null);

  // Pagination
  const [page, setPage] = useState(1);
  const [perPage] = useState(50);
  const [totalPages, setTotalPages] = useState(0);
  const [totalItems, setTotalItems] = useState(0);

  // Time range
  const now = Math.floor(Date.now() / 1000);
  const [timeFrom, setTimeFrom] = useState(now - DEFAULT_TIME_RANGE);
  const [timeTo, setTimeTo] = useState(now);
  const [relativeRange, setRelativeRange] = useState<number | null>(DEFAULT_TIME_RANGE);
  const [refreshInterval, setRefreshInterval] = useState(DEFAULT_REFRESH_INTERVAL);
  const refreshRef = useRef<number | null>(null);

  const loadFeed = useCallback(async (
    from?: number,
    to?: number,
    isRefresh?: boolean,
    pageOverride?: number,
    typeOverride?: TypeFilter,
  ) => {
    try {
      setLoading(true);
      setError('');
      const currentNow = Math.floor(Date.now() / 1000);

      let effectiveFrom: number | undefined;
      let effectiveTo: number | undefined;
      if (isRefresh && relativeRange !== null) {
        effectiveFrom = currentNow - relativeRange;
        effectiveTo = currentNow;
      } else {
        effectiveFrom = from ?? timeFrom;
        effectiveTo = to ?? timeTo;
      }

      const effectiveType = typeOverride ?? typeFilter;
      const currentPage = pageOverride ?? page;
      const result = await eventsApi.list({
        from: effectiveFrom,
        to: effectiveTo,
        page: currentPage,
        perPage,
        type: effectiveType === 'all' ? undefined : effectiveType,
      });
      setItems(result.data);
      setTotalPages(result.pagination.total_pages);
      setTotalItems(result.pagination.total);

      if (isRefresh && relativeRange !== null) {
        setTimeFrom(effectiveFrom!);
        setTimeTo(effectiveTo!);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load feed');
    } finally {
      setLoading(false);
    }
  }, [timeFrom, timeTo, relativeRange, page, perPage, typeFilter]);

  const loadFeedRef = useRef(loadFeed);

  useEffect(() => {
    loadFeed();
  }, []);

  useEffect(() => {
    loadFeedRef.current = loadFeed;
  }, [loadFeed]);

  useEffect(() => {
    if (refreshInterval > 0) {
      refreshRef.current = window.setInterval(() => {
        loadFeedRef.current(undefined, undefined, true);
      }, refreshInterval);
    }
    return () => {
      if (refreshRef.current) {
        clearInterval(refreshRef.current);
        refreshRef.current = null;
      }
    };
  }, [refreshInterval]);

  const handleTimeRangeChange = useCallback((from: number, to: number, relativeDuration?: number | null) => {
    setTimeFrom(from);
    setTimeTo(to);
    setPage(1);
    setRelativeRange(relativeDuration ?? null);
    loadFeed(from, to, false, 1);
  }, [loadFeed]);

  const handlePageChange = useCallback((newPage: number) => {
    setPage(newPage);
    loadFeed(undefined, undefined, false, newPage);
  }, [loadFeed]);

  const handleTypeFilter = useCallback((t: TypeFilter) => {
    setTypeFilter(t);
    setPage(1);
    loadFeed(undefined, undefined, false, 1, t);
  }, [loadFeed]);

  const toggleExpand = useCallback((uuid: string) => {
    setExpandedUUIDs(prev => {
      const next = new Set(prev);
      if (next.has(uuid)) {
        next.delete(uuid);
      } else {
        next.add(uuid);
      }
      return next;
    });
  }, []);

  const toggleRaw = useCallback((item: EventFeedItem) => {
    const uuid = item.event_uuid;
    setRawExpanded(prev => {
      const next = new Set(prev);
      if (next.has(uuid)) {
        next.delete(uuid);
        return next;
      }
      next.add(uuid);
      // Fetch the raw payload the first time this row is expanded.
      setRawData(prevData => {
        if (prevData[uuid] && !prevData[uuid].error) return prevData;
        eventsApi.raw(item.event_type, uuid)
          .then(res => setRawData(d => ({ ...d, [uuid]: { loading: false, raw: res.raw, original_message: res.original_message } })))
          .catch(err => setRawData(d => ({ ...d, [uuid]: { loading: false, error: err instanceof Error ? err.message : 'Failed to load raw message' } })));
        return { ...prevData, [uuid]: { loading: true } };
      });
      return next;
    });
  }, []);

  const handleMoved = useCallback((result: { incidentUUID: string; isNew: boolean }) => {
    setMoveTarget(null);
    setSuccessMsg(result.isNew
      ? `Alert unlinked. New incident: ${result.incidentUUID.slice(0, 8)}…`
      : `Alert linked to incident ${result.incidentUUID.slice(0, 8)}…`);
    setTimeout(() => setSuccessMsg(null), 6000);
    setPage(1);
    loadFeed(undefined, undefined, false, 1);
  }, [loadFeed]);

  const typeFilterOptions: { label: string; value: TypeFilter }[] = [
    { label: 'All', value: 'all' },
    { label: 'Alert', value: 'alert' },
    { label: 'Cron', value: 'cron' },
    { label: 'Slack', value: 'slack_mention' },
    { label: 'Manual', value: 'manual' },
  ];

  return (
    <div>
      <PageHeader
        title="Feed"
        description="All incoming events — alerts, cron runs, and manual triggers"
        action={
          <div className="flex items-center gap-3">
            <TimeRangePicker
              from={timeFrom}
              to={timeTo}
              refreshInterval={refreshInterval}
              onChange={handleTimeRangeChange}
              onRefreshIntervalChange={setRefreshInterval}
            />
            <button onClick={() => loadFeed()} className="btn btn-secondary" disabled={loading}>
              <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
              Refresh
            </button>
          </div>
        }
      />

      {error && <ErrorMessage message={error} />}
      {successMsg && <SuccessMessage message={successMsg} />}

      {/* Type filter chips */}
      <div className="flex items-center gap-2 mb-4">
        {typeFilterOptions.map(opt => (
          <button
            key={opt.value}
            onClick={() => handleTypeFilter(opt.value)}
            className={`px-3 py-1 rounded-full text-xs font-medium transition-colors border ${
              typeFilter === opt.value
                ? 'bg-primary-500 text-white border-primary-500'
                : 'bg-white dark:bg-gray-800 text-gray-600 dark:text-gray-400 border-gray-300 dark:border-gray-600 hover:bg-gray-50 dark:hover:bg-gray-700'
            }`}
          >
            {opt.label}
          </button>
        ))}
      </div>

      {loading ? (
        <LoadingSpinner />
      ) : (
        <div className="card">
          {items.length === 0 ? (
            <div className="py-16 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
              <Rss className="w-12 h-12 mx-auto text-gray-400 mb-3" />
              <p className="text-gray-500 dark:text-gray-400">No events in the selected time range</p>
              <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">
                Adjust the time range or type filter to see events
              </p>
            </div>
          ) : (
            <>
              <div className="overflow-x-auto">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Time</th>
                      <th>Type</th>
                      <th>Title</th>
                      <th>Status</th>
                      <th>Incident</th>
                      <th>Correlation</th>
                      <th></th>
                    </tr>
                  </thead>
                  <tbody>
                    {items.map((item) => {
                      const isExpanded = expandedUUIDs.has(item.event_uuid);
                      const isRawExpanded = rawExpanded.has(item.event_uuid);
                      // Any alert can be reassigned (origin or correlated).
                      const showMove = item.event_type === 'alert';

                      return (
                        <Fragment key={item.event_uuid}>
                          <tr>
                            <td className="text-gray-500 dark:text-gray-400 text-sm whitespace-nowrap" title={formatAbsolute(item.occurred_at)}>
                              {formatRelative(item.occurred_at)}
                            </td>
                            <td>
                              <TypeChip type={item.event_type} />
                            </td>
                            <td className="max-w-xs">
                              <div className="flex flex-col gap-0.5">
                                <span className="text-gray-800 dark:text-gray-100 text-sm font-medium truncate" title={item.title || undefined}>
                                  {item.title || <span className="text-gray-400 italic font-normal">Untitled</span>}
                                </span>
                                {item.target_host && (
                                  <span className="text-xs text-gray-400 dark:text-gray-500 truncate" title={item.target_host}>
                                    {item.target_host}
                                  </span>
                                )}
                              </div>
                            </td>
                            <td>
                              <StatusBadge status={item.status} />
                            </td>
                            <td>
                              {item.incident_uuid ? (
                                <div className="flex flex-col gap-0.5">
                                  <Link
                                    to={`/incidents/${item.incident_uuid}`}
                                    className="inline-flex items-center gap-1 text-xs text-primary-600 dark:text-primary-400 hover:underline"
                                  >
                                    <LinkIcon className="w-3 h-3" />
                                    <code>{item.incident_uuid.slice(0, 8)}</code>
                                  </Link>
                                  {item.incident_title && (
                                    <span className="text-xs text-gray-400 dark:text-gray-500 truncate max-w-[140px]" title={item.incident_title}>
                                      {item.incident_title}
                                    </span>
                                  )}
                                </div>
                              ) : (
                                <span className="text-xs text-gray-400">—</span>
                              )}
                            </td>
                            <td>
                              {item.event_type === 'alert' && (
                                <CorrelationChip
                                  item={item}
                                  expanded={isExpanded}
                                  onToggle={() => item.correlation_reasoning ? toggleExpand(item.event_uuid) : undefined}
                                />
                              )}
                            </td>
                            <td>
                              <div className="flex items-center gap-1 justify-end">
                                <button
                                  onClick={() => toggleRaw(item)}
                                  className={`btn btn-ghost p-1.5 ${
                                    isRawExpanded
                                      ? 'text-primary-600 dark:text-primary-400'
                                      : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-300'
                                  }`}
                                  title="View raw alert/event message"
                                >
                                  <FileCode className="w-4 h-4" />
                                </button>
                                {showMove && (
                                  <button
                                    onClick={() => setMoveTarget(item)}
                                    className="btn btn-ghost p-1.5 text-orange-500 hover:text-orange-700 dark:text-orange-400 dark:hover:text-orange-300"
                                    title="Move alert to another incident (or unlink into a new one)"
                                  >
                                    <Shuffle className="w-4 h-4" />
                                  </button>
                                )}
                              </div>
                            </td>
                          </tr>
                          {isExpanded && item.correlation_reasoning && (
                            <tr key={`${item.event_uuid}-reasoning`} className="bg-gray-50 dark:bg-gray-900/30">
                              <td colSpan={7} className="px-4 py-3">
                                <div className="flex items-start gap-2">
                                  <Activity className="w-4 h-4 text-gray-400 mt-0.5 flex-shrink-0" />
                                  <p className="text-xs text-gray-600 dark:text-gray-300 whitespace-pre-wrap">
                                    {item.correlation_reasoning}
                                  </p>
                                </div>
                              </td>
                            </tr>
                          )}
                          {isRawExpanded && (
                            <tr key={`${item.event_uuid}-raw`} className="bg-gray-900">
                              <td colSpan={7} className="px-4 py-3">
                                <div className="flex items-center gap-2 text-gray-500 mb-2">
                                  <FileCode className="w-3.5 h-3.5" />
                                  <span className="text-[10px] font-medium uppercase tracking-wide">Raw message</span>
                                </div>
                                <RawEventContent state={rawData[item.event_uuid]} />
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              {/* Pagination */}
              {totalPages > 1 && (
                <div className="flex items-center justify-between px-4 py-3 border-t border-gray-200 dark:border-gray-700">
                  <span className="text-sm text-gray-500 dark:text-gray-400">
                    Page {page} of {totalPages} ({totalItems.toLocaleString()} total)
                  </span>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => handlePageChange(page - 1)}
                      disabled={page <= 1}
                      className="btn btn-secondary p-1.5 disabled:opacity-50"
                    >
                      <ChevronLeft className="w-4 h-4" />
                    </button>
                    <button
                      onClick={() => handlePageChange(page + 1)}
                      disabled={page >= totalPages}
                      className="btn btn-secondary p-1.5 disabled:opacity-50"
                    >
                      <ChevronRight className="w-4 h-4" />
                    </button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      )}

      {moveTarget && (
        <MoveIncidentModal
          alertUUID={moveTarget.event_uuid}
          alertTitle={moveTarget.title}
          currentIncidentUUID={moveTarget.incident_uuid}
          onClose={() => setMoveTarget(null)}
          onMoved={handleMoved}
        />
      )}
    </div>
  );
}
