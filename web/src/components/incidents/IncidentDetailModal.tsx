import { useCallback } from 'react';
import { X, XCircle, Timer, Zap } from 'lucide-react';
import type { Incident } from '../../types';
import { incidentsApi } from '../../api/client';
import { formatExecutionTime, formatTokens } from '../../utils/format';
import { useCloseIncident } from '../../hooks/useCloseIncident';
import IncidentDetailView from '../IncidentDetailView';
import CloseIncidentModal from '../CloseIncidentModal';
import { getIncidentStatusConfig, isFinishedStatus } from './incidentStatus';

interface IncidentDetailModalProps {
  incident: Incident;
  autoRefresh: boolean;
  onAutoRefreshChange: (enabled: boolean) => void;
  // Called with a fresh copy of the incident after close/refresh actions.
  onIncidentUpdate: (incident: Incident) => void;
  onClose: () => void;
}

// IncidentDetailModal is the overlay opened from the Incidents table. It owns
// the close-incident flow; polling of running incidents stays with the page
// so the list row is updated alongside the modal.
export default function IncidentDetailModal({
  incident,
  autoRefresh,
  onAutoRefreshChange,
  onIncidentUpdate,
  onClose,
}: IncidentDetailModalProps) {
  const refresh = useCallback(async () => {
    try {
      onIncidentUpdate(await incidentsApi.get(incident.uuid));
    } catch (err) {
      console.error('Failed to refresh incident:', err);
    }
  }, [incident.uuid, onIncidentUpdate]);

  const close = useCloseIncident(incident.uuid, refresh);
  const statusConfig = getIncidentStatusConfig(
    incident.status,
    incident.monitor_until,
    incident.source_kind,
    incident.closed_reason,
  );

  return (
    <>
      <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
        <div className="bg-white dark:bg-gray-800 rounded-xl shadow-2xl max-w-5xl w-full max-h-[90vh] flex flex-col animate-fade-in">
          {/* Modal Header */}
          <div className="flex items-center justify-between p-6 border-b border-gray-200 dark:border-gray-700">
            <div>
              <div className="flex items-center gap-3">
                <h2 className="text-xl font-semibold text-gray-900 dark:text-white">
                  {incident.title || 'Incident Details'}
                </h2>
                <span className={`badge ${statusConfig.class}`}>{statusConfig.label}</span>
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
              <div className="mt-1 flex items-center gap-4 text-sm text-gray-500 dark:text-gray-400">
                <span>
                  UUID: <code className="text-primary-600 dark:text-primary-400">{incident.uuid.slice(0, 8)}</code>
                </span>
                <span className="text-gray-300 dark:text-gray-600">|</span>
                <span>Source: {incident.source}</span>
              </div>
              {close.closeError && (
                <div className="mt-2 px-3 py-2 rounded-lg bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-300 text-sm">
                  {close.closeError}
                </div>
              )}
            </div>
            <button onClick={onClose} className="btn btn-ghost p-2" title="Close">
              <X className="w-5 h-5" />
            </button>
          </div>

          {/* Shared Detail View */}
          <IncidentDetailView incident={incident} autoRefresh={autoRefresh} />

          {/* Modal Footer */}
          <div className="flex items-center justify-between p-6 border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
            <div className="flex items-center gap-4">
              {incident.status === 'running' && (
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={autoRefresh}
                    onChange={(e) => onAutoRefreshChange(e.target.checked)}
                  />
                  <span className="text-sm text-gray-600 dark:text-gray-400">Auto-refresh (2s)</span>
                </label>
              )}
              {isFinishedStatus(incident.status) && (
                <div className="flex items-center gap-4 text-sm text-gray-500 dark:text-gray-400">
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
                </div>
              )}
            </div>
            <button onClick={onClose} className="btn btn-secondary">
              Close
            </button>
          </div>
        </div>
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
    </>
  );
}
