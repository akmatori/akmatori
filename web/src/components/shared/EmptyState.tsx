import type { LucideIcon } from 'lucide-react';

interface EmptyStateProps {
  icon?: LucideIcon;
  title: string;
  description?: string;
  // Vertical padding; lists use the default, compact panels pass 'py-12'.
  padding?: string;
}

// EmptyState is the dashed placeholder shown when a list has no rows.
export default function EmptyState({ icon: Icon, title, description, padding = 'py-16' }: EmptyStateProps) {
  return (
    <div className={`${padding} px-6 text-center border border-dashed border-line rounded-lg`}>
      {Icon && <Icon className="w-8 h-8 mx-auto text-fg-subtle mb-3" aria-hidden="true" />}
      <p className="text-fg">{title}</p>
      {description && (
        <p className="text-sm text-fg-subtle mt-1">{description}</p>
      )}
    </div>
  );
}
