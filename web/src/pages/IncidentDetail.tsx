import { useEffect, useState, useRef } from 'react';
import { useParams, Link } from 'react-router-dom';
import { ArrowLeft, Activity, Clock, CheckCircle, AlertCircle, Timer, Zap } from 'lucide-react';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import IncidentDetailView from '../components/IncidentDetailView';
import { incidentsApi } from '../api/client';
import type { Incident } from '../types';

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

const formatTokens = (tokens: number): string => {
  if (!tokens || tokens <= 0) return '-';
  return tokens.toLocaleString();
};

export default function IncidentDetail() {
  const { uuid } = useParams<{ uuid: string }>();
  const [incident, setIncident] = useState<Incident | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [autoRefresh, setAutoRefresh] = useState(true);
  const refreshIntervalRef = useRef<number | null>(null);

  useEffect(() => {
    if (!uuid) return;
    const load = async () => {
      try {
        setLoading(true);
        const data = await incidentsApi.get(uuid);
        setIncident(data);
        setError('');
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load incident');
      } finally {
        setLoading(false);
      }
    };
    load();
  }, [uuid]);

  // Auto-refresh while running
  useEffect(() => {
    if (!incident || incident.status !== 'running' || !autoRefresh || !uuid) return;

    refreshIntervalRef.current = window.setInterval(async () => {
      try {
        const updated = await incidentsApi.get(uuid);
        setIncident(updated);
      } catch (err) {
        console.error('Failed to refresh incident:', err);
      }
    }, 2000);

    return () => {
      if (refreshIntervalRef.current) {
        clearInterval(refreshIntervalRef.current);
        refreshIntervalRef.current = null;
      }
    };
  }, [incident?.status, autoRefresh, uuid]);

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
      <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
        <div className="p-6 border-b border-gray-200 dark:border-gray-700">
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
                <span>{new Date(incident.started_at).toLocaleString()}</span>
              </div>
            </div>
            <span className={`badge ${statusConfig.class} inline-flex items-center gap-1`}>
              <StatusIcon className="w-3 h-3" />
              {statusConfig.label}
            </span>
          </div>

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
            {(incident.status === 'completed' || incident.status === 'failed') && (
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
            {incident.alert_count > 0 && (
              <span>{incident.alert_count} alert{incident.alert_count !== 1 ? 's' : ''}</span>
            )}
          </div>
        </div>

        {/* Detail view */}
        <IncidentDetailView incident={incident} autoRefresh={autoRefresh} />
      </div>
    </div>
  );
}
