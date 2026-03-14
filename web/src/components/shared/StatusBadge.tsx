interface StatusBadgeProps {
  label: string;
  variant?: 'primary' | 'success' | 'default' | 'warning' | 'danger';
}

const variantClasses: Record<string, string> = {
  primary: 'badge-primary',
  success: 'badge-success',
  default: 'badge-default',
  warning: 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-300',
  danger: 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300',
};

export default function StatusBadge({ label, variant = 'default' }: StatusBadgeProps) {
  return (
    <span className={`badge ${variantClasses[variant] || variantClasses.default}`}>
      {label}
    </span>
  );
}

export function EnabledBadge({ enabled }: { enabled: boolean }) {
  return (
    <StatusBadge
      label={enabled ? 'Enabled' : 'Disabled'}
      variant={enabled ? 'success' : 'default'}
    />
  );
}
