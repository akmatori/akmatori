import { useEffect, useMemo, useState, useRef, useCallback } from 'react';
import { RefreshCw, Rss, Search, X } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage, { SuccessMessage } from '../components/ErrorMessage';
import TimeRangePicker from '../components/TimeRangePicker';
import MoveIncidentModal from '../components/MoveIncidentModal';
import EmptyState from '../components/shared/EmptyState';
import Pagination from '../components/shared/Pagination';
import SegmentedControl from '../components/shared/SegmentedControl';
import { bucketSize, groupEvents } from '../components/events/eventHelpers';
import EventRow from '../components/events/EventRow';
import type { RawState } from '../components/events/RawEventContent';
import type { ResponseState } from '../components/events/IncidentResponseContent';
import { eventsApi } from '../api/client';
import type { EventFeedItem, EventKind } from '../types';
import { useInterval } from '../hooks/useInterval';
import { useLatestRef } from '../hooks/useLatestRef';
import { getErrorMessage } from '../utils/errors';

const DEFAULT_TIME_RANGE = 3 * 60 * 60; // 3 hours
const DEFAULT_REFRESH_INTERVAL = 60000;
const PER_PAGE = 50;
const SEARCH_DEBOUNCE_MS = 400;

type KindFilter = 'all' | EventKind;

const KIND_FILTER_OPTIONS: { label: string; value: KindFilter; title: string }[] = [
  { label: 'All', value: 'all', title: 'Every inbound event' },
  { label: 'Alerts', value: 'alert', title: 'Events that report a problem; only these open or join incidents' },
  { label: 'Changes', value: 'change', title: 'Deploys, config changes, maintenance' },
  { label: 'Info', value: 'info', title: 'Stored only; no action needed' },
  { label: 'Requests', value: 'request', title: 'Work asked of the agent: questions, cron runs, proposal chat' },
];

// toggleInSet returns a copy of `set` with `key` added or removed.
function toggleInSet(set: Set<string>, key: string): Set<string> {
  const next = new Set(set);
  if (next.has(key)) {
    next.delete(key);
  } else {
    next.add(key);
  }
  return next;
}

function withoutKey(set: Set<string>, key: string): Set<string> {
  const next = new Set(set);
  next.delete(key);
  return next;
}

