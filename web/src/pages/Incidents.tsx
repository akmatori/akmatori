import { useEffect, useState, useCallback } from 'react';
import { RefreshCw, Plus, Activity } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage, { SuccessMessage } from '../components/ErrorMessage';
import TimeRangePicker from '../components/TimeRangePicker';
import EmptyState from '../components/shared/EmptyState';
import Pagination from '../components/shared/Pagination';
import SegmentedControl from '../components/shared/SegmentedControl';
import IncidentsTable from '../components/incidents/IncidentsTable';
import IncidentDetailModal from '../components/incidents/IncidentDetailModal';
import CreateIncidentModal from '../components/incidents/CreateIncidentModal';
import { incidentsApi } from '../api/client';
import type { Incident } from '../types';
import { useInterval } from '../hooks/useInterval';
import { useLatestRef } from '../hooks/useLatestRef';
import { getErrorMessage } from '../utils/errors';

// Default: last 30 minutes
const DEFAULT_TIME_RANGE = 30 * 60;
// Default: refresh every 1 minute
const DEFAULT_REFRESH_INTERVAL = 60000;
// Poll a running incident shown in the modal this often
const RUNNING_POLL_INTERVAL = 2000;

type IncidentView = 'open' | 'history';
type TrendWindow = '1h' | '3h';

const VIEW_OPTIONS: { value: IncidentView; label: string }[] = [
  { value: 'open', label: 'Open' },
  { value: 'history', label: 'History' },
];

const TREND_OPTIONS: { value: TrendWindow; label: string; title: string }[] = [
  { value: '1h', label: '1h', title: 'Show 1-hour trend' },
  { value: '3h', label: '3h', title: 'Show 3-hour trend' },
];

// alert_active = "completed" alert-sourced incidents whose linked alert is
// still firing — still-open work, not history.
const OPEN_STATUS_FILTER = 'pending,running,diagnosed,monitor,alert_active';
const HISTORY_STATUS_FILTER = 'completed,failed,closed';

