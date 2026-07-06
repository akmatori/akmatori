import { useState, useEffect } from 'react';
import { X, AlertTriangle } from 'lucide-react';
import { incidentsApi } from '../api/client';

interface CloseIncidentModalProps {
  incidentUUID: string;
  firingAlertCount: number;
  inProgress: boolean;
  onClose: () => void;
  onClosed: () => void;
}

// CloseIncidentModal confirms cascading alert resolution, and/or force-
// closing a still-pending/running investigation, before manually closing an
// incident.
export default function CloseIncidentModal({
  incidentUUID,
  firingAlertCount,
  inProgress,
  onClose,
  onClosed,
}: CloseIncidentModalProps) {
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !submitting) onClose();
    };
    document.addEventListener('keydown', handleEscape);
    return () => document.removeEventListener('keydown', handleEscape);
  }, [submitting, onClose]);

  const confirmClose = async () => {
    setSubmitting(true);
    setError('');
    try {
      await incidentsApi.close(incidentUUID, true);
      onClosed();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to close incident');
      setSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="close-incident-modal-title">
      <div className="fixed inset-0 bg-black/50 transition-opacity" onClick={submitting ? undefined : onClose} />
      <div className="flex min-h-full items-center justify-center p-4">
        <div className="relative w-full max-w-md bg-white dark:bg-gray-800 rounded-xl shadow-xl">
          <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200 dark:border-gray-700">
            <h2 id="close-incident-modal-title" className="text-base font-semibold text-gray-900 dark:text-gray-100">
              Close incident?
            </h2>
            <button
              onClick={onClose}
              disabled={submitting}
              className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 disabled:opacity-50"
            >
              <X className="w-5 h-5" />
            </button>
          </div>

          <div className="p-6 space-y-4">
            {inProgress && (
              <div className="flex items-start gap-3 px-3 py-2.5 rounded-lg bg-amber-50 dark:bg-amber-900/20 text-amber-700 dark:text-amber-300 text-sm">
                <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
                <span>
                  This incident's investigation is still pending/running. Closing it now will force-stop
                  tracking it as in-progress — only do this if it's actually stuck (e.g. the agent worker
                  never picked it up).
                </span>
              </div>
            )}
            {firingAlertCount > 0 && (
              <div className="flex items-start gap-3 px-3 py-2.5 rounded-lg bg-amber-50 dark:bg-amber-900/20 text-amber-700 dark:text-amber-300 text-sm">
                <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
                <span>
                  {firingAlertCount} alert{firingAlertCount === 1 ? ' is' : 's are'} still firing on this incident.
                  Closing it will mark {firingAlertCount === 1 ? 'that alert' : 'all of them'} as resolved.
                </span>
              </div>
            )}

            {error && (
              <div className="px-3 py-2 rounded-lg bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-300 text-sm">
                {error}
              </div>
            )}

            <div className="flex items-center justify-end gap-3">
              <button
                onClick={onClose}
                disabled={submitting}
                className="px-4 py-2 rounded-lg text-sm font-medium text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 disabled:opacity-50 transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={confirmClose}
                disabled={submitting}
                className="px-4 py-2 rounded-lg text-sm font-medium text-white bg-red-600 hover:bg-red-700 disabled:opacity-50 transition-colors"
              >
                {submitting ? 'Closing…' : 'Resolve alerts & close'}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
