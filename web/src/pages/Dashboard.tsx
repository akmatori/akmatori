import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Activity, CheckCircle, AlertCircle, Clock, ArrowRight, AlertTriangle, CheckCircle2 } from 'lucide-react';
import QuickIncidentInput from '../components/QuickIncidentInput';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage } from '../components/ErrorMessage';
import { incidentsApi, llmSettingsApi, alertSourcesApi } from '../api/client';
import type { Incident } from '../types';

export default function Dashboard() {
  const navigate = useNavigate();
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  // System health state
  const [llmConfigured, setLlmConfigured] = useState<boolean | null>(null);
  const [alertSourcesCount, setAlertSourcesCount] = useState<number>(0);

  useEffect(() => {
    loadDashboardData();
  }, []);

  const loadDashboardData = async () => {
    try {
      setLoading(true);
      setError('');

      const [incidentsData, llmSettings, alertSources] = await Promise.all([
        incidentsApi.list(),
        llmSettingsApi.get().catch(() => null),
        alertSourcesApi.list().catch(() => []),
      ]);

      setIncidents(incidentsData?.slice(0, 5) ?? []);
      setLlmConfigured(llmSettings?.is_configured ?? false);
      setAlertSourcesCount(alertSources?.length ?? 0);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load dashboard');
    } finally {
      setLoading(false);
    }
  };

  const handleIncidentCreated = () => {
    setSuccess(`Investigation started`);
    setTimeout(() => {
      setSuccess('');
      navigate('/incidents');
    }, 1500);
    loadDashboardData();
  };

  const handleIncidentError = (errorMsg: string) => {
    setError(errorMsg);
    setTimeout(() => setError(''), 5000);
  };

  const getStatusConfig = (status: string) => {
    switch (status) {
      case 'completed':
        return { class: 'text-green-600 dark:text-green-400', icon: CheckCircle, label: 'Completed' };
      case 'running':
        return { class: 'text-primary-600 dark:text-primary-400', icon: Activity, label: 'Running' };
      case 'failed':
        return { class: 'text-red-600 dark:text-red-400', icon: AlertCircle, label: 'Failed' };
      default:
        return { class: 'text-gray-500 dark:text-gray-400', icon: Clock, label: 'Pending' };
    }
  };

  const runningCount = incidents.filter(i => i.status === 'running').length;
  const completedCount = incidents.filter(i => i.status === 'completed').length;
  const failedCount = incidents.filter(i => i.status === 'failed').length;

  if (loading) return <LoadingSpinner />;

  return (
    <div className="max-w-4xl mx-auto">
      {/* Hero Section */}
      <div className="py-12 text-center">
        <h1 className="text-3xl font-bold text-gray-900 dark:text-white mb-2">
          Akmatori
        </h1>
        <p className="text-gray-500 dark:text-gray-400 mb-8">
          AI-powered incident investigation and remediation
        </p>

        {/* Hero Input */}
        <div className="max-w-2xl mx-auto">
          <QuickIncidentInput
            onSuccess={handleIncidentCreated}
            onError={handleIncidentError}
          />
        </div>
      </div>

      {/* Messages */}
      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message={success} />}

      {/* Two Column Layout */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mt-8">
        {/* Live Activity */}
        <div className="card">
          <div className="flex items-center justify-between mb-4">
            <h2 className="font-semibold text-gray-900 dark:text-white">Live Activity</h2>
            <div className="flex items-center gap-3 text-sm">
              {runningCount > 0 && (
                <span className="flex items-center gap-1 text-primary-600 dark:text-primary-400">
                  <span className="w-2 h-2 rounded-full bg-primary-500 animate-pulse" />
                  {runningCount} running
                </span>
              )}
            </div>
          </div>

          {incidents.length === 0 ? (
            <div className="py-8 text-center text-gray-400 dark:text-gray-500">
              <Activity className="w-8 h-8 mx-auto mb-2 opacity-50" />
              <p className="text-sm">No recent incidents</p>
            </div>
          ) : (
            <div className="space-y-2">
              {incidents.slice(0, 5).map((incident) => {
                const statusConfig = getStatusConfig(incident.status);
                const StatusIcon = statusConfig.icon;

                return (
                  <Link
                    key={incident.id}
                    to="/incidents"
                    className="flex items-center gap-3 p-3 -mx-3 rounded-lg hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors"
                  >
                    <StatusIcon className={`w-4 h-4 ${statusConfig.class} ${incident.status === 'running' ? 'animate-pulse' : ''}`} />
                    <span className="flex-1 text-sm text-gray-700 dark:text-gray-300 truncate">
                      {incident.title || `Incident ${incident.uuid.slice(0, 8)}`}
                    </span>
                    <span className="text-xs text-gray-400 dark:text-gray-500">
                      {new Date(incident.started_at).toLocaleTimeString('en-US', {
                        hour: '2-digit',
                        minute: '2-digit',
                        hour12: false
                      })}
                    </span>
                  </Link>
                );
              })}
            </div>
          )}

          <Link
            to="/incidents"
            className="flex items-center justify-center gap-1 mt-4 pt-4 border-t border-gray-100 dark:border-gray-700 text-sm text-primary-600 dark:text-primary-400 hover:underline"
          >
            View all incidents
            <ArrowRight className="w-3 h-3" />
          </Link>
        </div>

        {/* System Health */}
        <div className="card">
          <h2 className="font-semibold text-gray-900 dark:text-white mb-4">System Health</h2>

          <div className="space-y-3">
            {/* LLM Status */}
            <div className="flex items-center justify-between py-2">
              <span className="text-sm text-gray-600 dark:text-gray-400">LLM Provider</span>
              {llmConfigured === null ? (
                <span className="text-sm text-gray-400">Checking...</span>
              ) : llmConfigured ? (
                <span className="flex items-center gap-1.5 text-sm text-green-600 dark:text-green-400">
                  <CheckCircle2 className="w-4 h-4" />
                  Connected
                </span>
              ) : (
                <Link to="/settings" className="flex items-center gap-1.5 text-sm text-amber-600 dark:text-amber-400 hover:underline">
                  <AlertTriangle className="w-4 h-4" />
                  Not configured
                </Link>
              )}
            </div>

            {/* Alert Sources */}
            <div className="flex items-center justify-between py-2">
              <span className="text-sm text-gray-600 dark:text-gray-400">Alert Sources</span>
              {alertSourcesCount > 0 ? (
                <span className="flex items-center gap-1.5 text-sm text-green-600 dark:text-green-400">
                  <CheckCircle2 className="w-4 h-4" />
                  {alertSourcesCount} active
                </span>
              ) : (
                <Link to="/settings" className="flex items-center gap-1.5 text-sm text-gray-400 dark:text-gray-500 hover:underline">
                  <AlertTriangle className="w-4 h-4" />
                  None configured
                </Link>
              )}
            </div>

            {/* Incident Stats */}
            <div className="pt-3 mt-3 border-t border-gray-100 dark:border-gray-700">
              <div className="grid grid-cols-3 gap-4 text-center">
                <div>
                  <p className="text-2xl font-semibold text-primary-600 dark:text-primary-400">{runningCount}</p>
                  <p className="text-xs text-gray-500 dark:text-gray-400">Running</p>
                </div>
                <div>
                  <p className="text-2xl font-semibold text-green-600 dark:text-green-400">{completedCount}</p>
                  <p className="text-xs text-gray-500 dark:text-gray-400">Completed</p>
                </div>
                <div>
                  <p className="text-2xl font-semibold text-red-600 dark:text-red-400">{failedCount}</p>
                  <p className="text-xs text-gray-500 dark:text-gray-400">Failed</p>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Setup hint for new users */}
      {!llmConfigured && (
        <div className="mt-6 p-4 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded-xl">
          <div className="flex items-start gap-3">
            <AlertTriangle className="w-5 h-5 text-amber-600 dark:text-amber-400 flex-shrink-0 mt-0.5" />
            <div>
              <p className="text-sm font-medium text-amber-800 dark:text-amber-200">
                Setup Required
              </p>
              <p className="text-sm text-amber-700 dark:text-amber-300 mt-1">
                Configure your LLM provider in Settings to start using Akmatori.
              </p>
              <Link
                to="/settings"
                className="inline-flex items-center gap-1 mt-2 text-sm font-medium text-amber-700 dark:text-amber-300 hover:underline"
              >
                Go to Settings
                <ArrowRight className="w-3 h-3" />
              </Link>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
