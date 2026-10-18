import { useState } from 'react';
import { MessageSquarePlus, Check, ChevronDown, ChevronRight } from 'lucide-react';
import { incidentsApi } from '../api/client';

interface IncidentFeedbackStripProps {
  incidentUUID: string;
}

// IncidentFeedbackStrip is the collapsible "Rate this resolution" affordance
// shown under the response of a finished incident. Submissions land in the
// existing POST /api/incidents/{uuid}/feedback endpoint, which persists the
// text as a global feedback memory that the improvement-evaluator cron reads
// on its next pass.
export default function IncidentFeedbackStrip({ incidentUUID }: IncidentFeedbackStripProps) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [error, setError] = useState('');

  const submit = async () => {
    const trimmed = text.trim();
    if (!trimmed || submitting) return;
    setSubmitting(true);
    setError('');
    try {
      await incidentsApi.sendFeedback(incidentUUID, trimmed);
      setSubmitted(true);
      setText('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to submit feedback');
    } finally {
      setSubmitting(false);
    }
  };

  if (submitted) {
    return (
      <div className="mt-4 flex items-center gap-2 px-4 py-3 rounded-lg bg-state-ok/10 text-state-ok text-sm">
        <Check className="w-4 h-4" />
        <span>
          Feedback saved as memory — it will inform future investigations and the next
          improvement review.
        </span>
      </div>
    );
  }

  return (
    <div className="mt-4 border border-line rounded-lg">
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center gap-2 px-4 py-3 text-sm text-fg-muted hover:bg-surface-2 transition-colors rounded-lg"
      >
        <MessageSquarePlus className="w-4 h-4 text-primary-500" />
        <span className="font-medium">Rate this resolution</span>
        <span className="text-fg-subtle text-xs">
          — was the root cause right? What was missed?
        </span>
        <span className="ml-auto">
          {open ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
        </span>
      </button>
      {open && (
        <div className="px-4 pb-4">
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="e.g. Root cause was actually a stale DNS cache, not the upstream — next time check resolv.conf on the edge hosts first."
            rows={3}
            className="w-full resize-none rounded-lg border border-line bg-surface px-3 py-2 text-sm text-fg placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-primary-500"
          />
          {error && <p className="text-xs text-red-500 mt-1">{error}</p>}
          <div className="mt-2 flex justify-end">
            <button
              onClick={submit}
              disabled={submitting || !text.trim()}
              className="px-4 py-2 text-sm rounded-lg bg-primary-500 text-white hover:bg-primary-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            >
              {submitting ? 'Saving…' : 'Submit feedback'}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
