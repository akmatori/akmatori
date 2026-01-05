import { useState, useRef, useEffect, useCallback } from 'react';
import { Clock, ChevronDown, Play, Pause, Calendar, Zap } from 'lucide-react';

interface TimeRangePickerProps {
  from: number;
  to: number;
  refreshInterval: number;
  onChange: (from: number, to: number) => void;
  onRefreshIntervalChange: (interval: number) => void;
}

interface TimePreset {
  label: string;
  value: number;
  unit: string;
}

const TIME_PRESETS: TimePreset[] = [
  { label: '5m', value: 5 * 60, unit: 'minutes' },
  { label: '15m', value: 15 * 60, unit: 'minutes' },
  { label: '30m', value: 30 * 60, unit: 'minutes' },
  { label: '1h', value: 60 * 60, unit: 'hour' },
  { label: '3h', value: 3 * 60 * 60, unit: 'hours' },
  { label: '6h', value: 6 * 60 * 60, unit: 'hours' },
  { label: '12h', value: 12 * 60 * 60, unit: 'hours' },
  { label: '24h', value: 24 * 60 * 60, unit: 'hours' },
  { label: '2d', value: 2 * 24 * 60 * 60, unit: 'days' },
  { label: '7d', value: 7 * 24 * 60 * 60, unit: 'days' },
];

const REFRESH_OPTIONS = [
  { label: 'Off', value: 0 },
  { label: '5s', value: 5000 },
  { label: '10s', value: 10000 },
  { label: '30s', value: 30000 },
  { label: '1m', value: 60000 },
  { label: '5m', value: 300000 },
  { label: '15m', value: 900000 },
  { label: '30m', value: 1800000 },
  { label: '1h', value: 3600000 },
];

function formatTimeRange(from: number, to: number): string {
  const now = Math.floor(Date.now() / 1000);
  const diff = to - from;

  // Check if it's a relative time (to is approximately now)
  if (Math.abs(to - now) < 60) {
    for (const preset of TIME_PRESETS) {
      if (Math.abs(diff - preset.value) < 60) {
        return `Last ${preset.label}`;
      }
    }
  }

  // Format as absolute range
  const fromDate = new Date(from * 1000);
  const toDate = new Date(to * 1000);
  const formatOptions: Intl.DateTimeFormatOptions = {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  };
  return `${fromDate.toLocaleString('en-US', formatOptions)} - ${toDate.toLocaleString('en-US', formatOptions)}`;
}

function toLocalDateTimeString(timestamp: number): string {
  const date = new Date(timestamp * 1000);
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  const hours = String(date.getHours()).padStart(2, '0');
  const minutes = String(date.getMinutes()).padStart(2, '0');
  return `${year}-${month}-${day}T${hours}:${minutes}`;
}

function fromLocalDateTimeString(str: string): number {
  const date = new Date(str);
  return Math.floor(date.getTime() / 1000);
}

