import type { AgentRun } from '../../types';
import { formatExecutionTime, formatShortDateTime, formatTokens } from '../../utils/format';
import { runStatusLabel, runStatusTone } from './runHelpers';
import { TONE_CLASSES } from '../incidents/incidentStatus';

interface RunsTabProps {
  runs: AgentRun[];
}

// RunsTab lists the agent runs of an incident or a request, oldest first:
// the first investigation and each follow-up turn, with model, time, tokens
// and how each run ended.
export default function RunsTab({ runs }: RunsTabProps) {
  if (runs.length === 0) {
    return <p className="py-8 text-center text-fg-subtle">No agent run yet.</p>;
  }
  return (
    <ol className="flex flex-col gap-2">
      {runs.map((run, i) => {
        const tone = TONE_CLASSES[runStatusTone(run.status)];
        return (
          <li key={run.run_id} className="px-4 py-3 rounded-lg border border-line bg-canvas">
            <p className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
              <span className="font-medium text-fg">Run {i + 1}</span>
              <span className={`inline-flex items-center gap-1.5 ${tone.text}`}>
                <span className={`w-2 h-2 rounded-full ${tone.dot}`} aria-hidden="true" />
                {runStatusLabel(run.status)}
              </span>
              <span className="text-fg-muted" title={run.started_at}>{formatShortDateTime(run.started_at)}</span>
              {run.model && <span className="font-mono text-xs text-fg-muted">{run.model}</span>}
            </p>
            <p className="flex flex-wrap gap-x-4 gap-y-1 mt-1 text-xs text-fg-subtle">
              {run.execution_time_ms > 0 && <span>{formatExecutionTime(run.execution_time_ms)}</span>}
              {run.tokens_used > 0 && <span>{formatTokens(run.tokens_used)} tokens</span>}
              {run.outcome && <span>Outcome: {run.outcome}</span>}
              <code className="font-mono">{run.run_id.slice(0, 8)}</code>
            </p>
            {run.error_message && (
              <p className="mt-1 text-xs text-state-fail break-words">{run.error_message}</p>
            )}
          </li>
        );
      })}
    </ol>
  );
}
