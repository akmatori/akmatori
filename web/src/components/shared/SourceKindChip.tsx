// SourceKindChip labels where an incident or event came from. Shared by the
// Incidents table and the Events page so the two pages stay visually in sync.
// Incidents use the trigger kinds (alert, cron, slack_mention, manual,
// proposal); events use their inbound path (webhook, slack_listener, ...).
const CHIPS: Record<string, { label: string; className: string }> = {
  alert: {
    label: 'Alert',
    className: 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400',
  },
  cron: {
    label: 'Cron',
    className: 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400',
  },
  slack_mention: {
    label: 'Slack',
    className: 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400',
  },
  webhook: {
    label: 'Webhook',
    className: 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400',
  },
  slack_listener: {
    label: 'Slack channel',
    className: 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400',
  },
  proposal: {
    label: 'Proposal',
    className: 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-400',
  },
};

const MANUAL = {
  label: 'Manual',
  className: 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400',
};

export default function SourceKindChip({ kind }: { kind?: string }) {
  const chip = (kind && CHIPS[kind]) || MANUAL;
  return (
    <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium ${chip.className}`}>
      {chip.label}
    </span>
  );
}
