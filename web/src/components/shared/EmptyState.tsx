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
    <div className={`${padding} text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg`}>
      {Icon && <Icon className="w-12 h-12 mx-auto text-gray-400 mb-3" />}
      <p className="text-gray-500 dark:text-gray-400">{title}</p>
      {description && (
        <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">{description}</p>
      )}
    </div>
  );
}
