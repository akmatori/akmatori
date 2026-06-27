import { useEffect, useState, useRef, useCallback } from 'react';
import { RefreshCw, X, Plus, MessageSquare, Activity, Clock, CheckCircle, AlertCircle, Terminal, Zap, Timer, Bell } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage } from '../components/ErrorMessage';
import TimeRangePicker from '../components/TimeRangePicker';
import IncidentDetailView from '../components/IncidentDetailView';
import TrendSparkline from '../components/TrendSparkline';
import { incidentsApi } from '../api/client';
import type { Incident } from '../types';
import { ChevronLeft, ChevronRight } from 'lucide-react';

// Default: last 30 minutes
const DEFAULT_TIME_RANGE = 30 * 60;
// Default: refresh every 1 minute
const DEFAULT_REFRESH_INTERVAL = 60000;

const formatRelative = (iso: string): string => {
  const diffMs = Date.now() - new Date(iso).getTime();
  const diffSec = Math.max(0, Math.floor(diffMs / 1000));
  if (diffSec < 60) return `${diffSec}s`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 7) return `${diffDay}d`;
  const diffWeek = Math.floor(diffDay / 7);
  return `${diffWeek}w`;
};

const formatCountdown = (iso: string): string => {
  const remainingMs = new Date(iso).getTime() - Date.now();
  if (remainingMs <= 0) return 'expired';
  const remainingMin = Math.floor(remainingMs / 60000);
  if (remainingMin < 60) return `${remainingMin}m left`;
  const remainingHr = Math.floor(remainingMin / 60);
  const remMin = remainingMin % 60;
  if (remainingHr < 24) return `${remainingHr}h ${remMin}m left`;
  const remainingDay = Math.floor(remainingHr / 24);
  return `${remainingDay}d left`;
};

