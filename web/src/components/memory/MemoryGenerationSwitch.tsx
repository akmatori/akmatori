import { useEffect, useState } from 'react';
import { generalSettingsApi } from '../../api/client';
import { getErrorMessage } from '../../utils/errors';

// MemoryGenerationSwitch turns agent-written memory on or off
// (GeneralSettings.memory_generation_enabled). Off, agents stop recording
// findings and the server discards anything they write to memory; operator
// edits on this page still work. Saves on toggle.
export default function MemoryGenerationSwitch() {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    generalSettingsApi
      .get()
      .then((s) => {
        if (!cancelled) setEnabled(s.memory_generation_enabled);
      })
      .catch((err) => {
        if (!cancelled) setError(getErrorMessage(err, 'Failed to load the memory setting'));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const toggle = async () => {
    if (enabled === null || saving) return;
    const next = !enabled;
    setSaving(true);
    setError('');
    try {
      const saved = await generalSettingsApi.update({ memory_generation_enabled: next });
      setEnabled(saved.memory_generation_enabled);
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to save the memory setting'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex items-start justify-between gap-4">
      <div>
        <p id="memory-generation-label" className="text-sm font-medium text-fg">
          Generate memory from incidents
        </p>
        <p className="text-xs text-fg-subtle">
          The agent records what it learns during investigations and cron runs. Off, it stops, and the server
          discards anything it writes. You can still add and edit memories here.
        </p>
        {error && <p className="mt-1 text-xs text-state-fail">{error}</p>}
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={enabled ?? false}
        aria-labelledby="memory-generation-label"
        onClick={() => void toggle()}
        disabled={enabled === null || saving}
        className={`relative inline-flex h-6 w-11 flex-shrink-0 items-center rounded-full transition-colors disabled:opacity-50 ${
          enabled ? 'bg-blue-600' : 'bg-gray-300 dark:bg-gray-600'
        }`}
      >
        <span
          className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
            enabled ? 'translate-x-6' : 'translate-x-1'
          }`}
        />
      </button>
    </div>
  );
}