export default function TimeRangePicker({
  from,
  to,
  refreshInterval,
  onChange,
  onRefreshIntervalChange,
}: TimeRangePickerProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<'relative' | 'absolute'>('relative');
  const [customFrom, setCustomFrom] = useState('');
  const [customTo, setCustomTo] = useState('');
  const [isRefreshing, setIsRefreshing] = useState(refreshInterval > 0);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);

  // Update custom fields when opening absolute tab
  useEffect(() => {
    if (activeTab === 'absolute') {
      setCustomFrom(toLocalDateTimeString(from));
      setCustomTo(toLocalDateTimeString(to));
    }
  }, [activeTab, from, to]);

  // Close dropdown when clicking outside
  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (
        dropdownRef.current &&
        !dropdownRef.current.contains(event.target as Node) &&
        buttonRef.current &&
        !buttonRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
      }
    }

    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [isOpen]);

  const handlePresetClick = useCallback(
    (preset: TimePreset) => {
      const now = Math.floor(Date.now() / 1000);
      onChange(now - preset.value, now);
      setIsOpen(false);
    },
    [onChange]
  );

  const handleApplyCustomRange = useCallback(() => {
    if (customFrom && customTo) {
      const fromTs = fromLocalDateTimeString(customFrom);
      const toTs = fromLocalDateTimeString(customTo);
      if (fromTs < toTs) {
        onChange(fromTs, toTs);
        setIsOpen(false);
      }
    }
  }, [customFrom, customTo, onChange]);

  const toggleRefresh = useCallback(() => {
    if (isRefreshing) {
      onRefreshIntervalChange(0);
      setIsRefreshing(false);
    } else {
      onRefreshIntervalChange(60000); // Default to 1m
      setIsRefreshing(true);
    }
  }, [isRefreshing, onRefreshIntervalChange]);

  const handleRefreshIntervalChange = useCallback(
    (value: number) => {
      onRefreshIntervalChange(value);
      setIsRefreshing(value > 0);
    },
    [onRefreshIntervalChange]
  );

  // Find current preset match
  const now = Math.floor(Date.now() / 1000);
  const diff = to - from;
  const isRelative = Math.abs(to - now) < 60;
  const currentPreset = isRelative
    ? TIME_PRESETS.find((p) => Math.abs(diff - p.value) < 60)
    : null;

  return (
    <div className="relative">
      {/* Main Button */}
      <button
        ref={buttonRef}
        onClick={() => setIsOpen(!isOpen)}
        className="
          group flex items-center gap-2.5 px-3 py-2
          bg-gray-100 dark:bg-gray-800/80
          hover:bg-gray-200 dark:hover:bg-gray-700/80
          border border-gray-200 dark:border-gray-700
          rounded-lg transition-all duration-150
          text-gray-700 dark:text-gray-200
          shadow-sm hover:shadow
        "
      >
        <Clock className="w-4 h-4 text-gray-500 dark:text-gray-400 group-hover:text-primary-500 transition-colors" />
        <span className="text-sm font-medium tracking-tight">
          {formatTimeRange(from, to)}
        </span>
        <ChevronDown
          className={`w-4 h-4 text-gray-400 transition-transform duration-200 ${
            isOpen ? 'rotate-180' : ''
          }`}
        />

        {/* Refresh indicator */}
        {refreshInterval > 0 && (
          <div className="flex items-center gap-1.5 ml-1 pl-2.5 border-l border-gray-300 dark:border-gray-600">
            <div className="w-1.5 h-1.5 bg-emerald-500 rounded-full animate-pulse" />
            <span className="text-xs text-gray-500 dark:text-gray-400 font-mono">
              {REFRESH_OPTIONS.find((r) => r.value === refreshInterval)?.label || ''}
            </span>
          </div>
        )}
      </button>

      {/* Dropdown Panel */}
      {isOpen && (
        <div
          ref={dropdownRef}
          className="
            absolute right-0 top-full mt-2 z-50
            w-80
            bg-white dark:bg-gray-800
            border border-gray-200 dark:border-gray-700
            rounded-xl shadow-xl
            overflow-hidden
            animate-fade-in
          "
        >
          {/* Tabs */}
          <div className="flex border-b border-gray-200 dark:border-gray-700">
            <button
              onClick={() => setActiveTab('relative')}
              className={`
                flex-1 flex items-center justify-center gap-2 px-4 py-3
                text-sm font-medium transition-colors
                ${
                  activeTab === 'relative'
                    ? 'text-primary-600 dark:text-primary-400 bg-primary-50 dark:bg-primary-500/10 border-b-2 border-primary-500'
                    : 'text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-700/50'
                }
              `}
            >
              <Zap className="w-4 h-4" />
              Relative
            </button>
            <button
              onClick={() => setActiveTab('absolute')}
              className={`
                flex-1 flex items-center justify-center gap-2 px-4 py-3
                text-sm font-medium transition-colors
                ${
                  activeTab === 'absolute'
                    ? 'text-primary-600 dark:text-primary-400 bg-primary-50 dark:bg-primary-500/10 border-b-2 border-primary-500'
                    : 'text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-700/50'
                }
              `}
            >
              <Calendar className="w-4 h-4" />
              Absolute
            </button>
          </div>

          {/* Tab Content */}
          <div className="p-3">
            {activeTab === 'relative' ? (
              <div className="grid grid-cols-5 gap-1.5">
                {TIME_PRESETS.map((preset) => (
                  <button
                    key={preset.label}
                    onClick={() => handlePresetClick(preset)}
                    className={`
                      px-2 py-2.5 rounded-lg text-sm font-medium
                      transition-all duration-150
                      ${
                        currentPreset?.label === preset.label
                          ? 'bg-primary-500 text-white shadow-md shadow-primary-500/25'
                          : 'bg-gray-100 dark:bg-gray-700/50 text-gray-700 dark:text-gray-300 hover:bg-gray-200 dark:hover:bg-gray-600/50'
                      }
                    `}
                  >
                    {preset.label}
                  </button>
                ))}
              </div>
            ) : (
              <div className="space-y-4">
                <div>
                  <label className="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1.5 uppercase tracking-wide">
                    From
                  </label>
                  <input
                    type="datetime-local"
                    value={customFrom}
                    onChange={(e) => setCustomFrom(e.target.value)}
                    className="
                      w-full px-3 py-2.5
                      bg-gray-50 dark:bg-gray-700/50
                      border border-gray-200 dark:border-gray-600
                      rounded-lg text-sm
                      text-gray-700 dark:text-gray-200
                      focus:outline-none focus:ring-2 focus:ring-primary-500/50 focus:border-primary-500
                    "
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1.5 uppercase tracking-wide">
                    To
                  </label>
                  <input
                    type="datetime-local"
                    value={customTo}
                    onChange={(e) => setCustomTo(e.target.value)}
                    className="
                      w-full px-3 py-2.5
                      bg-gray-50 dark:bg-gray-700/50
                      border border-gray-200 dark:border-gray-600
                      rounded-lg text-sm
                      text-gray-700 dark:text-gray-200
                      focus:outline-none focus:ring-2 focus:ring-primary-500/50 focus:border-primary-500
                    "
                  />
                </div>
                <button
                  onClick={handleApplyCustomRange}
                  disabled={!customFrom || !customTo}
                  className="
                    w-full py-2.5 px-4
                    bg-primary-500 hover:bg-primary-600
                    disabled:bg-gray-300 dark:disabled:bg-gray-600
                    text-white font-medium text-sm
                    rounded-lg transition-colors
                    disabled:cursor-not-allowed
                  "
                >
                  Apply Range
                </button>
              </div>
            )}
          </div>

          {/* Refresh Interval Footer */}
          <div className="px-3 py-3 bg-gray-50 dark:bg-gray-900/50 border-t border-gray-200 dark:border-gray-700">
            <div className="flex items-center gap-3">
              {/* Play/Pause Button */}
              <button
                onClick={toggleRefresh}
                className={`
                  flex items-center justify-center w-8 h-8 rounded-lg
                  transition-all duration-150
                  ${
                    isRefreshing
                      ? 'bg-emerald-500/20 text-emerald-600 dark:text-emerald-400 hover:bg-emerald-500/30'
                      : 'bg-gray-200 dark:bg-gray-700 text-gray-500 dark:text-gray-400 hover:bg-gray-300 dark:hover:bg-gray-600'
                  }
                `}
                title={isRefreshing ? 'Pause auto-refresh' : 'Start auto-refresh'}
              >
                {isRefreshing ? (
                  <Pause className="w-4 h-4" />
                ) : (
                  <Play className="w-4 h-4" />
                )}
              </button>

              {/* Refresh Interval Options */}
              <div className="flex-1 flex items-center gap-1 overflow-x-auto">
                {REFRESH_OPTIONS.map((option) => (
                  <button
                    key={option.value}
                    onClick={() => handleRefreshIntervalChange(option.value)}
                    className={`
                      px-2 py-1.5 rounded text-xs font-medium whitespace-nowrap
                      transition-colors duration-150
                      ${
                        refreshInterval === option.value
                          ? 'bg-primary-500 text-white'
                          : 'text-gray-500 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-700'
                      }
                    `}
                  >
                    {option.label}
                  </button>
                ))}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
