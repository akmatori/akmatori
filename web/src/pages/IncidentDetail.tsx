import { useEffect, useState, useCallback } from 'react';
import { useParams, Link } from 'react-router-dom';
import { ArrowLeft, Activity, Clock, CheckCircle, AlertCircle, Timer, Zap, XCircle, GitMerge } from 'lucide-react';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import IncidentDetailView from '../components/IncidentDetailView';
import CloseIncidentModal from '../components/CloseIncidentModal';
import { isFinishedStatus } from '../components/incidents/incidentStatus';
import { incidentsApi } from '../api/client';
import type { Incident } from '../types';
import { useCloseIncident } from '../hooks/useCloseIncident';
import { useInterval } from '../hooks/useInterval';
import { formatAbsolute, formatExecutionTime, formatTokens } from '../utils/format';
import { getErrorMessage } from '../utils/errors';

// Poll a running incident this often while auto-refresh is on.
const RUNNING_POLL_INTERVAL = 2000;

// The standalone page uses the raw status vocabulary (Running, Diagnosed,
// Monitor) rather than the list's collapsed "Ongoing" label.
const getStatusConfig = (status: string) => {
  switch (status) {
    case 'completed':
      return { class: 'badge-success', icon: CheckCircle, label: 'Completed' };
    case 'monitor':
      return { class: 'badge-success', icon: CheckCircle, label: 'Monitor' };
    case 'running':
      return { class: 'badge-primary', icon: Activity, label: 'Running' };
    case 'diagnosed':
      return { class: 'badge-purple', icon: CheckCircle, label: 'Diagnosed' };
    case 'failed':
      return { class: 'badge-error', icon: AlertCircle, label: 'Failed' };
    case 'closed':
      return { class: 'badge-default', icon: XCircle, label: 'Closed' };
    case 'merged':
      return { class: 'badge-default', icon: GitMerge, label: 'Merged' };
    default:
      return { class: 'badge-default', icon: Clock, label: 'Pending' };
  }
};

export default function IncidentDetail() {
  const { uuid } = useParams<{ uuid: string }>();
  const [incident, setIncident] = useState<Incident | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [autoRefresh, setAutoRefresh] = useState(true);

  useEffect(() => {
    if (!uuid) return;
    const load = async () => {
      try {
        setLoading(true);
        const data = await incidentsApi.get(uuid);
        setIncident(data);
        setError('');
      } catch (err) {
        setError(getErrorMessage(err, 'Failed to load incident'));
      } finally {
        setLoading(false);
      }
    };
    load();
  }, [uuid]);

  const refreshIncident = useCallback(async () => {
    if (!uuid) return;
    try {
      setIncident(await incidentsApi.get(uuid));
    } catch (err) {
      console.error('Failed to refresh incident:', err);
    }
  }, [uuid]);

  // Auto-refresh while running
  const pollingActive = !!uuid && incident?.status === 'running' && autoRefresh;
  useInterval(() => { void refreshIncident(); }, pollingActive ? RUNNING_POLL_INTERVAL : null);

  const close = useCloseIncident(uuid, refreshIncident);

  if (loading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <LoadingSpinner />
      </div>
    );
  }

  if (error || !incident) {
    return (
      <div className="max-w-4xl mx-auto">
        <Link to="/incidents" className="inline-flex items-center gap-2 text-sm text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 mb-6">
          <ArrowLeft className="w-4 h-4" />
          Back to Incidents
        </Link>
        <ErrorMessage message={error || 'Incident not found'} />
      </div>
    );
  }

  const statusConfig = getStatusConfig(incident.status);
  const StatusIcon = statusConfig.icon;

  return (
    <div className="animate-fade-in max-w-5xl mx-auto">
      {/* Back link */}
      <Link to="/incidents" className="inline-flex items-center gap-2 text-sm text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 mb-6">
        <ArrowLeft className="w-4 h-4" />
        Back to Incidents
      </Link>

      {/* Header */}
      <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden flex flex-col max-h-[calc(100vh-220px)]">
        <div className="p-6 border-b border-gray-200 dark:border-gray-700 shrink-0">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-xl font-semibold text-gray-900 dark:text-white">
                {incident.title || 'Untitled Incident'}
              </h1>
              <div className="mt-2 flex items-center gap-4 text-sm text-gray-500 dark:text-gray-400">
                <span>
                  UUID: <code className="text-primary-600 dark:text-primary-400">{incident.uuid.slice(0, 8)}</code>
                </span>
                <span className="text-gray-300 dark:text-gray-600">|</span>
                <span>Source: {incident.source}</span>
                <span className="text-gray-300 dark:text-gray-600">|</span>
                <span>{formatAbsolute(incident.started_at)}</span>
              </div>
            </div>
            <div className="flex items-center gap-3">
              <span className={`badge ${statusConfig.class} inline-flex items-center gap-1`}>
                <StatusIcon className="w-3 h-3" />
                {statusConfig.label}
              </span>
              {incident.status !== 'closed' && (
                <button
                  onClick={close.requestClose}
                  disabled={close.closing}
                  className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg text-xs font-medium text-gray-600 dark:text-gray-300 border border-gray-300 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700 disabled:opacity-50 transition-colors"
                >
                  <XCircle className="w-3.5 h-3.5" />
                  {close.closing ? 'Closing…' : 'Close Incident'}
                </button>
              )}
            </div>
          </div>

          {close.closeError && (
            <div className="mt-3 px-3 py-2 rounded-lg bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-300 text-sm">
              {close.closeError}
            </div>
          )}

          {/* Metrics bar */}
          <div className="mt-4 flex items-center gap-6 text-sm text-gray-500 dark:text-gray-400">
            {incident.status === 'running' && (
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={autoRefresh}
                  onChange={(e) => setAutoRefresh(e.target.checked)}
                />
                <span>Auto-refresh (2s)</span>
              </label>
            )}
            {isFinishedStatus(incident.status) && (
              <>
                {incident.execution_time_ms > 0 && (
                  <span className="flex items-center gap-1.5">
                    <Timer className="w-4 h-4" />
                    {formatExecutionTime(incident.execution_time_ms)}
                  </span>
                )}
                {incident.tokens_used > 0 && (
                  <span className="flex items-center gap-1.5">
                    <Zap className="w-4 h-4" />
                    {formatTokens(incident.tokens_used)} tokens
                  </span>
                )}
              </>
            )}
          </div>
        </div>

        {/* Detail view */}
        <IncidentDetailView incident={incident} autoRefresh={autoRefresh} />
      </div>

      {close.confirmClose && (
        <CloseIncidentModal
          incidentUUID={incident.uuid}
          firingAlertCount={close.confirmClose.firingAlertCount}
          inProgress={close.confirmClose.inProgress}
          onClose={close.dismissConfirm}
          onClosed={close.handleConfirmed}
        />
      )}
    </div>
  );
}
