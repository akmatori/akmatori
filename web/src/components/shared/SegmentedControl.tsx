interface SegmentedOption<T extends string> {
  value: T;
  label: string;
  title?: string;
}

interface SegmentedControlProps<T extends string> {
  value: T;
  options: SegmentedOption<T>[];
  onChange: (value: T) => void;
  // Per-button padding; defaults suit short text labels.
  buttonClassName?: string;
}

// SegmentedControl is a bordered group of mutually exclusive buttons, used
// for view toggles such as Open/History or 1h/3h.
export default function SegmentedControl<T extends string>({
  value,
  options,
  onChange,
  buttonClassName = 'h-8 px-3',
}: SegmentedControlProps<T>) {
  return (
    <div role="group" className="flex flex-shrink-0 rounded-md border border-line overflow-hidden">
      {options.map((opt, i) => (
        <button
          key={opt.value}
          type="button"
          onClick={() => onChange(opt.value)}
          aria-pressed={value === opt.value}
          title={opt.title}
          className={`${buttonClassName} text-xs font-medium transition-colors ${
            i > 0 ? 'border-l border-line ' : ''
          }${
            value === opt.value
              ? 'bg-primary-500/15 text-fg'
              : 'bg-surface text-fg-muted hover:bg-surface-2 hover:text-fg'
          }`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}
