import { Fragment } from 'react';
import { Link } from 'react-router-dom';
import { Activity, Link as LinkIcon, Shuffle, FileCode, MessageSquareText } from 'lucide-react';
import type { EventFeedItem } from '../../types';
import { formatRelativeAgo, formatAbsolute } from '../../utils/format';
import SourceKindChip from '../shared/SourceKindChip';
import EventStatusBadge from './EventStatusBadge';
import CorrelationChip from './CorrelationChip';
import RawEventContent, { type RawState } from './RawEventContent';
import IncidentResponseContent, { type ResponseState } from './IncidentResponseContent';

interface FeedEventRowProps {
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

// FeedEventRow renders one feed event plus its optional expansion rows
// (correlation reasoning, raw message, linked incident response).
export default function FeedEventRow({
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
}: FeedEventRowProps) {
  // Any alert can be reassigned (origin or correlated).
  const showMove = item.event_type === 'alert';

  return (
    <Fragment>
      <tr
        onClick={onToggleDetails}
        className="cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800/50"
        title="Click to see raw message and correlation details"
      >
        <td className="text-gray-500 dark:text-gray-400 text-sm whitespace-nowrap" title={formatAbsolute(item.occurred_at)}>
          {formatRelativeAgo(item.occurred_at)}
        </td>
        <td>
          <button
            onClick={(e) => { e.stopPropagation(); onFilterByEventId(); }}
            className="font-mono text-xs text-gray-500 dark:text-gray-400 hover:text-primary-600 dark:hover:text-primary-400 hover:underline"
            title={`${item.event_uuid} — click to filter the feed to this event`}
          >
            {item.event_uuid.slice(0, 8)}
          </button>
        </td>
        <td>
          <SourceKindChip kind={item.event_type} />
        </td>
        <td className="max-w-xs">
          <div className="flex flex-col gap-0.5">
            <span className="text-gray-800 dark:text-gray-100 text-sm font-medium truncate" title={item.title || undefined}>
              {item.title || <span className="text-gray-400 italic font-normal">Untitled</span>}
            </span>
            {item.target_host && (
              <span className="text-xs text-gray-400 dark:text-gray-500 truncate" title={item.target_host}>
                {item.target_host}
              </span>
            )}
          </div>
        </td>
        <td>
          <EventStatusBadge status={item.status} />
        </td>
        <td>
          {item.incident_uuid ? (
            <div className="flex flex-col gap-0.5">
              <div className="flex items-center gap-1.5">
                <button
                  onClick={(e) => { e.stopPropagation(); onToggleResponse(); }}
                  className={`inline-flex items-center gap-1 text-xs hover:underline ${
                    responseExpanded
                      ? 'text-primary-700 dark:text-primary-300'
                      : 'text-primary-600 dark:text-primary-400'
                  }`}
                  title="Click to see the incident's response"
                >
                  <MessageSquareText className="w-3 h-3" />
                  <code>{item.incident_uuid.slice(0, 8)}</code>
                </button>
                <Link
                  to={`/incidents/${item.incident_uuid}`}
                  onClick={(e) => e.stopPropagation()}
                  className="text-gray-400 hover:text-primary-600 dark:hover:text-primary-400"
                  title="Open incident page"
                >
                  <LinkIcon className="w-3 h-3" />
                </Link>
              </div>
              {item.incident_title && (
                <span className="text-xs text-gray-400 dark:text-gray-500 truncate max-w-[140px]" title={item.incident_title}>
                  {item.incident_title}
                </span>
              )}
            </div>
          ) : (
            <span className="text-xs text-gray-400">—</span>
          )}
        </td>
        <td onClick={(e) => e.stopPropagation()}>
          {item.event_type === 'alert' && (
            <CorrelationChip item={item} expanded={reasoningExpanded} onToggle={onToggleReasoning} />
          )}
        </td>
        <td onClick={(e) => e.stopPropagation()}>
          <div className="flex items-center gap-1 justify-end">
            <button
              onClick={onToggleRaw}
              className={`btn btn-ghost p-1.5 ${
                rawExpanded
                  ? 'text-primary-600 dark:text-primary-400'
                  : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-300'
              }`}
              title="View raw alert/event message"
            >
              <FileCode className="w-4 h-4" />
            </button>
            {showMove && (
              <button
                onClick={onMove}
                className="btn btn-ghost p-1.5 text-orange-500 hover:text-orange-700 dark:text-orange-400 dark:hover:text-orange-300"
                title="Move alert to another incident (or unlink into a new one)"
              >
                <Shuffle className="w-4 h-4" />
              </button>
            )}
          </div>
        </td>
      </tr>
      {reasoningExpanded && item.correlation_reasoning && (
        <tr className="bg-gray-50 dark:bg-gray-900/30">
          <td colSpan={8} className="px-4 py-3">
            <div className="flex items-start gap-2">
              <Activity className="w-4 h-4 text-gray-400 mt-0.5 flex-shrink-0" />
              <div>
                <span className="text-[10px] font-medium uppercase tracking-wide text-gray-400 block mb-1">Correlation reasoning</span>
                <p className="text-xs text-gray-600 dark:text-gray-300 whitespace-pre-wrap">
                  {item.correlation_reasoning}
                </p>
              </div>
            </div>
          </td>
        </tr>
      )}
      {rawExpanded && (
        <tr className="bg-gray-900">
          <td colSpan={8} className="px-4 py-3">
            <div className="flex items-center gap-2 text-gray-500 mb-2">
              <FileCode className="w-3.5 h-3.5" />
              <span className="text-[10px] font-medium uppercase tracking-wide">Raw message</span>
            </div>
            <RawEventContent state={rawState} />
          </td>
        </tr>
      )}
      {responseExpanded && item.incident_uuid && (
        <tr className="bg-gray-50 dark:bg-gray-900/30">
          <td colSpan={8} className="px-4 py-3">
            <div className="flex items-center gap-2 text-gray-500 mb-2">
              <MessageSquareText className="w-3.5 h-3.5" />
              <span className="text-[10px] font-medium uppercase tracking-wide">
                Incident response{responseState?.title ? ` — ${responseState.title}` : ''}
              </span>
            </div>
            <IncidentResponseContent state={responseState} />
          </td>
        </tr>
      )}
    </Fragment>
  );
}
