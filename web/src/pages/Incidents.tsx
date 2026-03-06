import { useEffect, useState, useRef, useCallback } from 'react';
import { RefreshCw, X, Plus, MessageSquare, Activity, Clock, CheckCircle, AlertCircle, Terminal, Zap, Timer } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage } from '../components/ErrorMessage';
import TimeRangePicker from '../components/TimeRangePicker';
import IncidentDetailView from '../components/IncidentDetailView';
import { incidentsApi } from '../api/client';
import type { Incident } from '../types';

// Default: last 30 minutes
const DEFAULT_TIME_RANGE = 30 * 60;
// Default: refresh every 1 minute
const DEFAULT_REFRESH_INTERVAL = 60000;

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

  // Time range picker state
  const now = Math.floor(Date.now() / 1000);
  const [timeFrom, setTimeFrom] = useState(now - DEFAULT_TIME_RANGE);
  const [timeTo, setTimeTo] = useState(now);
  // Track relative time range duration (null = absolute range)
  const [relativeRange, setRelativeRange] = useState<number | null>(DEFAULT_TIME_RANGE);
  const [listRefreshInterval, setListRefreshInterval] = useState(DEFAULT_REFRESH_INTERVAL);
  const listRefreshRef = useRef<number | null>(null);

  // Load incidents with current time range
  const loadIncidents = useCallback(async (from?: number, to?: number, isRefresh?: boolean) => {
    try {
      setLoading(true);
      setError('');
      const currentNow = Math.floor(Date.now() / 1000);

      let effectiveFrom: number;
      let effectiveTo: number;

      if (isRefresh && relativeRange !== null) {
        // For relative ranges on refresh, recalculate both from and to
        effectiveFrom = currentNow - relativeRange;
        effectiveTo = currentNow;
      } else {
        effectiveFrom = from ?? timeFrom;
        effectiveTo = to ?? currentNow;
      }

      const data = await incidentsApi.list(effectiveFrom, effectiveTo);
      setIncidents(data);

      // Update time state for display
      if (isRefresh && relativeRange !== null) {
        setTimeFrom(effectiveFrom);
        setTimeTo(effectiveTo);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load incidents');
    } finally {
      setLoading(false);
    }
  }, [timeFrom, relativeRange]);

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
    // Track if this is a relative range (for auto-refresh recalculation)
    setRelativeRange(relativeDuration ?? null);
    loadIncidents(from, to);
  }, [loadIncidents]);

  // Handle refresh interval change
  const handleRefreshIntervalChange = useCallback((interval: number) => {
    setListRefreshInterval(interval);
  }, []);

  useEffect(() => {
    if (showModal && selectedIncident && selectedIncident.status === 'running' && autoRefresh) {
      refreshIntervalRef.current = window.setInterval(async () => {
        try {
          const updated = await incidentsApi.get(selectedIncident.uuid);
          setSelectedIncident(updated);
          setIncidents(prev => prev.map(i => i.uuid === updated.uuid ? updated : i));
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

  const getStatusConfig = (status: string) => {
    switch (status) {
      case 'completed':
        return { class: 'badge-success', icon: CheckCircle, label: 'Completed' };
      case 'running':
        return { class: 'badge-primary', icon: Activity, label: 'Running' };
      case 'diagnosed':
        return { class: 'badge-purple', icon: CheckCircle, label: 'Diagnosed' };
      case 'observing':
        return { class: 'badge-warning', icon: Clock, label: 'Observing' };
      case 'failed':
        return { class: 'badge-error', icon: AlertCircle, label: 'Failed' };
      default:
        return { class: 'badge-default', icon: Clock, label: 'Pending' };
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

      // Fetch the full incident and add to list immediately
      const newIncident = await incidentsApi.get(response.uuid);
      setIncidents(prev => [newIncident, ...prev]);

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

  return (
    <div>
      <PageHeader
        title="Incidents"
        description="Monitor all incident manager sessions and execution logs"
        action={
          <div className="flex items-center gap-3">
            <TimeRangePicker
              from={timeFrom}
              to={timeTo}
              refreshInterval={listRefreshInterval}
              onChange={handleTimeRangeChange}
              onRefreshIntervalChange={handleRefreshIntervalChange}
            />
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
              <p className="text-gray-500 dark:text-gray-400">No incidents found</p>
              <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">Create a new incident to get started</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="table">
                <thead>
                  <tr>
                    <th>UUID</th>
                    <th>Source</th>
                    <th>Title</th>
                    <th>Status</th>
                    <th>Started</th>
                    <th>Duration</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {incidents.map((incident) => {
                    const statusConfig = getStatusConfig(incident.status);
                    const StatusIcon = statusConfig.icon;
                    const duration = incident.completed_at
                      ? Math.round((new Date(incident.completed_at).getTime() - new Date(incident.started_at).getTime()) / 1000)
                      : null;

                    return (
                      <tr key={incident.id}>
                        <td>
                          <code className="text-xs bg-gray-100 dark:bg-gray-800 px-2 py-1 rounded">
                            {incident.uuid.slice(0, 8)}
                          </code>
                        </td>
                        <td className="text-gray-600 dark:text-gray-300 capitalize">
                          {incident.source}
                        </td>
                        <td className="max-w-xs">
                          <span className="text-gray-700 dark:text-gray-200 text-sm truncate block" title={incident.title || '-'}>
                            {incident.title || <span className="text-gray-400 italic">No title</span>}
                          </span>
                          {incident.alert_count > 0 && (
                            <span className="text-sm text-gray-500">
                              {incident.alert_count} alert{incident.alert_count !== 1 ? 's' : ''}
                            </span>
                          )}
                        </td>
                        <td>
                          <span className={`badge ${statusConfig.class} inline-flex items-center gap-1`}>
                            <StatusIcon className="w-3 h-3" />
                            {statusConfig.label}
                          </span>
                        </td>
                        <td className="text-gray-500 dark:text-gray-400 text-sm">
                          {new Date(incident.started_at).toLocaleString('en-US', {
                            month: 'short',
                            day: '2-digit',
                            hour: '2-digit',
                            minute: '2-digit',
                            hour12: false
                          })}
                        </td>
                        <td className="text-gray-500 dark:text-gray-400 text-sm font-mono">
                          {duration !== null ? `${duration}s` : '-'}
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
                            {(incident.status === 'completed' || incident.status === 'failed') && (
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
                  <span className={`badge ${getStatusConfig(selectedIncident.status).class}`}>
                    {selectedIncident.status}
                  </span>
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
                {(selectedIncident.status === 'completed' || selectedIncident.status === 'failed') && (
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
