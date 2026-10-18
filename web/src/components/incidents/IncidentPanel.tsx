import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowLeft, XCircle } from 'lucide-react';
import type { Incident } from '../../types';
import { incidentsApi } from '../../api/client';
import { useCloseIncident } from '../../hooks/useCloseIncident';
import { useInterval } from '../../hooks/useInterval';
import { getErrorMessage } from '../../utils/errors';
import { formatRelativeAgo, formatShortDateTime } from '../../utils/format';
import LoadingSpinner from '../LoadingSpinner';
import CloseIncidentModal from '../CloseIncidentModal';
import SourceKindChip from '../shared/SourceKindChip';
import IncidentSummaryTab from './IncidentSummaryTab';
import IncidentTimelineTab from './IncidentTimelineTab';
import IncidentAlertsTab from './IncidentAlertsTab';
import { getIncidentState, isStalled, TONE_CLASSES } from './incidentStatus';

// Poll an in-flight incident this often.
const LIVE_POLL_INTERVAL = 2000;

type TabId = 'summary' | 'timeline' | 'alerts';

interface IncidentPanelProps {
  uuid: string;
  // Called with each fresh copy so the list row can follow along.
  onUpdate?: (incident: Incident) => void;
}

// IncidentPanel is the incident detail: header, state line and the
// Summary / Timeline / Alerts tabs. The Incidents page mounts it with
// key={uuid}, so every incident starts from clean state.
export default function IncidentPanel({ uuid, onUpdate }: IncidentPanelProps) {
  const [incident, setIncident] = useState<Incident | null>(null);
  const [error, setError] = useState('');
  // null = the default tab for this incident (Summary once there is one).
  const [tab, setTab] = useState<TabId | null>(null);

  const refresh = useCallback(async () => {
    try {
      const fresh = await incidentsApi.get(uuid);
      setIncident(fresh);
      onUpdate?.(fresh);
      setError('');
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load incident'));
    }
  }, [uuid, onUpdate]);

  useEffect(() => {
    let cancelled = false;
    incidentsApi.get(uuid)
      .then((data) => { if (!cancelled) setIncident(data); })
      .catch((err: unknown) => { if (!cancelled) setError(getErrorMessage(err, 'Failed to load incident')); });
    return () => { cancelled = true; };
  }, [uuid]);

  const live = !!incident && (incident.status === 'running' || incident.status === 'pending') && !isStalled(incident);
  useInterval(() => { void refresh(); }, live ? LIVE_POLL_INTERVAL : null);

  const close = useCloseIncident(uuid, refresh);

  const backLink = (
    <Link to="/incidents" className="lg:hidden inline-flex items-center gap-1.5 mb-4 text-sm text-fg-muted hover:text-fg">
      <ArrowLeft size={14} aria-hidden="true" />
      All incidents
    </Link>
  );

  if (!incident) {
    return (
      <div className="p-5">
        {backLink}
        {error ? <p className="text-state-fail">{error}</p> : <LoadingSpinner />}
      </div>
    );
  }

  const state = getIncidentState(incident);
  const tone = TONE_CLASSES[state.tone];
  const hasAlerts = incident.source_kind === 'alert' || (incident.alert_count ?? 0) > 0;
  const activeTab: TabId = tab ?? (incident.response ? 'summary' : 'timeline');
  const tabs: { id: TabId; label: string }[] = [
    { id: 'summary', label: 'Summary' },
    { id: 'timeline', label: 'Timeline' },
    ...(hasAlerts ? [{ id: 'alerts' as const, label: incident.alert_count ? `Alerts ${incident.alert_count}` : 'Alerts' }] : []),
  ];
  const canClose = incident.status !== 'closed' && incident.status !== 'merged';

  return (
    <section aria-labelledby="incident-title" className="flex flex-col min-h-0 h-full">
      <div className="px-5 pt-4 border-b border-line">
        {backLink}
        <div className="flex flex-wrap items-start gap-3">
          <h2
            id="incident-title"
            title={incident.title || undefined}
            className="flex-[1_1_320px] text-lg font-semibold text-fg leading-snug break-words line-clamp-3"
          >
            {incident.title || `Incident ${incident.uuid.slice(0, 8)}`}
          </h2>
          {canClose && (
            <button
              type="button"
              onClick={close.requestClose}
              disabled={close.closing}
              className="btn btn-secondary h-8 px-3 text-xs inline-flex items-center gap-1.5"
            >
              <XCircle size={14} aria-hidden="true" />
              {close.closing ? 'Closing…' : 'Close incident'}
            </button>
          )}
        </div>
        <p className="flex flex-wrap items-center gap-x-4 gap-y-1 mt-2 text-sm text-fg-muted">
          <span className={`inline-flex items-center gap-1.5 ${tone.text}`}>
            <span className={`w-2 h-2 rounded-full ${tone.dot} ${live ? 'animate-pulse' : ''}`} aria-hidden="true" />
            {state.label}
          </span>
          <SourceKindChip kind={incident.source_kind} />
          {incident.source && <span>{incident.source}</span>}
          <span title={formatShortDateTime(incident.started_at)}>Started {formatRelativeAgo(incident.started_at)}</span>
          <code className="font-mono text-xs text-fg-subtle">{incident.uuid.slice(0, 8)}</code>
        </p>
        {close.closeError && (
          <p className="mt-2 px-3 py-2 rounded-md bg-state-fail/10 text-sm text-state-fail">{close.closeError}</p>
        )}
        <div role="tablist" aria-label="Incident detail" className="flex gap-1 mt-3 -mb-px">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              id={`incident-tab-${t.id}`}
              aria-selected={activeTab === t.id}
              aria-controls="incident-tabpanel"
              onClick={() => setTab(t.id)}
              className={`h-9 px-3 text-sm border-b-2 transition-colors ${
                activeTab === t.id ? 'border-primary-500 text-fg' : 'border-transparent text-fg-muted hover:text-fg'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>

      <div
        id="incident-tabpanel"
        role="tabpanel"
        aria-labelledby={`incident-tab-${activeTab}`}
        className="flex-1 min-h-0 overflow-y-auto px-5 py-5"
      >
        {activeTab === 'summary' && <IncidentSummaryTab incident={incident} />}
        {activeTab === 'timeline' && <IncidentTimelineTab incident={incident} live={live} />}
        {activeTab === 'alerts' && <IncidentAlertsTab incidentUUID={incident.uuid} />}
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
    </section>
  );
}
