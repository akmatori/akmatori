import { useState, useEffect } from 'react';
import { Save, Info } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { SuccessMessage } from '../ErrorMessage';
import { retentionSettingsApi } from '../../api/client';
import type { RetentionSettings } from '../../types';

interface RetentionSettingsSectionProps {
  onStatusChange?: (status: 'configured' | 'disabled' | undefined) => void;
}

export default function RetentionSettingsSection({ onStatusChange }: RetentionSettingsSectionProps) {
  const [, setSettings] = useState<RetentionSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [retentionDays, setRetentionDays] = useState(90);
  const [cleanupIntervalHours, setCleanupIntervalHours] = useState(6);

  useEffect(() => {
    loadSettings();
  }, []);

  const loadSettings = async () => {
    try {
      setLoading(true);
      const data = await retentionSettingsApi.get();
      setSettings(data);
      setEnabled(data.enabled);
      setRetentionDays(data.retention_days);
      setCleanupIntervalHours(data.cleanup_interval_hours);
      setError(null);
      onStatusChange?.(data.enabled ? 'configured' : 'disabled');
    } catch (err) {
      setError('Failed to load retention settings');
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    try {
      setSaving(true);
      setError(null);
      setSuccess(false);

      const updated = await retentionSettingsApi.update({
        enabled,
        retention_days: retentionDays,
        cleanup_interval_hours: cleanupIntervalHours,
      });
      setSettings(updated);
      onStatusChange?.(updated.enabled ? 'configured' : 'disabled');
      setSuccess(true);
      setTimeout(() => setSuccess(false), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save retention settings');
      console.error(err);
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return <LoadingSpinner />;
  }

  return (
    <div className="space-y-5">
      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message="Retention settings saved" />}

      <div className="flex items-center justify-between">
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
            Enable automatic cleanup
          </label>
          <p className="text-xs text-gray-500 dark:text-gray-400">
            Automatically delete old incident data (working directories and database records)
          </p>
        </div>
        <button
          type="button"
          role="switch"
          aria-checked={enabled}
          onClick={() => setEnabled(!enabled)}
          className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
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

      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
          Retention period (days)
        </label>
        <input
          type="number"
          min={1}
          value={retentionDays}
          onChange={(e) => setRetentionDays(Math.max(1, parseInt(e.target.value) || 1))}
          disabled={!enabled}
          className="input-field"
        />
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
          Incidents older than this will be deleted. Includes working directories, logs, and database records.
        </p>
      </div>

      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
          Cleanup interval (hours)
        </label>
        <input
          type="number"
          min={1}
          value={cleanupIntervalHours}
          onChange={(e) => setCleanupIntervalHours(Math.max(1, parseInt(e.target.value) || 1))}
          disabled={!enabled}
          className="input-field"
        />
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
          How often the background cleanup runs.
        </p>
      </div>

      <div className="flex items-center justify-between pt-4 border-t border-gray-200 dark:border-gray-700">
        <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-1.5">
          <Info className="w-3.5 h-3.5" />
          Changes take effect on the next cleanup cycle
        </p>
        <button
          onClick={handleSave}
          disabled={saving}
          className="btn btn-primary"
        >
          <Save className="w-4 h-4" />
          {saving ? 'Saving...' : 'Save'}
        </button>
      </div>
    </div>
  );
}
