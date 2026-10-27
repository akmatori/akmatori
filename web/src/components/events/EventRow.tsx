import { Link } from 'react-router-dom';
import { ChevronRight, FileCode, MessageSquareText, Shuffle } from 'lucide-react';
import type { EventFeedItem } from '../../types';
import { formatAbsolute, formatClock } from '../../utils/format';
import SourceKindChip from '../shared/SourceKindChip';
import EventKindChip from './EventKindChip';
import { TONE_CLASSES } from '../incidents/incidentStatus';
import CorrelationChip from './CorrelationChip';
import RawEventContent, { type RawState } from './RawEventContent';
import IncidentResponseContent, { type ResponseState } from './IncidentResponseContent';
import { classificationSummary, eventStatusLabel, eventTone } from './eventHelpers';

interface EventRowProps {
  item: EventFeedItem;
  reasoningExpanded: boolean;
  rawExpanded: boolean;
  responseExpanded: boolean;
  rawState?: RawState;
  responseState?: ResponseState;
  onToggleDetails: () => void;
  onFilterByEventId: () => void;
  onToggleResponse: () => void;
  onToggleReasoning: () => void;
  onToggleRaw: () => void;
  onMove: () => void;
}

// EventRow is one event on the Events timeline: time, state dot, source and
// class, title and host, correlation, linked incident and actions. Below it
// sit the optional panels (class, correlation reasoning, raw message,
// incident response or request answer).
export default function EventRow({
  item,
  reasoningExpanded,
  rawExpanded,
  responseExpanded,
  rawState,
  responseState,
  onToggleDetails,
  onFilterByEventId,
  onToggleResponse,
  onToggleReasoning,
  onToggleRaw,
  onMove,
}: EventRowProps) {
  const tone = TONE_CLASSES[eventTone(item.status)];
  const detailsOpen = rawExpanded || reasoningExpanded;
  // Request events point at an agent run, not an incident.
  const runNoun = item.kind === 'request' ? 'run' : 'incident';

  return (
    <li className="border-t border-line first:border-t-0">
      <div className="grid grid-cols-[auto_1fr_auto] md:grid-cols-[4.5rem_auto_1fr_auto_auto] items-start gap-x-3 gap-y-1 px-4 py-2.5 hover:bg-surface-2 transition-colors">
        <time
          dateTime={item.occurred_at}
          title={formatAbsolute(item.occurred_at)}
          className="hidden md:block pt-0.5 font-mono text-xs text-fg-subtle tabular-nums"
        >
          {formatClock(item.occurred_at, true)}
        </time>
        <span className="flex items-center gap-2 pt-0.5">
          <span className={`w-2 h-2 rounded-full ${tone.dot}`} aria-hidden="true" />
          <SourceKindChip kind={item.source_kind} />
          <EventKindChip kind={item.kind} />
        </span>
        <button
          type="button"
          onClick={onToggleDetails}
          aria-expanded={detailsOpen}
          className="min-w-0 text-left flex flex-col gap-0.5 group"
          title="Show the raw message, the class and the correlation details"
        >
          <span className="flex items-start gap-1.5 text-sm text-fg">
            <ChevronRight
              size={14}
              className={`mt-0.5 flex-shrink-0 text-fg-subtle transition-transform ${detailsOpen ? 'rotate-90' : ''}`}
              aria-hidden="true"
            />
            <span className="min-w-0 line-clamp-2 break-words group-hover:underline decoration-line underline-offset-2">
              {item.title || 'Untitled event'}
            </span>
          </span>
          <span className="flex flex-wrap gap-x-3 pl-5 text-xs text-fg-subtle">
            {item.status && <span className={tone.text}>{eventStatusLabel(item.status)}</span>}
            {item.target_host && <span className="font-mono">{item.target_host}</span>}
            {!!item.repeat_count && (
              <span title={item.last_seen_at ? `Last re-sent ${formatAbsolute(item.last_seen_at)}` : undefined}>
                re-sent {item.repeat_count}×
              </span>
            )}
            <span className="md:hidden">{formatClock(item.occurred_at)}</span>
          </span>
        </button>
        <span className="hidden md:flex items-center pt-0.5">
          {item.kind === 'alert' && (
            <CorrelationChip item={item} expanded={reasoningExpanded} onToggle={onToggleReasoning} />
          )}
        </span>
        <span className="flex items-center justify-end gap-1">
          {item.incident_uuid && (
            <>
              <Link
                to={`/incidents/${item.incident_uuid}`}
                className="hidden sm:inline font-mono text-xs text-primary-400 hover:underline"
                title={item.incident_title ? `Open ${runNoun}: ${item.incident_title}` : `Open ${runNoun}`}
              >
                {item.incident_uuid.slice(0, 8)}
              </Link>
              <button
                type="button"
                onClick={onToggleResponse}
                aria-expanded={responseExpanded}
                className={`btn btn-ghost p-1.5 ${responseExpanded ? 'text-primary-400' : 'text-fg-subtle'}`}
                title={`Show the ${runNoun} response`}
                aria-label={`Show the ${runNoun} response`}
              >
                <MessageSquareText size={15} />
              </button>
            </>
          )}
          <button
            type="button"
            onClick={onToggleRaw}
            aria-expanded={rawExpanded}
            className={`btn btn-ghost p-1.5 ${rawExpanded ? 'text-primary-400' : 'text-fg-subtle'}`}
            title="Show the raw message"
            aria-label="Show the raw message"
          >
            <FileCode size={15} />
          </button>
          {item.alert_uuid && (
            <button
              type="button"
              onClick={onMove}
              className="btn btn-ghost p-1.5 text-fg-subtle"
              title="Move this alert to another incident, or unlink it into a new one"
              aria-label="Move alert"
            >
              <Shuffle size={15} />
            </button>
          )}
        </span>
      </div>

      {(detailsOpen || responseExpanded) && (
        <div className="flex flex-col gap-3 px-4 pb-3 md:pl-[calc(4.5rem+2rem)]">
          <span className="flex flex-wrap gap-x-3 text-xs text-fg-subtle">
            <span>Event</span>
            <button type="button" onClick={onFilterByEventId} className="font-mono hover:text-fg hover:underline" title="Show only this event">
              {item.event_uuid}
            </button>
          </span>
          <div className="rounded-md border border-line px-3 py-2">
            <p className="text-xs font-medium text-fg-muted mb-1">{classificationSummary(item)}</p>
            {item.classification_reasoning && (
              <p className="text-sm text-fg whitespace-pre-wrap">{item.classification_reasoning}</p>
            )}
          </div>
          {reasoningExpanded && item.correlation_reasoning && (
            <div className="rounded-md border border-line px-3 py-2">
              <p className="text-xs font-medium text-fg-muted mb-1">Correlation reasoning</p>
              <p className="text-sm text-fg whitespace-pre-wrap">{item.correlation_reasoning}</p>
            </div>
          )}
          {rawExpanded && (
            <div className="rounded-md border border-line bg-canvas px-3 py-2">
              <p className="text-xs font-medium text-fg-muted mb-1">Raw message</p>
              <RawEventContent state={rawState} />
            </div>
          )}
          {responseExpanded && item.incident_uuid && (
            <div className="rounded-md border border-line px-3 py-2">
              <p className="text-xs font-medium text-fg-muted mb-1">
                {item.kind === 'request' ? 'Agent answer' : 'Incident response'}{responseState?.title ? `: ${responseState.title}` : ''}
              </p>
              <IncidentResponseContent state={responseState} />
            </div>
          )}
        </div>
      )}
    </li>
  );
}
