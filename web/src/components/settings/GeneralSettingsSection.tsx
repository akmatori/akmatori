import { useState, useEffect } from 'react';
import { Save, Info } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { SuccessMessage } from '../ErrorMessage';
import { generalSettingsApi } from '../../api/client';
import type { GeneralSettings as GeneralSettingsType } from '../../types';

interface GeneralSettingsSectionProps {
  onStatusChange?: (status: 'configured' | undefined) => void;
}

export default function GeneralSettingsSection({ onStatusChange }: GeneralSettingsSectionProps) {
  const [, setGeneralSettings] = useState<GeneralSettingsType | null>(null);
  const [generalLoading, setGeneralLoading] = useState(true);
  const [generalSaving, setGeneralSaving] = useState(false);
  const [generalError, setGeneralError] = useState<string | null>(null);
  const [generalSuccess, setGeneralSuccess] = useState(false);
  const [instanceBaseUrl, setInstanceBaseUrl] = useState('');

  // Alert correlation fields
  const [correlationEnabled, setCorrelationEnabled] = useState(false);
  const [monitorWindowMinutes, setMonitorWindowMinutes] = useState(60);

  useEffect(() => {
    loadGeneralSettings();
  }, []);

  const loadGeneralSettings = async () => {
    try {
      setGeneralLoading(true);
      const data = await generalSettingsApi.get();
      setGeneralSettings(data);
      setInstanceBaseUrl(data.base_url || '');
      setCorrelationEnabled(data.alert_correlation_enabled);
      setMonitorWindowMinutes(data.alert_monitor_window_minutes ?? 60);
      setGeneralError(null);
      onStatusChange?.(data.base_url ? 'configured' : undefined);
    } catch (err) {
      setGeneralError('Failed to load general settings');
      console.error(err);
    } finally {
      setGeneralLoading(false);
    }
  };

  const handleGeneralSave = async () => {
    try {
      setGeneralSaving(true);
      setGeneralError(null);
      setGeneralSuccess(false);

      const updated = await generalSettingsApi.update({
        base_url: instanceBaseUrl,
        alert_correlation_enabled: correlationEnabled,
        alert_monitor_window_minutes: monitorWindowMinutes,
      });
      setGeneralSettings(updated);
      onStatusChange?.(updated.base_url ? 'configured' : undefined);
      setGeneralSuccess(true);
      setTimeout(() => setGeneralSuccess(false), 3000);
    } catch (err) {
      setGeneralError(err instanceof Error ? err.message : 'Failed to save general settings');
      console.error(err);
    } finally {
      setGeneralSaving(false);
    }
  };

  if (generalLoading) {
    return <LoadingSpinner />;
  }

  return (
    <div className="space-y-5">
      {generalError && <ErrorMessage message={generalError} />}
      {generalSuccess && <SuccessMessage message="Settings saved" />}

      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
          Base URL
        </label>
        <input
          type="text"
          value={instanceBaseUrl}
          onChange={(e) => setInstanceBaseUrl(e.target.value)}
          placeholder="https://akmatori.example.com"
          className="input-field"
        />
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
          External URL for accessing this Akmatori instance. Used in Slack message links.
        </p>
      </div>

      {/* Alert Correlation */}
      <div className="border-t border-gray-200 dark:border-gray-700 pt-4">
        <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Alert Correlation</h3>
        <p className="text-xs text-gray-500 dark:text-gray-400 mb-3">
          Before spawning a new incident, ask an LLM whether the incoming alert is a recurrence of a recent one.
          Changes take effect on the next alert without a restart.
        </p>

        <div className="flex items-center gap-2 mb-4">
          <input
            id="correlation-enabled"
            type="checkbox"
            checked={correlationEnabled}
            onChange={(e) => setCorrelationEnabled(e.target.checked)}
            className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
          />
          <label htmlFor="correlation-enabled" className="text-sm text-gray-700 dark:text-gray-300">
            Enable alert correlation gate
          </label>
        </div>

        <div className="w-1/3">
          <label className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">
            Monitor window (minutes)
          </label>
          <input
            type="number"
            min={1}
            value={monitorWindowMinutes}
            onChange={(e) => setMonitorWindowMinutes(Number(e.target.value))}
            className="input-field text-sm"
          />
          <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
            How long a completed incident stays in monitor mode after resolution.
          </p>
        </div>
      </div>

      <div className="flex items-center justify-between pt-4 border-t border-gray-200 dark:border-gray-700">
        <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-1.5">
          <Info className="w-3.5 h-3.5" />
          Correlation changes take effect immediately
        </p>
        <button
          onClick={handleGeneralSave}
          disabled={generalSaving}
          className="btn btn-primary"
        >
          <Save className="w-4 h-4" />
          {generalSaving ? 'Saving...' : 'Save'}
        </button>
      </div>
    </div>
  );
}
