import { useMemo } from 'react';
import type { AgentWork } from '../../types';
import { formatExecutionTime, formatTokens } from '../../utils/format';
import { parseTimeline, summarizeTools, type TimelineStep } from './incidentLogHelpers';

interface IncidentTimelineTabProps {
  // An incident or request work.
  incident: AgentWork;
  // True while the panel polls an in-flight run.
  live: boolean;
}

function StepMarker({ className }: { className: string }) {
  return (
    <span className="flex flex-col items-center" aria-hidden="true">
      <span className={`mt-1.5 w-2 h-2 rounded-full ${className}`} />
      <span className="flex-1 w-px bg-line mt-1" />
    </span>
  );
}

function Step({ step, live }: { step: TimelineStep; live: boolean }) {
  if (step.kind === 'note') {
    return (
      <li className="grid grid-cols-[12px_1fr] gap-3">
        <StepMarker className="bg-fg-subtle" />
        <p className="pb-4 whitespace-pre-wrap text-fg">{step.text}</p>
      </li>
    );
  }
  if (step.kind === 'running') {
    return (
      <li className="grid grid-cols-[12px_1fr] gap-3">
        <StepMarker className={`bg-state-work ${live ? 'animate-pulse' : ''}`} />
        <p className="pb-4 text-sm text-state-work">Calling {summarizeTools(step.tools)}</p>
      </li>
    );
  }
  return (
    <li className="grid grid-cols-[12px_1fr] gap-3">
      <StepMarker className={step.ok ? 'bg-state-ok' : 'bg-state-fail'} />
      <details className="pb-4 min-w-0 group">
        <summary className="flex items-start gap-2 cursor-pointer list-none text-sm">
          <span className={`flex-shrink-0 ${step.ok ? 'text-state-ok' : 'text-state-fail'}`}>
            {step.ok ? 'Ran' : 'Failed'}
          </span>
          <code className="min-w-0 font-mono text-xs leading-5 text-fg-muted truncate group-open:whitespace-pre-wrap group-open:break-all">
            {step.command}
          </code>
        </summary>
        {step.output ? (
          <pre className="mt-2 max-h-96 overflow-auto rounded-md bg-canvas border border-line p-3 font-mono text-xs text-fg-muted whitespace-pre-wrap break-all">
            {step.output}
          </pre>
        ) : (
          <p className="mt-2 text-xs text-fg-subtle">No output.</p>
        )}
      </details>
    </li>
  );
}

// IncidentTimelineTab shows the agent's log as ordered steps. Tool calls
// collapse to one line and expand to the command and its output.
export default function IncidentTimelineTab({ incident, live }: IncidentTimelineTabProps) {
  // "Calling X" repeats the "Ran X" step that follows it; keep it only as
  // the in-flight step of a live run.
  const steps = useMemo(() => {
    const all = parseTimeline(incident.full_log ?? '');
    return all.filter((s, i) => s.kind !== 'running' || (live && i === all.length - 1));
  }, [incident.full_log, live]);
  const toolCalls = steps.filter((s) => s.kind === 'tool');
  const failedCalls = toolCalls.filter((s) => s.kind === 'tool' && !s.ok).length;

  if (steps.length === 0) {
    return (
      <p className="py-8 text-center text-fg-subtle">
        {incident.status === 'pending' ? 'The agent has not started yet.' : 'The log is empty.'}
      </p>
    );
  }

  return (
    <div>
      <p className="flex flex-wrap gap-x-5 gap-y-1 mb-5 text-sm text-fg-subtle">
        <span>{toolCalls.length} tool calls</span>
        {failedCalls > 0 && <span className="text-state-fail">{failedCalls} failed</span>}
        {incident.execution_time_ms > 0 && <span>{formatExecutionTime(incident.execution_time_ms)}</span>}
        {incident.tokens_used > 0 && <span>{formatTokens(incident.tokens_used)} tokens</span>}
        {live && <span className="text-state-work">Live, updates every 2s</span>}
      </p>
      <ol>
        {steps.map((step, i) => (
          <Step key={i} step={step} live={live && i === steps.length - 1} />
        ))}
      </ol>
    </div>
  );
}
