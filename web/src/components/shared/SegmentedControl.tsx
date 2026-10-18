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
  buttonClassName = 'px-3 py-1',
}: SegmentedControlProps<T>) {
  return (
    <div className="flex rounded-lg border border-gray-300 dark:border-gray-600 overflow-hidden">
      {options.map((opt, i) => (
        <button
          key={opt.value}
          onClick={() => onChange(opt.value)}
          title={opt.title}
          className={`${buttonClassName} text-xs font-medium transition-colors ${
            i > 0 ? 'border-l border-gray-300 dark:border-gray-600 ' : ''
          }${
            value === opt.value
              ? 'bg-primary-500 text-white'
              : 'bg-white dark:bg-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-700'
          }`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}
