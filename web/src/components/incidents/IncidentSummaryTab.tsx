import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { AgentWork } from '../../types';
import IncidentFeedbackStrip from '../IncidentFeedbackStrip';
import IncidentMemoriesLine from './IncidentMemoriesLine';
import { cleanResponse } from './incidentLogHelpers';
import { isFinishedStatus } from './incidentStatus';

interface IncidentSummaryTabProps {
  // An incident or request work.
  incident: AgentWork;
  // Show the feedback strip once the work has finished (incidents only).
  feedback?: boolean;
}

// IncidentSummaryTab renders the agent's final response as Markdown, with
// the memories line and the feedback strip once the investigation has
// finished.
export default function IncidentSummaryTab({ incident, feedback = true }: IncidentSummaryTabProps) {
  if (!incident.response) {
    return (
      <p className="py-8 text-center text-fg-subtle">
        {incident.status === 'pending' || incident.status === 'running'
          ? 'The summary shows here when the agent finishes. Open Timeline to follow the work.'
          : 'There is no summary.'}
      </p>
    );
  }

  if (incident.status === 'failed') {
    return (
      <div>
        <div role="alert" className="px-4 py-3 rounded-lg border border-state-fail/40 bg-state-fail/10">
          <p className="text-sm font-medium text-state-fail">The agent run failed</p>
          <p className="mt-1 text-sm text-fg whitespace-pre-wrap break-words">{cleanResponse(incident.response)}</p>
          <p className="mt-2 text-xs text-fg-subtle">Open Timeline to see where it stopped.</p>
        </div>
        {feedback && <IncidentFeedbackStrip incidentUUID={incident.uuid} />}
      </div>
    );
  }

  return (
    <div>
      <div className="md">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{cleanResponse(incident.response)}</ReactMarkdown>
      </div>
      {feedback && isFinishedStatus(incident.status) && (
        <>
          <IncidentMemoriesLine incidentUUID={incident.uuid} />
          <IncidentFeedbackStrip incidentUUID={incident.uuid} />
        </>
      )}
    </div>
  );
}
