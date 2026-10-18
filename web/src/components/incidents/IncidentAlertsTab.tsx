import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ChevronDown, ChevronRight, RefreshCw, Shuffle } from 'lucide-react';
import type { Alert } from '../../types';
import { alertsApi, incidentsApi } from '../../api/client';
import { getErrorMessage } from '../../utils/errors';
import { formatShortDateTime } from '../../utils/format';
import MoveIncidentModal from '../MoveIncidentModal';

interface IncidentAlertsTabProps {
  incidentUUID: string;
}

// IncidentAlertsTab lists the alerts linked to an incident, with manual
// resolve and move-to-another-incident actions. The parent remounts it per
// incident (key), so state never carries over between incidents.
export default function IncidentAlertsTab({ incidentUUID }: IncidentAlertsTabProps) {
  const [alerts, setAlerts] = useState<Alert[] | null>(null);
  const [error, setError] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [moveTarget, setMoveTarget] = useState<Alert | null>(null);
  const [moveResult, setMoveResult] = useState<{ incidentUUID: string; isNew: boolean } | null>(null);
  const [resolving, setResolving] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setAlerts(await incidentsApi.getAlerts(incidentUUID));
      setError('');
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load alerts'));
    }
  }, [incidentUUID]);

  useEffect(() => {
    let cancelled = false;
    incidentsApi.getAlerts(incidentUUID)
      .then((data) => { if (!cancelled) setAlerts(data); })
      .catch((err: unknown) => { if (!cancelled) setError(getErrorMessage(err, 'Failed to load alerts')); });
    return () => { cancelled = true; };
  }, [incidentUUID]);

  const handleResolve = async (alert: Alert) => {
    setResolving(alert.uuid);
    try {
      await alertsApi.resolve(alert.uuid);
      await load();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to resolve alert'));
    } finally {
      setResolving(null);
    }
  };

  const toggleReasoning = (uuid: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(uuid)) next.delete(uuid);
      else next.add(uuid);
      return next;
    });

  if (alerts === null && !error) {
    return (
      <div className="flex items-center justify-center py-12 text-fg-subtle">
        <RefreshCw className="w-4 h-4 animate-spin mr-2" aria-hidden="true" />
        Loading alerts…
      </div>
    );
  }

  return (
    <div>
      {error && <p className="mb-4 px-3 py-2 rounded-md bg-state-fail/10 text-state-fail text-sm">{error}</p>}
      {moveResult && (
        <p className="mb-4 px-3 py-2 rounded-md bg-state-ok/10 text-sm text-fg">
          {moveResult.isNew ? 'Alert unlinked. New investigation: ' : 'Alert moved to incident '}
          <Link to={`/incidents/${moveResult.incidentUUID}`} className="font-mono text-primary-400 hover:underline">
            {moveResult.incidentUUID.slice(0, 8)}
          </Link>
        </p>
      )}
      {alerts && alerts.length === 0 ? (
        <p className="py-8 text-center text-fg-subtle">No alerts are linked to this incident.</p>
      ) : alerts && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-line text-left text-xs text-fg-subtle">
                <th className="pb-2 pr-4 font-medium">Alert</th>
                <th className="pb-2 pr-4 font-medium">Host</th>
                <th className="pb-2 pr-4 font-medium">State</th>
                <th className="pb-2 pr-4 font-medium">Fired</th>
                <th className="pb-2 font-medium"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {alerts.map((alert) => (
                <tr key={alert.uuid} className="align-top border-b border-line last:border-0">
                  <td className="py-3 pr-4">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium text-fg">{alert.alert_name}</span>
                      {alert.correlated ? (
                        <button
                          type="button"
                          onClick={() => toggleReasoning(alert.uuid)}
                          aria-expanded={expanded.has(alert.uuid)}
                          className="inline-flex items-center gap-1 text-xs text-fg-subtle hover:text-fg"
                        >
                          Correlated
                          {alert.correlation_confidence != null && ` ${Math.round(alert.correlation_confidence * 100)}%`}
                          {expanded.has(alert.uuid) ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
                        </button>
                      ) : (
                        <span className="text-xs text-fg-subtle">Origin</span>
                      )}
                    </div>
                    {expanded.has(alert.uuid) && alert.correlation_reasoning && (
                      <p className="mt-2 max-w-md text-xs text-fg-muted">{alert.correlation_reasoning}</p>
                    )}
                  </td>
                  <td className="py-3 pr-4 font-mono text-xs text-fg-muted">{alert.target_host}</td>
                  <td className="py-3 pr-4 whitespace-nowrap">
                    {alert.status === 'firing' ? (
                      <span className="text-state-fail">Firing</span>
                    ) : (
                      <span className="text-state-ok">
                        Resolved{alert.resolved_at && <span className="text-fg-subtle"> {formatShortDateTime(alert.resolved_at)}</span>}
                      </span>
                    )}
                  </td>
                  <td className="py-3 pr-4 text-xs text-fg-subtle whitespace-nowrap">{formatShortDateTime(alert.fired_at)}</td>
                  <td className="py-3">
                    <div className="flex items-center justify-end gap-1">
                      {alert.status === 'firing' && (
                        <button
                          type="button"
                          onClick={() => void handleResolve(alert)}
                          disabled={resolving === alert.uuid}
                          className="btn btn-ghost px-2 py-1 text-xs"
                          title="Mark this alert resolved by hand"
                        >
                          {resolving === alert.uuid ? 'Resolving…' : 'Resolve'}
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => setMoveTarget(alert)}
                        className="btn btn-ghost px-2 py-1 text-xs inline-flex items-center gap-1"
                        title="Move this alert to another incident, or unlink it into a new one"
                      >
                        <Shuffle size={12} aria-hidden="true" />
                        Move
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {moveTarget && (
        <MoveIncidentModal
          alertUUID={moveTarget.uuid}
          alertTitle={moveTarget.alert_name}
          currentIncidentUUID={incidentUUID}
          onClose={() => setMoveTarget(null)}
          onMoved={(result) => {
            setMoveTarget(null);
            setMoveResult(result);
            void load();
          }}
        />
      )}
    </div>
  );
}
