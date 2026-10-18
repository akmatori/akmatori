import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';

type ColumnTone = 'need' | 'work' | 'ok';

// Literal class names so Tailwind picks them up.
const TONE: Record<ColumnTone, { rail: string; count: string }> = {
  need: { rail: 'border-t-state-need', count: 'text-state-need' },
  work: { rail: 'border-t-state-work', count: 'text-state-work' },
  ok: { rail: 'border-t-state-ok', count: 'text-state-ok' },
};

interface OverviewColumnProps {
  id: string;
  title: string;
  tone: ColumnTone;
  count: number;
  // Rendered rows; can differ from count when rows are capped or grouped.
  rows: number;
  empty: string;
  children: ReactNode;
}

// OverviewColumn is one of the Overview state columns: a colored top rail,
// a title with a count, and a list of rows.
export function OverviewColumn({ id, title, tone, count, rows, empty, children }: OverviewColumnProps) {
  return (
    <section
      aria-labelledby={id}
      className="flex flex-col rounded-xl border border-line bg-surface overflow-hidden"
    >
      <div className={`flex items-baseline gap-2 px-4 pt-3.5 pb-2.5 border-t-[3px] ${TONE[tone].rail}`}>
        <h2 id={id} className="text-sm font-semibold text-fg">{title}</h2>
        <span className={`tabular-nums ${TONE[tone].count}`}>{count}</span>
      </div>
      {rows === 0 ? (
        <p className="px-4 py-6 border-t border-line text-sm text-fg-subtle">{empty}</p>
      ) : (
        <ul>{children}</ul>
      )}
    </section>
  );
}

interface OverviewRowProps {
  to: string;
  children: ReactNode;
}

export function OverviewRow({ to, children }: OverviewRowProps) {
  return (
    <li className="border-t border-line">
      <Link to={to} className="flex flex-col gap-1 px-4 py-3 text-fg hover:bg-surface-2 transition-colors">
        {children}
      </Link>
    </li>
  );
}