export default function Incidents() {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selectedIncident, setSelectedIncident] = useState<Incident | null>(null);
  const [showModal, setShowModal] = useState(false);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [newTask, setNewTask] = useState('');
  const [creating, setCreating] = useState(false);
  const [createSuccess, setCreateSuccess] = useState<string | null>(null);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const refreshIntervalRef = useRef<number | null>(null);

  // Open/History view toggle
  const [view, setView] = useState<'open' | 'history'>('open');

  // Trend window
  const [trendWindow, setTrendWindow] = useState<'1h' | '3h'>('1h');

  // Pagination state
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(50);
  const [totalPages, setTotalPages] = useState(0);
  const [totalIncidents, setTotalIncidents] = useState(0);

  // Time range picker state (used only in history view)
  const now = Math.floor(Date.now() / 1000);
  const [timeFrom, setTimeFrom] = useState(now - DEFAULT_TIME_RANGE);
  const [timeTo, setTimeTo] = useState(now);
  // Track relative time range duration (null = absolute range)
  const [relativeRange, setRelativeRange] = useState<number | null>(DEFAULT_TIME_RANGE);
  const [listRefreshInterval, setListRefreshInterval] = useState(DEFAULT_REFRESH_INTERVAL);
  const listRefreshRef = useRef<number | null>(null);

  // Load incidents — view-aware: open view omits from/to and applies status filter
  const loadIncidents = useCallback(async (
    from?: number,
    to?: number,
    isRefresh?: boolean,
    pageOverride?: number,
    perPageOverride?: number,
    trendWindowOverride?: '1h' | '3h',
    viewOverride?: 'open' | 'history',
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
        statusFilter = 'pending,running,diagnosed,monitor';
      } else {
        statusFilter = 'completed,failed';
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
      setError(err instanceof Error ? err.message : 'Failed to load incidents');
    } finally {
      setLoading(false);
    }
  }, [timeFrom, timeTo, relativeRange, page, perPage, trendWindow, view]);

  // Initial load
  useEffect(() => {
    loadIncidents();
  }, []);

  // Auto-refresh for incidents list
  useEffect(() => {
    if (listRefreshInterval > 0) {
      listRefreshRef.current = window.setInterval(() => {
        loadIncidents(undefined, undefined, true);
      }, listRefreshInterval);
    }

    return () => {
      if (listRefreshRef.current) {
        clearInterval(listRefreshRef.current);
        listRefreshRef.current = null;
      }
    };
  }, [listRefreshInterval, loadIncidents]);

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

  // Handle refresh interval change
  const handleRefreshIntervalChange = useCallback((interval: number) => {
    setListRefreshInterval(interval);
  }, []);

  const handleTrendWindowChange = useCallback((newWindow: '1h' | '3h') => {
    setTrendWindow(newWindow);
    loadIncidents(undefined, undefined, false, undefined, undefined, newWindow);
  }, [loadIncidents]);

  const handleViewChange = useCallback((newView: 'open' | 'history') => {
    setView(newView);
    setPage(1);
    loadIncidents(undefined, undefined, false, 1, undefined, undefined, newView);
  }, [loadIncidents]);

  useEffect(() => {
    if (showModal && selectedIncident && selectedIncident.status === 'running' && autoRefresh) {
      refreshIntervalRef.current = window.setInterval(async () => {
        try {
          const updated = await incidentsApi.get(selectedIncident.uuid);
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
        } catch (err) {
          console.error('Failed to refresh incident:', err);
        }
      }, 2000);
    }

    return () => {
      if (refreshIntervalRef.current) {
        clearInterval(refreshIntervalRef.current);
        refreshIntervalRef.current = null;
      }
    };
  }, [showModal, selectedIncident?.uuid, selectedIncident?.status, autoRefresh]);

  const getStatusConfig = (status: string, monitorUntil?: string) => {
    switch (status) {
      case 'completed':
        return { class: 'badge-success', icon: CheckCircle, label: 'Resolved', subLabel: undefined };
      case 'monitor':
        return {
          class: 'badge-success',
          icon: CheckCircle,
          label: 'Monitoring',
          subLabel: monitorUntil ? formatCountdown(monitorUntil) : undefined,
        };
      case 'pending':
      case 'running':
        return { class: 'badge-primary', icon: Activity, label: 'Ongoing', subLabel: undefined };
      case 'diagnosed':
        return { class: 'badge-purple', icon: Activity, label: 'Ongoing', subLabel: undefined };
      case 'failed':
        return { class: 'badge-error', icon: AlertCircle, label: 'Failed', subLabel: undefined };
      default:
        return { class: 'badge-default', icon: Clock, label: 'Pending', subLabel: undefined };
    }
  };

  const getSourceKindChip = (kind?: string) => {
    switch (kind) {
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
  };

  // Format execution time in human-readable format
  const formatExecutionTime = (ms: number): string => {
    if (!ms || ms <= 0) return '-';
    if (ms < 1000) return `${ms}ms`;
    const seconds = ms / 1000;
    if (seconds < 60) return `${seconds.toFixed(1)}s`;
    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = seconds % 60;
    if (minutes < 60) return `${minutes}m ${Math.round(remainingSeconds)}s`;
    const hours = Math.floor(minutes / 60);
    const remainingMinutes = minutes % 60;
    return `${hours}h ${remainingMinutes}m`;
  };

  // Format token count with thousands separator
  const formatTokens = (tokens: number): string => {
    if (!tokens || tokens <= 0) return '-';
    return tokens.toLocaleString();
  };

  const openModal = useCallback(async (incident: Incident) => {
    try {
      const latest = await incidentsApi.get(incident.uuid);
      setSelectedIncident(latest);
    } catch {
      setSelectedIncident(incident);
    }
    setShowModal(true);
  }, []);

  const closeModal = () => {
    setShowModal(false);
    setSelectedIncident(null);
  };

  const handleCreateIncident = async () => {
    if (!newTask.trim()) return;

    try {
      setCreating(true);
      setError('');
      const response = await incidentsApi.create({ task: newTask.trim() });

      // Fetch the full incident and add to list immediately, but only when in
      // open view — a new incident starts as pending and would be out of place
      // in the history view which filters for completed/failed.
      const newIncident = await incidentsApi.get(response.uuid);
      if (view === 'open') {
        setIncidents(prev => [newIncident, ...prev]);
      }

      setCreateSuccess(`Incident created: ${response.uuid.slice(0, 8)}...`);
      setNewTask('');
      setShowCreateModal(false);
      setTimeout(() => setCreateSuccess(null), 5000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create incident');
    } finally {
      setCreating(false);
    }
  };

  const selectedStatusConfig = selectedIncident
    ? getStatusConfig(selectedIncident.status, selectedIncident.monitor_until)
    : null;

  return (
    <div>
      <PageHeader
        title="Incidents"
        description="Monitor all incident manager sessions and execution logs"
        action={
          <div className="flex items-center gap-3">
            {/* Open/History view toggle */}
            <div className="flex rounded-lg border border-gray-300 dark:border-gray-600 overflow-hidden">
              <button
                onClick={() => handleViewChange('open')}
                className={`px-3 py-1 text-xs font-medium transition-colors ${
                  view === 'open'
                    ? 'bg-primary-500 text-white'
                    : 'bg-white dark:bg-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-700'
                }`}
              >
                Open
              </button>
              <button
                onClick={() => handleViewChange('history')}
                className={`px-3 py-1 text-xs font-medium border-l border-gray-300 dark:border-gray-600 transition-colors ${
                  view === 'history'
                    ? 'bg-primary-500 text-white'
                    : 'bg-white dark:bg-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-700'
                }`}
              >
                History
              </button>
            </div>
            {/* Trend window segmented toggle */}
            <div className="flex rounded-lg border border-gray-300 dark:border-gray-600 overflow-hidden">
              <button
                onClick={() => handleTrendWindowChange('1h')}
                className={`px-2.5 py-1 text-xs font-medium transition-colors ${
                  trendWindow === '1h'
                    ? 'bg-primary-500 text-white'
                    : 'bg-white dark:bg-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-700'
                }`}
                title="Show 1-hour trend"
              >
                1h
              </button>
              <button
                onClick={() => handleTrendWindowChange('3h')}
                className={`px-2.5 py-1 text-xs font-medium border-l border-gray-300 dark:border-gray-600 transition-colors ${
                  trendWindow === '3h'
                    ? 'bg-primary-500 text-white'
                    : 'bg-white dark:bg-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-700'
                }`}
                title="Show 3-hour trend"
              >
                3h
              </button>
            </div>
            {view === 'history' && (
              <TimeRangePicker
                from={timeFrom}
                to={timeTo}
                refreshInterval={listRefreshInterval}
                onChange={handleTimeRangeChange}
                onRefreshIntervalChange={handleRefreshIntervalChange}
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
            <div className="py-16 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
              <Activity className="w-12 h-12 mx-auto text-gray-400 mb-3" />
              <p className="text-gray-500 dark:text-gray-400">
                {view === 'open' ? 'No open incidents' : 'No incidents found'}
              </p>
              <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">
                {view === 'open' ? 'All clear — no active or monitoring incidents' : 'Create a new incident to get started'}
              </p>
            </div>
          ) : (
            <>
            <div className="overflow-x-auto">
              <table className="table">
                <thead>
                  <tr>
                    <th>Issue</th>
                    <th>Trend</th>
                    <th>Age</th>
                    <th>Last seen</th>
                    <th>Status</th>
                    <th>Alerts</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {incidents.map((incident) => {
                    const statusConfig = getStatusConfig(incident.status, incident.monitor_until);
                    const StatusIcon = statusConfig.icon;
                    const alertCount = incident.alert_count ?? 0;

                    return (
                      <tr key={incident.id}>
                        <td className="max-w-xs">
                          <div className="flex flex-col gap-0.5">
                            <span
                              className="text-gray-800 dark:text-gray-100 font-semibold text-sm truncate"
                              title={incident.title || undefined}
                            >
                              {incident.title || <span className="text-gray-400 italic font-normal">No title</span>}
                            </span>
                            <div className="flex items-center gap-1.5 flex-wrap">
                              {getSourceKindChip(incident.source_kind)}
                              <code className="text-xs text-gray-400 dark:text-gray-500">
                                {incident.uuid.slice(0, 8)}
                              </code>
                              {incident.source && (
                                <span className="text-xs text-gray-400 dark:text-gray-500 truncate max-w-[120px]" title={incident.source}>
                                  {incident.source}
                                </span>
                              )}
                            </div>
                          </div>
                        </td>
                        <td className="text-gray-500 dark:text-gray-400">
                          <TrendSparkline buckets={incident.trend ?? []} />
                        </td>
                        <td className="text-gray-500 dark:text-gray-400 text-sm whitespace-nowrap">
                          {formatRelative(incident.first_seen ?? incident.started_at)}
                        </td>
                        <td className="text-gray-500 dark:text-gray-400 text-sm whitespace-nowrap">
                          {formatRelative(incident.last_seen ?? incident.started_at)}
                        </td>
                        <td>
                          <div className="flex flex-col gap-0.5">
                            <span className={`badge ${statusConfig.class} inline-flex items-center gap-1`}>
                              <StatusIcon className="w-3 h-3" />
                              {statusConfig.label}
                            </span>
                            {statusConfig.subLabel && (
                              <span className="text-xs text-gray-400 dark:text-gray-500">
                                {statusConfig.subLabel}
                              </span>
                            )}
                          </div>
                        </td>
                        <td>
                          {alertCount > 1 ? (
                            <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
                              <Bell className="w-3 h-3" />
                              {alertCount}
                            </span>
                          ) : (
                            <span className="text-xs text-gray-400 dark:text-gray-500">
                              {alertCount}
                            </span>
                          )}
                        </td>
                        <td>
                          <div className="flex items-center gap-2">
                            <button
                              onClick={() => openModal(incident)}
                              className={`btn btn-ghost p-1.5 ${incident.status === 'running' ? 'text-primary-500 animate-pulse' : ''}`}
                              title="View reasoning log"
                            >
                              <Terminal className="w-4 h-4" />
                            </button>
                            {(incident.status === 'completed' || incident.status === 'monitor' || incident.status === 'failed') && (
                              <button
                                onClick={() => openModal(incident)}
                                className="btn btn-ghost p-1.5"
                                title="View response"
                              >
                                <MessageSquare className="w-4 h-4" />
                              </button>
                            )}
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            {/* Pagination Controls */}
            {totalPages > 1 && (
              <div className="flex items-center justify-between px-4 py-3 border-t border-gray-200 dark:border-gray-700">
                <div className="flex items-center gap-4">
                  <span className="text-sm text-gray-500 dark:text-gray-400">
                    Page {page} of {totalPages} ({totalIncidents.toLocaleString()} total)
                  </span>
                  <select
                    value={perPage}
                    onChange={(e) => {
                      const newPerPage = Number(e.target.value);
                      setPerPage(newPerPage);
                      setPage(1);
                      loadIncidents(undefined, undefined, false, 1, newPerPage);
                    }}
                    className="text-sm border border-gray-300 dark:border-gray-600 rounded px-2 py-1 bg-white dark:bg-gray-800 text-gray-700 dark:text-gray-300"
                  >
                    <option value={25}>25 per page</option>
                    <option value={50}>50 per page</option>
                    <option value={100}>100 per page</option>
                  </select>
                </div>
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

      {/* Detail Modal */}
      {showModal && selectedIncident && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-800 rounded-xl shadow-2xl max-w-5xl w-full max-h-[90vh] flex flex-col animate-fade-in">
            {/* Modal Header */}
            <div className="flex items-center justify-between p-6 border-b border-gray-200 dark:border-gray-700">
              <div>
                <div className="flex items-center gap-3">
                  <h2 className="text-xl font-semibold text-gray-900 dark:text-white">
                    {selectedIncident.title || 'Incident Details'}
                  </h2>
                  {selectedStatusConfig && <span className={`badge ${selectedStatusConfig.class}`}>{selectedStatusConfig.label}</span>}
                </div>
                <div className="mt-1 flex items-center gap-4 text-sm text-gray-500 dark:text-gray-400">
                  <span>
                    UUID: <code className="text-primary-600 dark:text-primary-400">{selectedIncident.uuid.slice(0, 8)}</code>
                  </span>
                  <span className="text-gray-300 dark:text-gray-600">|</span>
                  <span>Source: {selectedIncident.source}</span>
                </div>
              </div>
              <button onClick={closeModal} className="btn btn-ghost p-2" title="Close">
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Shared Detail View */}
            <IncidentDetailView incident={selectedIncident} autoRefresh={autoRefresh} />

            {/* Modal Footer */}
            <div className="flex items-center justify-between p-6 border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
              <div className="flex items-center gap-4">
                {selectedIncident.status === 'running' && (
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={autoRefresh}
                      onChange={(e) => setAutoRefresh(e.target.checked)}
                    />
                    <span className="text-sm text-gray-600 dark:text-gray-400">Auto-refresh (2s)</span>
                  </label>
                )}
                {(selectedIncident.status === 'completed' || selectedIncident.status === 'monitor' || selectedIncident.status === 'failed') && (
                  <div className="flex items-center gap-4 text-sm text-gray-500 dark:text-gray-400">
                    {selectedIncident.execution_time_ms > 0 && (
                      <span className="flex items-center gap-1.5">
                        <Timer className="w-4 h-4" />
                        {formatExecutionTime(selectedIncident.execution_time_ms)}
                      </span>
                    )}
                    {selectedIncident.tokens_used > 0 && (
                      <span className="flex items-center gap-1.5">
                        <Zap className="w-4 h-4" />
                        {formatTokens(selectedIncident.tokens_used)} tokens
                      </span>
                    )}
                  </div>
                )}
              </div>
              <button onClick={closeModal} className="btn btn-secondary">
                Close
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Create Incident Modal */}
      {showCreateModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-800 rounded-xl shadow-2xl max-w-2xl w-full animate-fade-in">
            {/* Modal Header */}
            <div className="flex items-center justify-between p-6 border-b border-gray-200 dark:border-gray-700">
              <div>
                <h2 className="text-xl font-semibold text-gray-900 dark:text-white">Create Incident</h2>
                <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  Start an incident investigation by providing a task description
                </p>
              </div>
              <button onClick={() => setShowCreateModal(false)} className="btn btn-ghost p-2" title="Close">
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Modal Body */}
            <div className="p-6">
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
                Task Description <span className="text-red-500">*</span>
              </label>
              <textarea
                value={newTask}
                onChange={(e) => setNewTask(e.target.value)}
                placeholder="Describe the task or investigation you want to perform..."
                className="input-field min-h-[180px] resize-y"
                autoFocus
              />
              <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">
                The incident manager will analyze this task and coordinate with skills to resolve it.
              </p>
            </div>

            {/* Modal Footer */}
            <div className="flex items-center justify-end gap-3 p-6 border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
              <button onClick={() => setShowCreateModal(false)} className="btn btn-secondary" disabled={creating}>
                Cancel
              </button>
              <button
                onClick={handleCreateIncident}
                className="btn btn-primary"
                disabled={creating || !newTask.trim()}
              >
                {creating ? (
                  <>
                    <RefreshCw className="w-4 h-4 animate-spin" />
                    Creating...
                  </>
                ) : (
                  <>
                    <Plus className="w-4 h-4" />
                    Create
                  </>
                )}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
