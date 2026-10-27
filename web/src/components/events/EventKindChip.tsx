import type { EventKind } from '../../types';

// EventKindChip shows an event's class. Only alerts open or join incidents,
// so the alert chip is the strongest color; stored-only classes stay quiet.
const CHIPS: Record<EventKind, { label: string; className: string; title: string }> = {
  alert: {
    label: 'Alert',
    className: 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400',
    title: 'Reports a problem: goes through correlation and opens or joins an incident',
  },
  change: {
    label: 'Change',
    className: 'bg-teal-100 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400',
    title: 'Reports a change (deploy, config, maintenance); stored for the agent',
  },
  info: {
    label: 'Info',
    className: 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400',
    title: 'No action needed; stored only',
  },
  request: {
    label: 'Request',
    className: 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-400',
    title: 'Work asked of the agent; starts an agent run, not an incident',
  },
};

export default function EventKindChip({ kind }: { kind: EventKind }) {
  const chip = CHIPS[kind] ?? CHIPS.info;
  return (
    <span
      className={`inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium ${chip.className}`}
      title={chip.title}
    >
      {chip.label}
    </span>
  );
}
