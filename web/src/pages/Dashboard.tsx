import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { AlertTriangle } from 'lucide-react';
import InvestigateBar from '../components/overview/InvestigateBar';
import { OverviewColumn, OverviewRow } from '../components/overview/OverviewColumn';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { useOverviewData } from '../hooks/useOverviewData';
import { useSystemStatus } from '../hooks/useSystemStatus';
import { formatCountdown, formatRelativeAgo, formatRelativeShort } from '../utils/format';
import type { Incident } from '../types';
import { taskResultPath } from '../components/runs/runHelpers';

const NEEDS_ROW_LIMIT = 8;

function incidentTitle(incident: Incident): string {
  return incident.title || `Incident ${incident.uuid.slice(0, 8)}`;
}

// attentionReason says why an incident sits in "Needs you".
function attentionReason(incident: Incident): string {
  switch (incident.status) {
    case 'diagnosed':
      return 'Diagnosed, action needed';
    case 'failed':
      return 'Investigation failed';
    case 'running':
      return `No progress for ${formatRelativeShort(incident.updated_at)}`;
    default:
      return 'Agent finished, alert still firing';
  }
}

interface HealthIssue {
  message: string;
  to?: string;
  action?: string;
}

export default function Dashboard() {
  const navigate = useNavigate();
  const { data, error } = useOverviewData();
  const { apiAvailable, llm, worker } = useSystemStatus();
  const [startError, setStartError] = useState('');

  if (!data && !error) return <LoadingSpinner />;

  // The health strip shows only when something is degraded.
  const issues: HealthIssue[] = [];
  if (!apiAvailable) issues.push({ message: 'The API does not respond. Data on this page can be out of date.' });
  if (apiAvailable && worker === 'offline') {
    issues.push({ message: 'The agent worker is not connected. New incidents wait until it reconnects.' });
  } else if (apiAvailable && worker === 'unresponsive') {
    issues.push({ message: 'The agent worker is connected but has not answered for over 90 seconds. Investigations can stall.' });
  }
  if (llm && !llm.configured) {
    issues.push({ message: 'No LLM is configured, so the agent cannot investigate.', to: '/settings/ai', action: 'Configure an LLM' });
  }

  // Proposals collapse into one row: an install can hold hundreds.
  const needsRows = data ? Math.min(data.needsAttention.length, NEEDS_ROW_LIMIT) + (data.day.proposals > 0 ? 1 : 0) : 0;

  return (
    <div className="flex flex-col gap-6">
      <InvestigateBar
        onStarted={(res) => {
          const path = taskResultPath(res);
          if (path) navigate(path);
          else setStartError(res.message);
        }}
        onError={setStartError}
      />

      {startError && <ErrorMessage message={startError} />}
      {error && <ErrorMessage message={error} />}

      {issues.map((issue) => (
        <div
          key={issue.message}
          role="status"
          className="flex flex-wrap items-center gap-x-4 gap-y-2 px-3.5 py-2.5 rounded-lg border border-state-need/40 bg-state-need/10 text-sm text-fg"
        >
          <AlertTriangle size={16} className="text-state-need flex-shrink-0" aria-hidden="true" />
          <span className="flex-[1_1_320px]">{issue.message}</span>
          {issue.to && (
            <Link to={issue.to} className="font-medium text-state-need hover:underline">{issue.action}</Link>
          )}
        </div>
      ))}

      {data && (
        <>
          <div className="grid items-start gap-4 grid-cols-[repeat(auto-fit,minmax(300px,1fr))]">
            <OverviewColumn id="col-need" title="Needs you" tone="need" count={data.needsAttention.length} rows={needsRows} empty="Nothing waits for you.">
              {data.day.proposals > 0 && (
                <OverviewRow to="/proposals">
                  <span className="text-xs text-state-need">Self-improvement</span>
                  <span className="font-medium">
                    {data.day.proposals} {data.day.proposals === 1 ? 'proposal waits' : 'proposals wait'} for review
                  </span>
                  {data.latestProposal && (
                    <span className="text-xs text-fg-subtle truncate">Latest: {data.latestProposal.title}</span>
                  )}
                </OverviewRow>
              )}
              {data.needsAttention.slice(0, NEEDS_ROW_LIMIT).map((i) => (
                <OverviewRow key={i.uuid} to={`/incidents/${i.uuid}`}>
                  <span className={`text-xs ${i.status === 'failed' || i.status === 'running' ? 'text-state-fail' : 'text-state-need'}`}>
                    {attentionReason(i)}
                  </span>
                  <span className="font-medium line-clamp-2">{incidentTitle(i)}</span>
                  <span className="flex gap-3 text-xs text-fg-subtle">
                    <span>{i.source}</span>
                    <span>{formatRelativeAgo(i.started_at)}</span>
                  </span>
                </OverviewRow>
              ))}
              {data.needsAttention.length > NEEDS_ROW_LIMIT && (
                <li className="border-t border-line">
                  <Link to="/incidents" className="block px-4 py-2.5 text-sm text-primary-400 hover:bg-surface-2">
                    {data.needsAttention.length - NEEDS_ROW_LIMIT} more in Incidents
                  </Link>
                </li>
              )}
            </OverviewColumn>

            <OverviewColumn id="col-work" title="Agent working" tone="work" count={data.working.length} rows={data.working.length} empty="The agent is idle.">
              {data.working.map((i) => (
                <OverviewRow key={i.uuid} to={`/incidents/${i.uuid}`}>
                  <span className="font-medium line-clamp-2">{incidentTitle(i)}</span>
                  <span className="flex items-center gap-2 text-xs text-state-work">
                    <span className="w-1.5 h-1.5 rounded-full bg-state-work animate-pulse" aria-hidden="true" />
                    {i.status === 'pending' ? 'Queued' : 'Investigating'}
                  </span>
                  <span className="flex gap-3 text-xs text-fg-subtle">
                    <span>{i.source}</span>
                    <span>started {formatRelativeAgo(i.started_at)}</span>
                  </span>
                </OverviewRow>
              ))}
            </OverviewColumn>

            <OverviewColumn id="col-mon" title="Monitoring" tone="ok" count={data.day.monitoring} rows={data.monitoring.length} empty="No incidents are in a monitor window.">
              {data.monitoring.map((i) => (
                <OverviewRow key={i.uuid} to={`/incidents/${i.uuid}`}>
                  <span className="flex items-start gap-3">
                    <span className="flex-1 min-w-0 font-medium line-clamp-2">{incidentTitle(i)}</span>
                    {i.monitor_until && (
                      <span className="text-xs text-state-ok tabular-nums whitespace-nowrap">{formatCountdown(i.monitor_until)}</span>
                    )}
                  </span>
                  {i.completed_at && (
                    <span className="text-xs text-fg-subtle">Resolved {formatRelativeAgo(i.completed_at)}</span>
                  )}
                </OverviewRow>
              ))}
            </OverviewColumn>
          </div>

          <section
            aria-labelledby="overview-day"
            className="flex flex-wrap items-center gap-x-10 gap-y-3 px-4 py-4 rounded-xl border border-line"
          >
            <h2 id="overview-day" className="text-sm font-semibold text-fg-muted">Last 24 hours</h2>
            {[
              { v: data.day.incidents, l: 'incidents' },
              { v: data.day.failed, l: 'failed investigations' },
              { v: data.day.monitoring, l: 'in monitor window now' },
              { v: data.day.proposals, l: 'proposals to review' },
            ].map((d) => (
              <span key={d.l} className="flex items-baseline gap-2">
                <span className="text-xl font-medium tabular-nums text-fg">{d.v}</span>
                <span className="text-sm text-fg-subtle">{d.l}</span>
              </span>
            ))}
            <Link to="/incidents" className="ml-auto text-sm text-primary-400 hover:underline">Open incidents</Link>
          </section>
        </>
      )}
    </div>
  );
}