export default function Incidents() {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selectedIncident, setSelectedIncident] = useState<Incident | null>(null);
  const [showModal, setShowModal] = useState(false);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [createSuccess, setCreateSuccess] = useState<string | null>(null);
  const [autoRefresh, setAutoRefresh] = useState(true);

  // Open/History view toggle
  const [view, setView] = useState<IncidentView>('open');

  // Trend window
  const [trendWindow, setTrendWindow] = useState<TrendWindow>('1h');

  // Pagination state
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(50);
  const [totalPages, setTotalPages] = useState(0);
  const [totalIncidents, setTotalIncidents] = useState(0);

  // Time range picker state (used only in history view)
  const [initialNow] = useState(() => Math.floor(Date.now() / 1000));
  const [timeFrom, setTimeFrom] = useState(initialNow - DEFAULT_TIME_RANGE);
  const [timeTo, setTimeTo] = useState(initialNow);
  // Track relative time range duration (null = absolute range)
  const [relativeRange, setRelativeRange] = useState<number | null>(DEFAULT_TIME_RANGE);
  const [listRefreshInterval, setListRefreshInterval] = useState(DEFAULT_REFRESH_INTERVAL);

  // Load incidents — view-aware: open view omits from/to and applies status filter
  const loadIncidents = useCallback(async (
    from?: number,
    to?: number,
    isRefresh?: boolean,
    pageOverride?: number,
    perPageOverride?: number,
    trendWindowOverride?: TrendWindow,
    viewOverride?: IncidentView,
  ) => {
    try {
      setLoading(true);
      setError('');
      const currentNow = Math.floor(Date.now() / 1000);
      const effectiveView = viewOverride ?? view;

      let effectiveFrom: number | undefined;
      let effectiveTo: number | undefined;
      let statusFilter: string;

      if (effectiveView === 'open') {
        effectiveFrom = undefined;
        effectiveTo = undefined;
        statusFilter = OPEN_STATUS_FILTER;
      } else {
        statusFilter = HISTORY_STATUS_FILTER;
        if (isRefresh && relativeRange !== null) {
          effectiveFrom = currentNow - relativeRange;
          effectiveTo = currentNow;
        } else {
          effectiveFrom = from ?? timeFrom;
          effectiveTo = to ?? timeTo;
        }
      }

      const currentPage = pageOverride ?? page;
      const currentPerPage = perPageOverride ?? perPage;
      const effectiveTrendWindow = trendWindowOverride ?? trendWindow;
      const result = await incidentsApi.list(effectiveFrom, effectiveTo, currentPage, currentPerPage, effectiveTrendWindow, statusFilter);
      setIncidents(result.data);
      setTotalPages(result.pagination.total_pages);
      setTotalIncidents(result.pagination.total);

      // Update time display state for relative ranges in history view
      if (effectiveView === 'history' && isRefresh && relativeRange !== null) {
        setTimeFrom(effectiveFrom!);
        setTimeTo(effectiveTo!);
      }
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load incidents'));
    } finally {
      setLoading(false);
    }
  }, [timeFrom, timeTo, relativeRange, page, perPage, trendWindow, view]);

  // Initial load (read through a ref so the effect runs exactly once)
  const loadIncidentsRef = useLatestRef(loadIncidents);
  useEffect(() => {
    void loadIncidentsRef.current();
  }, [loadIncidentsRef]);

  // Auto-refresh for incidents list
  useInterval(() => { void loadIncidents(undefined, undefined, true); }, listRefreshInterval);

  // Handle time range change
  const handleTimeRangeChange = useCallback((from: number, to: number, relativeDuration?: number | null) => {
    setTimeFrom(from);
    setTimeTo(to);
    setPage(1);
    // Track if this is a relative range (for auto-refresh recalculation)
    setRelativeRange(relativeDuration ?? null);
    loadIncidents(from, to, false, 1);
  }, [loadIncidents]);

  // Handle page change
  const handlePageChange = useCallback((newPage: number) => {
    setPage(newPage);
    loadIncidents(undefined, undefined, false, newPage);
  }, [loadIncidents]);

  const handlePerPageChange = useCallback((newPerPage: number) => {
    setPerPage(newPerPage);
    setPage(1);
    loadIncidents(undefined, undefined, false, 1, newPerPage);
  }, [loadIncidents]);

  const handleTrendWindowChange = useCallback((newWindow: TrendWindow) => {
    setTrendWindow(newWindow);
    loadIncidents(undefined, undefined, false, undefined, undefined, newWindow);
  }, [loadIncidents]);

  const handleViewChange = useCallback((newView: IncidentView) => {
    setView(newView);
    setPage(1);
    loadIncidents(undefined, undefined, false, 1, undefined, undefined, newView);
  }, [loadIncidents]);

  // Apply a refreshed incident to both the modal and its list row. The list
  // endpoint carries aggregate fields the single-incident endpoint lacks, so
  // keep those when the fresh copy omits them.
  const applyIncidentUpdate = useCallback((updated: Incident) => {
    setSelectedIncident(updated);
    setIncidents(prev => prev.map(i => {
      if (i.uuid !== updated.uuid) return i;
      return {
        ...updated,
        first_seen: updated.first_seen ?? i.first_seen,
        last_seen: updated.last_seen ?? i.last_seen,
        trend: updated.trend ?? i.trend,
      };
    }));
  }, []);

  // Poll the selected incident while it is running and the modal is open.
  const pollSelectedIncident = useCallback(async () => {
    if (!selectedIncident) return;
    try {
      applyIncidentUpdate(await incidentsApi.get(selectedIncident.uuid));
    } catch (err) {
      console.error('Failed to refresh incident:', err);
    }
  }, [selectedIncident, applyIncidentUpdate]);

  const pollingActive = showModal && selectedIncident?.status === 'running' && autoRefresh;
  useInterval(() => { void pollSelectedIncident(); }, pollingActive ? RUNNING_POLL_INTERVAL : null);

  const openModal = useCallback(async (incident: Incident) => {
    try {
      const latest = await incidentsApi.get(incident.uuid);
      setSelectedIncident(latest);
    } catch {
      setSelectedIncident(incident);
    }
    setShowModal(true);
  }, []);

  const closeModal = useCallback(() => {
    setShowModal(false);
    setSelectedIncident(null);
  }, []);

  const handleIncidentCreated = useCallback((incident: Incident) => {
    // Only the open view lists a brand-new (pending) incident — it would be
    // out of place in the history view which filters for completed/failed.
    if (view === 'open') {
      setIncidents(prev => [incident, ...prev]);
    }
    setError('');
    setCreateSuccess(`Incident created: ${incident.uuid.slice(0, 8)}...`);
    setShowCreateModal(false);
    setTimeout(() => setCreateSuccess(null), 5000);
  }, [view]);

  return (
    <div>
      <PageHeader
        title="Incidents"
        description="Monitor all incident manager sessions and execution logs"
        action={
          <div className="flex items-center gap-3">
            <SegmentedControl value={view} options={VIEW_OPTIONS} onChange={handleViewChange} />
            <SegmentedControl
              value={trendWindow}
              options={TREND_OPTIONS}
              onChange={handleTrendWindowChange}
              buttonClassName="px-2.5 py-1"
            />
            {view === 'history' && (
              <TimeRangePicker
                from={timeFrom}
                to={timeTo}
                refreshInterval={listRefreshInterval}
                onChange={handleTimeRangeChange}
                onRefreshIntervalChange={setListRefreshInterval}
              />
            )}
            <button onClick={() => setShowCreateModal(true)} className="btn btn-primary">
              <Plus className="w-4 h-4" />
              New Incident
            </button>
            <button onClick={() => loadIncidents()} className="btn btn-secondary" disabled={loading}>
              <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
              Refresh
            </button>
          </div>
        }
      />

      {error && <ErrorMessage message={error} />}
      {createSuccess && <SuccessMessage message={createSuccess} />}

      {loading ? (
        <LoadingSpinner />
      ) : (
        <div className="card">
          {incidents.length === 0 ? (
            <EmptyState
              icon={Activity}
              title={view === 'open' ? 'No open incidents' : 'No incidents found'}
              description={view === 'open' ? 'All clear — no active or monitoring incidents' : 'Create a new incident to get started'}
            />
          ) : (
            <>
              <IncidentsTable incidents={incidents} onOpen={openModal} />
              <Pagination
                page={page}
                totalPages={totalPages}
                totalItems={totalIncidents}
                onPageChange={handlePageChange}
                perPage={perPage}
                onPerPageChange={handlePerPageChange}
              />
            </>
          )}
        </div>
      )}

      {showModal && selectedIncident && (
        <IncidentDetailModal
          incident={selectedIncident}
          autoRefresh={autoRefresh}
          onAutoRefreshChange={setAutoRefresh}
          onIncidentUpdate={applyIncidentUpdate}
          onClose={closeModal}
        />
      )}

      {showCreateModal && (
        <CreateIncidentModal
          onClose={() => setShowCreateModal(false)}
          onCreated={handleIncidentCreated}
          onError={setError}
        />
      )}
    </div>
  );
}
