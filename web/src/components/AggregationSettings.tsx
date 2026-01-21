import { useState, useEffect } from 'react';
import { Save, Layers } from 'lucide-react';
import LoadingSpinner from './LoadingSpinner';
import ErrorMessage, { SuccessMessage } from './ErrorMessage';
import { aggregationSettingsApi } from '../api/client';
import type { AggregationSettings as AggregationSettingsType } from '../types';

export default function AggregationSettings() {
  const [settings, setSettings] = useState<AggregationSettingsType | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  useEffect(() => {
    loadSettings();
  }, []);

  const loadSettings = async () => {
    try {
      setLoading(true);
      const data = await aggregationSettingsApi.get();
      setSettings(data);
      setError(null);
    } catch (err) {
      setError('Failed to load aggregation settings');
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    if (!settings) return;
    try {
      setSaving(true);
      setError(null);
      await aggregationSettingsApi.update(settings);
      setSuccess(true);
      setTimeout(() => setSuccess(false), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save settings');
    } finally {
      setSaving(false);
    }
  };

  const updateSetting = <K extends keyof AggregationSettingsType>(
    key: K,
    value: AggregationSettingsType[K]
  ) => {
    if (settings) {
      setSettings({ ...settings, [key]: value });
    }
  };

  if (loading) {
    return <LoadingSpinner />;
  }

  if (!settings) {
    return <ErrorMessage message="Failed to load aggregation settings" />;
  }

  return (
    <div className="space-y-6">
      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message="Aggregation settings saved successfully" />}

      {/* Header with Enable Toggle */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="p-2 rounded-lg bg-purple-100 dark:bg-purple-900/30">
            <Layers className="w-5 h-5 text-purple-600 dark:text-purple-400" />
          </div>
          <div>
            <h3 className="text-lg font-medium text-gray-900 dark:text-white">Alert Aggregation</h3>
            <p className="text-sm text-gray-500 dark:text-gray-400">
              Automatically group related alerts into incidents
            </p>
          </div>
        </div>
        <label className="flex items-center gap-2 cursor-pointer">
          <span className="text-sm text-gray-600 dark:text-gray-400">
            {settings.enabled ? 'Enabled' : 'Disabled'}
          </span>
          <input
            type="checkbox"
            checked={settings.enabled}
            onChange={(e) => updateSetting('enabled', e.target.checked)}
            className="w-5 h-5 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
          />
        </label>
      </div>

      {/* Correlation Thresholds */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
            Correlation Confidence Threshold
          </label>
          <input
            type="number"
            min="0"
            max="1"
            step="0.05"
            value={settings.correlation_confidence_threshold}
            onChange={(e) => updateSetting('correlation_confidence_threshold', parseFloat(e.target.value))}
            className="w-full px-4 py-2.5 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
          <p className="mt-1.5 text-sm text-gray-500 dark:text-gray-400">
            Minimum confidence (0-1) to attach alert to incident
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
            Merge Confidence Threshold
          </label>
          <input
            type="number"
            min="0"
            max="1"
            step="0.05"
            value={settings.merge_confidence_threshold}
            onChange={(e) => updateSetting('merge_confidence_threshold', parseFloat(e.target.value))}
            className="w-full px-4 py-2.5 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
          <p className="mt-1.5 text-sm text-gray-500 dark:text-gray-400">
            Minimum confidence (0-1) to auto-merge incidents
          </p>
        </div>
      </div>

      {/* Timing Settings */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
            Re-correlation Interval (minutes)
          </label>
          <input
            type="number"
            min="1"
            max="30"
            value={settings.recorrelation_interval_minutes}
            onChange={(e) => updateSetting('recorrelation_interval_minutes', parseInt(e.target.value) || 1)}
            className="w-full px-4 py-2.5 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
          <p className="mt-1.5 text-sm text-gray-500 dark:text-gray-400">
            How often to re-analyze unattached alerts (1-30 min)
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
            Observing Duration (minutes)
          </label>
          <input
            type="number"
            min="5"
            max="120"
            value={settings.observing_duration_minutes}
            onChange={(e) => updateSetting('observing_duration_minutes', parseInt(e.target.value) || 5)}
            className="w-full px-4 py-2.5 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
          <p className="mt-1.5 text-sm text-gray-500 dark:text-gray-400">
            Wait time after alerts resolve before closing incident (5-120 min)
          </p>
        </div>
      </div>

      {/* Advanced Settings */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
            Max Incidents to Analyze
          </label>
          <input
            type="number"
            min="1"
            max="50"
            value={settings.max_incidents_to_analyze}
            onChange={(e) => updateSetting('max_incidents_to_analyze', parseInt(e.target.value) || 1)}
            className="w-full px-4 py-2.5 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
          <p className="mt-1.5 text-sm text-gray-500 dark:text-gray-400">
            Maximum active incidents to consider for correlation
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
            Correlator Timeout (seconds)
          </label>
          <input
            type="number"
            min="5"
            max="120"
            value={settings.correlator_timeout_seconds}
            onChange={(e) => updateSetting('correlator_timeout_seconds', parseInt(e.target.value) || 5)}
            className="w-full px-4 py-2.5 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
          <p className="mt-1.5 text-sm text-gray-500 dark:text-gray-400">
            Timeout for LLM correlation analysis
          </p>
        </div>
      </div>

      {/* Re-correlation Toggle */}
      <div>
        <label className="flex items-center justify-between p-3 rounded-lg border border-gray-200 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800/50 cursor-pointer">
          <div>
            <div className="font-medium text-gray-900 dark:text-white">
              Background Re-correlation
            </div>
            <div className="text-sm text-gray-500 dark:text-gray-400">
              Periodically re-analyze unattached alerts to find matching incidents
            </div>
          </div>
          <input
            type="checkbox"
            checked={settings.recorrelation_enabled}
            onChange={(e) => updateSetting('recorrelation_enabled', e.target.checked)}
            className="w-5 h-5 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
          />
        </label>
      </div>

      {/* Save Button */}
      <div className="flex justify-end pt-4">
        <button
          onClick={handleSave}
          disabled={saving}
          className="flex items-center gap-2 px-6 py-2.5 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-400 text-white font-medium rounded-lg transition-colors"
        >
          {saving ? (
            <div className="w-5 h-5 border-2 border-white border-t-transparent rounded-full animate-spin" />
          ) : (
            <Save className="w-5 h-5" />
          )}
          Save Settings
        </button>
      </div>
    </div>
  );
}