export default function Events() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [items, setItems] = useState<EventFeedItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [successMsg, setSuccessMsg] = useState<string | null>(null);
  const [kindFilter, setKindFilter] = useState<KindFilter>('all');
  // Correlation-reasoning expansion, keyed by event UUID.
  const [reasoningExpanded, setReasoningExpanded] = useState<Set<string>>(new Set());
  // Raw-message expansion + per-event fetch cache.
  const [rawExpanded, setRawExpanded] = useState<Set<string>>(new Set());
  const [rawData, setRawData] = useState<Record<string, RawState>>({});
  // Incident-response expansion (by event UUID) + per-incident fetch cache.
  const [responseExpanded, setResponseExpanded] = useState<Set<string>>(new Set());
  const [responseData, setResponseData] = useState<Record<string, ResponseState>>({});
  // Search: `search` mirrors the input; `activeSearch` is what the API sees.
  // Seeded from ?search= (or ?event=) so event links are shareable.
  const [initialSearch] = useState(() => searchParams.get('search') || searchParams.get('event') || '');
  const [search, setSearch] = useState(initialSearch);
  const [activeSearch, setActiveSearch] = useState(initialSearch);
  const searchDebounceRef = useRef<number | null>(null);
  // The alert currently being reassigned via the Move modal.
  const [moveTarget, setMoveTarget] = useState<EventFeedItem | null>(null);

  // Pagination
  const [page, setPage] = useState(1);
  const [totalPages, setTotalPages] = useState(0);
  const [totalItems, setTotalItems] = useState(0);

  // Time range
  const [initialNow] = useState(() => Math.floor(Date.now() / 1000));
  const [timeFrom, setTimeFrom] = useState(initialNow - DEFAULT_TIME_RANGE);
  const [timeTo, setTimeTo] = useState(initialNow);
  const [relativeRange, setRelativeRange] = useState<number | null>(DEFAULT_TIME_RANGE);
  const [refreshInterval, setRefreshInterval] = useState(DEFAULT_REFRESH_INTERVAL);

  const loadEvents = useCallback(async (
    from?: number,
    to?: number,
    isRefresh?: boolean,
    pageOverride?: number,
    kindOverride?: KindFilter,
    searchOverride?: string,
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

      const effectiveKind = kindOverride ?? kindFilter;
      const effectiveSearch = (searchOverride ?? activeSearch).trim();
      const currentPage = pageOverride ?? page;
      const result = await eventsApi.list({
        from: effectiveFrom,
        to: effectiveTo,
        page: currentPage,
        perPage: PER_PAGE,
        kind: effectiveKind === 'all' ? undefined : effectiveKind,
        search: effectiveSearch || undefined,
      });
      setItems(result.data);
      setTotalPages(result.pagination.total_pages);
      setTotalItems(result.pagination.total);

      if (isRefresh && relativeRange !== null) {
        setTimeFrom(effectiveFrom!);
        setTimeTo(effectiveTo!);
      }
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load events'));
    } finally {
      setLoading(false);
    }
  }, [timeFrom, timeTo, relativeRange, page, kindFilter, activeSearch]);

  // Initial load (read through a ref so the effect runs exactly once)
  const loadEventsRef = useLatestRef(loadEvents);
  useEffect(() => {
    void loadEventsRef.current();
  }, [loadEventsRef]);

  useInterval(() => { void loadEvents(undefined, undefined, true); }, refreshInterval);

  const handleTimeRangeChange = useCallback((from: number, to: number, relativeDuration?: number | null) => {
    setTimeFrom(from);
    setTimeTo(to);
    setPage(1);
    setRelativeRange(relativeDuration ?? null);
    loadEvents(from, to, false, 1);
  }, [loadEvents]);

  const handlePageChange = useCallback((newPage: number) => {
    setPage(newPage);
    loadEvents(undefined, undefined, false, newPage);
  }, [loadEvents]);

  const handleKindFilter = useCallback((k: KindFilter) => {
    setKindFilter(k);
    setPage(1);
    loadEvents(undefined, undefined, false, 1, k);
  }, [loadEvents]);

  const toggleReasoning = useCallback((uuid: string) => {
    setReasoningExpanded(prev => toggleInSet(prev, uuid));
  }, []);

  // fetchRaw lazily loads (and caches) an event's raw payload.
  const fetchRaw = useCallback((item: EventFeedItem) => {
    const uuid = item.event_uuid;
    setRawData(prevData => {
      if (prevData[uuid] && !prevData[uuid].error) return prevData;
      eventsApi.raw(uuid)
        .then(res => setRawData(d => ({ ...d, [uuid]: { loading: false, raw: res.raw, original_message: res.original_message } })))
        .catch(err => setRawData(d => ({ ...d, [uuid]: { loading: false, error: getErrorMessage(err, 'Failed to load raw message') } })));
      return { ...prevData, [uuid]: { loading: true } };
    });
  }, []);

  const toggleRaw = useCallback((item: EventFeedItem) => {
    const uuid = item.event_uuid;
    setRawExpanded(prev => {
      if (prev.has(uuid)) return withoutKey(prev, uuid);
      fetchRaw(item);
      return new Set(prev).add(uuid);
    });
  }, [fetchRaw]);

  // toggleRowDetails is the whole-row click target: it opens (or closes) the
  // raw message, the class and the correlation reasoning together.
  const toggleRowDetails = useCallback((item: EventFeedItem) => {
    const uuid = item.event_uuid;
    const isOpen = rawExpanded.has(uuid) || reasoningExpanded.has(uuid);
    if (isOpen) {
      setRawExpanded(prev => withoutKey(prev, uuid));
      setReasoningExpanded(prev => withoutKey(prev, uuid));
      return;
    }
    setRawExpanded(prev => new Set(prev).add(uuid));
    fetchRaw(item);
    if (item.correlation_reasoning) {
      setReasoningExpanded(prev => new Set(prev).add(uuid));
    }
  }, [rawExpanded, reasoningExpanded, fetchRaw]);

  // toggleResponse expands an inline panel with the linked incident's final
  // response, fetched on demand through the lightweight /response endpoint.
  const toggleResponse = useCallback((item: EventFeedItem) => {
    const incidentUUID = item.incident_uuid;
    if (!incidentUUID) return;
    const uuid = item.event_uuid;
    setResponseExpanded(prev => {
      if (prev.has(uuid)) return withoutKey(prev, uuid);
      setResponseData(prevData => {
        if (prevData[incidentUUID] && !prevData[incidentUUID].error) return prevData;
        eventsApi.incidentResponse(incidentUUID)
          .then(res => setResponseData(d => ({ ...d, [incidentUUID]: { loading: false, title: res.title, status: res.status, response: res.response } })))
          .catch(err => setResponseData(d => ({ ...d, [incidentUUID]: { loading: false, error: getErrorMessage(err, 'Failed to load incident response') } })));
        return { ...prevData, [incidentUUID]: { loading: true } };
      });
      return new Set(prev).add(uuid);
    });
  }, []);

  // applySearch commits a search term: syncs the URL (?search=) so the view is
  // shareable, resets to page 1, and reloads.
  const applySearch = useCallback((term: string) => {
    const trimmed = term.trim();
    setActiveSearch(trimmed);
    setPage(1);
    setSearchParams(prev => {
      const next = new URLSearchParams(prev);
      if (trimmed) next.set('search', trimmed); else next.delete('search');
      next.delete('event');
      return next;
    }, { replace: true });
    loadEvents(undefined, undefined, false, 1, undefined, trimmed);
  }, [loadEvents, setSearchParams]);

  const handleSearchInput = useCallback((value: string) => {
    setSearch(value);
    if (searchDebounceRef.current) window.clearTimeout(searchDebounceRef.current);
    searchDebounceRef.current = window.setTimeout(() => applySearch(value), SEARCH_DEBOUNCE_MS);
  }, [applySearch]);

  // Clicking an event's ID chip filters the list down to that event.
  const filterByEventId = useCallback((uuid: string) => {
    setSearch(uuid);
    if (searchDebounceRef.current) window.clearTimeout(searchDebounceRef.current);
    applySearch(uuid);
  }, [applySearch]);

  const clearSearch = useCallback(() => {
    setSearch('');
    applySearch('');
  }, [applySearch]);

  const handleMoved = useCallback((result: { incidentUUID: string; isNew: boolean }) => {
    setMoveTarget(null);
    setSuccessMsg(result.isNew
      ? `Alert unlinked. New incident: ${result.incidentUUID.slice(0, 8)}…`
      : `Alert linked to incident ${result.incidentUUID.slice(0, 8)}…`);
    setTimeout(() => setSuccessMsg(null), 6000);
    setPage(1);
    loadEvents(undefined, undefined, false, 1);
  }, [loadEvents]);

  const groups = useMemo(
    () => groupEvents(items, bucketSize(timeTo - timeFrom)),
    [items, timeFrom, timeTo],
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2.5">
        <label className="relative flex-[1_1_260px] max-w-md">
          <span className="sr-only">Search events</span>
          <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-fg-subtle pointer-events-none" aria-hidden="true" />
          <input
            type="search"
            value={search}
            onChange={(e) => handleSearchInput(e.target.value)}
            placeholder="Search by event ID, title or host"
            className="input-field h-9 !pl-8 !pr-8"
          />
          {search && (
            <button
              type="button"
              onClick={clearSearch}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-fg-subtle hover:text-fg"
              title="Clear search"
              aria-label="Clear search"
            >
              <X size={14} />
            </button>
          )}
        </label>
        <SegmentedControl value={kindFilter} options={KIND_FILTER_OPTIONS} onChange={handleKindFilter} />
        <TimeRangePicker
          from={timeFrom}
          to={timeTo}
          refreshInterval={refreshInterval}
          onChange={handleTimeRangeChange}
          onRefreshIntervalChange={setRefreshInterval}
        />
        <span className="flex-1" />
        <button
          type="button"
          onClick={() => loadEvents()}
          disabled={loading}
          className="btn btn-ghost h-9 w-9 p-0 inline-flex items-center justify-center"
          title="Refresh"
          aria-label="Refresh"
        >
          <RefreshCw size={15} className={loading ? 'animate-spin' : ''} />
        </button>
      </div>

      {error && <ErrorMessage message={error} />}
      {successMsg && <SuccessMessage message={successMsg} />}

      {loading && items.length === 0 ? (
        <LoadingSpinner />
      ) : items.length === 0 ? (
        <EmptyState
          icon={Rss}
          title="No events in this time range"
          description="Choose a longer time range or another event class."
        />
      ) : (
        <div className="flex flex-col gap-5">
          {groups.map((group) => (
            <section key={group.key} aria-labelledby={`events-${group.key}`}>
              <h2 id={`events-${group.key}`} className="flex items-baseline gap-2 mb-2 text-sm">
                <span className="font-medium text-fg">{group.label}</span>
                <span className="text-fg-subtle">
                  {group.items.length} {group.items.length === 1 ? 'event' : 'events'}
                </span>
              </h2>
              <ul className="rounded-xl border border-line bg-surface overflow-hidden">
                {group.items.map((item) => (
                  <EventRow
                    key={item.event_uuid}
                    item={item}
                    reasoningExpanded={reasoningExpanded.has(item.event_uuid)}
                    rawExpanded={rawExpanded.has(item.event_uuid)}
                    responseExpanded={responseExpanded.has(item.event_uuid)}
                    rawState={rawData[item.event_uuid]}
                    responseState={item.incident_uuid ? responseData[item.incident_uuid] : undefined}
                    onToggleDetails={() => toggleRowDetails(item)}
                    onFilterByEventId={() => filterByEventId(item.event_uuid)}
                    onToggleResponse={() => toggleResponse(item)}
                    onToggleReasoning={() => toggleReasoning(item.event_uuid)}
                    onToggleRaw={() => toggleRaw(item)}
                    onMove={() => setMoveTarget(item)}
                  />
                ))}
              </ul>
            </section>
          ))}
          <Pagination
            page={page}
            totalPages={totalPages}
            totalItems={totalItems}
            onPageChange={handlePageChange}
          />
        </div>
      )}

      {moveTarget?.alert_uuid && (
        <MoveIncidentModal
          alertUUID={moveTarget.alert_uuid}
          alertTitle={moveTarget.title}
          currentIncidentUUID={moveTarget.incident_uuid}
          onClose={() => setMoveTarget(null)}
          onMoved={handleMoved}
        />
      )}
    </div>
  );
}
