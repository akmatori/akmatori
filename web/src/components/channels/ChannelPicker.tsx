import { useEffect, useState } from 'react';
import { channelsApi } from '../../api/client';
import type { Channel } from '../../types';
import { buildChannelPickerOptions, type ChannelPickerOption } from './channelHelpers';

interface ChannelPickerProps {
  // Selected channel UUID. Empty string / undefined / null all mean "no
  // explicit channel" and the backend will fall back to the provider default.
  value: string | null | undefined;
  onChange: (uuid: string | null) => void;
  // Optional override; tests inject a pre-built list. When omitted, the picker
  // fetches its own list filtered by can_post=true.
  channels?: Channel[];
  // Label rendered above the select; defaults are fine for the AlertSourceForm
  // surface but the manager pages can customise.
  label?: string;
  // Allowed-empty toggle. When true, the picker exposes a "(Use default)"
  // option that emits null so callers can clear the binding.
  allowEmpty?: boolean;
  disabled?: boolean;
  // Test seam: pass a pre-built option list to skip the API call and the
  // build step. Used by the unit tests so they can exercise rendering
  // without spinning up fetch mocks.
  options?: ChannelPickerOption[];
}

export default function ChannelPicker({
  value,
  onChange,
  channels,
  label = 'Channel',
  allowEmpty = true,
  disabled = false,
  options: optionsProp,
}: ChannelPickerProps) {
  const [fetchedChannels, setFetchedChannels] = useState<Channel[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (channels !== undefined || optionsProp !== undefined) {
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const data = await channelsApi.list({ can_post: true });
        if (!cancelled) setFetchedChannels(data);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load channels');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [channels, optionsProp]);

  const options =
    optionsProp ?? buildChannelPickerOptions(channels ?? fetchedChannels ?? []);

  return (
    <div>
      <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
        {label}
      </label>
      <select
        className="input-field"
        value={value ?? ''}
        disabled={disabled}
        onChange={(e) => {
          const next = e.target.value;
          onChange(next === '' ? null : next);
        }}
      >
        {allowEmpty && <option value="">— Use provider default —</option>}
        {options.map((opt) => (
          <option key={opt.uuid} value={opt.uuid}>
            {opt.icon} {opt.label}
            {opt.isDefault ? ' (default)' : ''}
          </option>
        ))}
      </select>
      {error && (
        <p className="mt-1 text-xs text-red-600 dark:text-red-400">{error}</p>
      )}
      {!error && options.length === 0 && (
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
          No post-capable channels configured. Add one under Settings &rarr; Channels.
        </p>
      )}
    </div>
  );
}
