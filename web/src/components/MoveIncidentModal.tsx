import { useState, useEffect, useMemo } from 'react';
import { X, Search, Loader2, Sparkles, ArrowRight } from 'lucide-react';
import type { Incident } from '../types';
import { incidentsApi, alertsApi } from '../api/client';

interface MoveIncidentModalProps {
  alertUUID: string;
  alertTitle: string;
  currentIncidentUUID?: string;
  onClose: () => void;
  onMoved: (result: { incidentUUID: string; isNew: boolean }) => void;
}

// MoveIncidentModal lets an operator reassign an alert: either start a fresh
// investigation (unlink) or link it to an existing incident.
export default function MoveIncidentModal({
  alertUUID,
  alertTitle,
  currentIncidentUUID,
  onClose,
  onMoved,
}: MoveIncidentModalProps) {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    incidentsApi.list(undefined, undefined, 1, 50)
      .then(res => {
        if (!cancelled) {
          setIncidents(res.data.filter(inc => inc.uuid !== currentIncidentUUID));
          setLoading(false);
        }
      })
      .catch(err => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Failed to load incidents');
          setLoading(false);
        }
      });
    return () => { cancelled = true; };
  }, [currentIncidentUUID]);

  useEffect(() => {
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !submitting) onClose();
    };
    document.addEventListener('keydown', handleEscape);
    return () => document.removeEventListener('keydown', handleEscape);
  }, [submitting, onClose]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return incidents;
    return incidents.filter(inc =>
      (inc.title || '').toLowerCase().includes(q) || inc.uuid.toLowerCase().includes(q));
  }, [incidents, search]);

  const doMove = async (target?: string) => {
    setSubmitting(true);
    setError('');
    try {
      const res = await alertsApi.move(alertUUID, target);
      onMoved({ incidentUUID: res.incident_uuid, isNew: !target });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to move alert');
      setSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="move-modal-title">
      <div className="fixed inset-0 bg-black/50 transition-opacity" onClick={submitting ? undefined : onClose} />
      <div className="flex min-h-full items-center justify-center p-4">
        <div className="relative w-full max-w-lg bg-surface rounded-xl shadow-xl">
          {/* Header */}
          <div className="flex items-center justify-between px-6 py-4 border-b border-line">
            <div>
              <h2 id="move-modal-title" className="text-base font-semibold text-fg">
                Move alert
              </h2>
              <p className="text-xs text-fg-subtle mt-0.5 truncate max-w-md" title={alertTitle}>
                {alertTitle || 'Untitled alert'}
              </p>
            </div>
            <button
              onClick={onClose}
              disabled={submitting}
              className="text-gray-400 hover:text-fg disabled:opacity-50"
            >
              <X className="w-5 h-5" />
            </button>
          </div>

          <div className="p-6 space-y-4">
            {error && (
              <div className="px-3 py-2 rounded-lg bg-state-fail/10 text-state-fail text-sm">
                {error}
              </div>
            )}

            {/* New investigation option */}
            <button
              onClick={() => doMove(undefined)}
              disabled={submitting}
              className="w-full flex items-center gap-3 px-4 py-3 rounded-lg border border-primary-200 dark:border-primary-800 bg-primary-50 dark:bg-primary-900/20 text-primary-700 dark:text-primary-300 hover:bg-primary-100 dark:hover:bg-primary-900/40 disabled:opacity-50 transition-colors text-left"
            >
              <Sparkles className="w-4 h-4 shrink-0" />
              <span className="flex-1 text-sm font-medium">Unlink into a new investigation</span>
              <ArrowRight className="w-4 h-4 shrink-0" />
            </button>

            <div className="flex items-center gap-3">
              <div className="flex-1 h-px bg-gray-200 dark:bg-gray-700" />
              <span className="text-xs text-gray-400">or link to an existing incident</span>
              <div className="flex-1 h-px bg-gray-200 dark:bg-gray-700" />
            </div>

            {/* Search */}
            <div className="relative">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" />
              <input
                type="text"
                value={search}
                onChange={e => setSearch(e.target.value)}
                placeholder="Search incidents by title or UUID…"
                className="w-full pl-9 pr-3 py-2 rounded-lg border border-line bg-surface text-sm text-fg focus:outline-none focus:ring-2 focus:ring-primary-500"
              />
            </div>

            {/* Incident list */}
            <div className="max-h-64 overflow-y-auto border border-line rounded-lg divide-y divide-gray-100 dark:divide-gray-800">
              {loading ? (
                <div className="flex items-center justify-center py-8 text-gray-400">
                  <Loader2 className="w-5 h-5 animate-spin mr-2" /> Loading incidents…
                </div>
              ) : filtered.length === 0 ? (
                <p className="text-center text-sm text-gray-400 py-8">No matching incidents</p>
              ) : (
                filtered.map(inc => (
                  <button
                    key={inc.uuid}
                    onClick={() => doMove(inc.uuid)}
                    disabled={submitting}
                    className="w-full flex items-center gap-3 px-3 py-2.5 text-left hover:bg-surface-2 disabled:opacity-50 transition-colors"
                  >
                    <div className="flex-1 min-w-0">
                      <div className="text-sm text-fg truncate">
                        {inc.title || <span className="italic text-gray-400">Untitled</span>}
                      </div>
                      <div className="flex items-center gap-2 mt-0.5">
                        <code className="text-[11px] text-gray-400">{inc.uuid.slice(0, 8)}</code>
                        <span className="text-[11px] text-gray-400">{inc.status}</span>
                      </div>
                    </div>
                    <ArrowRight className="w-4 h-4 text-gray-300 dark:text-gray-600 shrink-0" />
                  </button>
                ))
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
