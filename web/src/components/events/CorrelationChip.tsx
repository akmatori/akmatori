import type { EventFeedItem } from '../../types';

interface CorrelationChipProps {
  item: EventFeedItem;
  expanded: boolean;
  onToggle: () => void;
}

// CorrelationChip summarizes how an alert was correlated. When reasoning is
// available the chip is a button that toggles the reasoning row.
export default function CorrelationChip({ item, expanded, onToggle }: CorrelationChipProps) {
  const { correlation_confidence } = item;
  // Historical/seeded alerts can carry correlated=true (with confidence and
  // reasoning) but a NULL correlation_decision. Derive the display state from
  // `correlated` in that case so the reasoning is not silently hidden.
  const correlation_decision = item.correlation_decision || (item.correlated ? 'linked' : '');
  if (!correlation_decision) return null;

  if (correlation_decision === 'linked') {
    const pct = correlation_confidence !== undefined ? Math.round(correlation_confidence * 100) : null;
    const label = `Correlated${pct !== null ? ` ${pct}%` : ''}`;
    if (!item.correlation_reasoning) {
      return (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400">
          {label}
        </span>
      );
    }
    return (
      <button
        onClick={onToggle}
        className={`inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium cursor-pointer transition-colors ${
          expanded
            ? 'bg-green-200 text-green-800 dark:bg-green-800/40 dark:text-green-300'
            : 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400 hover:bg-green-200 dark:hover:bg-green-800/40'
        }`}
        title="Click to see correlation reasoning"
      >
        {label}
      </button>
    );
  }

  if (correlation_decision === 'new_incident') {
    if (!item.correlation_reasoning) {
      return (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400">
          New incident
        </span>
      );
    }
    return (
      <button
        onClick={onToggle}
        className={`inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium cursor-pointer transition-colors ${
          expanded
            ? 'bg-orange-200 text-orange-800 dark:bg-orange-800/40 dark:text-orange-300'
            : 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400 hover:bg-orange-200 dark:hover:bg-orange-800/40'
        }`}
        title="Click to see reasoning"
      >
        New incident
      </button>
    );
  }

  // not_evaluated
  return (
    <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-500">
      Not evaluated
    </span>
  );
}
