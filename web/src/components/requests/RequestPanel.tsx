import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowLeft } from 'lucide-react';
import type { RequestDetail } from '../../types';
import { requestsApi } from '../../api/client';
import { useInterval } from '../../hooks/useInterval';
import { getErrorMessage } from '../../utils/errors';
import { formatRelativeAgo, formatShortDateTime } from '../../utils/format';
import LoadingSpinner from '../LoadingSpinner';
import SourceKindChip from '../shared/SourceKindChip';
import IncidentSummaryTab from '../incidents/IncidentSummaryTab';
import IncidentTimelineTab from '../incidents/IncidentTimelineTab';
import { TONE_CLASSES } from '../incidents/incidentStatus';
import RunsTab from '../runs/RunsTab';
import { runStatusLabel, runStatusTone } from '../runs/runHelpers';

// Poll in-flight request work this often.
const LIVE_POLL_INTERVAL = 2000;

type TabId = 'summary' | 'timeline' | 'runs';

// RequestPanel shows request work: agent work that is not an incident (a
// Slack or API request, a cron run, a proposal chat). Same Summary and
// Timeline tabs as an incident, plus the list of its runs.
export default function RequestPanel({ uuid }: { uuid: string }) {
  const [request, setRequest] = useState<RequestDetail | null>(null);
  const [error, setError] = useState('');
  // null = the default tab (Summary once there is one).
  const [tab, setTab] = useState<TabId | null>(null);

  const refresh = useCallback(async () => {
    try {
      setRequest(await requestsApi.get(uuid));
      setError('');
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load the request'));
    }
  }, [uuid]);

  useEffect(() => {
    let cancelled = false;
    requestsApi.get(uuid)
      .then((data) => { if (!cancelled) setRequest(data); })
      .catch((err: unknown) => { if (!cancelled) setError(getErrorMessage(err, 'Failed to load the request')); });
    return () => { cancelled = true; };
  }, [uuid]);

  const live = request?.status === 'running';
  useInterval(() => { void refresh(); }, live ? LIVE_POLL_INTERVAL : null);

  const backLink = (
    <Link to="/events?kind=request" className="inline-flex items-center gap-1.5 mb-4 text-sm text-fg-muted hover:text-fg">
      <ArrowLeft size={14} aria-hidden="true" />
      All requests
    </Link>
  );

  if (!request) {
    return (
      <div className="p-5">
        {backLink}
        {error ? <p className="text-state-fail">{error}</p> : <LoadingSpinner />}
      </div>
    );
  }

  const tone = TONE_CLASSES[runStatusTone(request.status)];
  const activeTab: TabId = tab ?? (request.response ? 'summary' : 'timeline');
  const tabs: { id: TabId; label: string }[] = [
    { id: 'summary', label: 'Summary' },
    { id: 'timeline', label: 'Timeline' },
    { id: 'runs', label: request.runs.length > 1 ? `Runs ${request.runs.length}` : 'Runs' },
  ];

  return (
    <section aria-labelledby="request-title" className="flex flex-col min-h-0 h-full">
      <div className="px-5 pt-4 border-b border-line">
        {backLink}
        <h2
          id="request-title"
          title={request.title}
          className="text-lg font-semibold text-fg leading-snug break-words line-clamp-3"
        >
          {request.title}
        </h2>
        <p className="flex flex-wrap items-center gap-x-4 gap-y-1 mt-2 text-sm text-fg-muted">
          <span className={`inline-flex items-center gap-1.5 ${tone.text}`}>
            <span className={`w-2 h-2 rounded-full ${tone.dot} ${live ? 'animate-pulse' : ''}`} aria-hidden="true" />
            {runStatusLabel(request.status)}
          </span>
          <SourceKindChip kind={request.source_kind} />
          <span>Request, not an incident</span>
          <span title={formatShortDateTime(request.started_at)}>Started {formatRelativeAgo(request.started_at)}</span>
          <code className="font-mono text-xs text-fg-subtle">{request.uuid.slice(0, 8)}</code>
        </p>
        <div role="tablist" aria-label="Request detail" className="flex gap-1 mt-3 -mb-px">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              id={`request-tab-${t.id}`}
              aria-selected={activeTab === t.id}
              aria-controls="request-tabpanel"
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
        id="request-tabpanel"
        role="tabpanel"
        aria-labelledby={`request-tab-${activeTab}`}
        className="flex-1 min-h-0 overflow-y-auto px-5 py-5"
      >
        {activeTab === 'summary' && <IncidentSummaryTab incident={request} feedback={false} />}
        {activeTab === 'timeline' && <IncidentTimelineTab incident={request} live={live} />}
        {activeTab === 'runs' && <RunsTab runs={request.runs} />}
      </div>
    </section>
  );
}
