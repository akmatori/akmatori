import { useState, useEffect } from 'react';
import { Save, Info, AlertTriangle } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { SuccessMessage } from '../ErrorMessage';
import { generalSettingsApi, recurrenceStatsApi } from '../../api/client';
import type { GeneralSettings as GeneralSettingsType, RecurrenceStats } from '../../types';

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
  const [correlationWindowMinutes, setCorrelationWindowMinutes] = useState(30);
  const [correlationThreshold, setCorrelationThreshold] = useState(0.7);
  const [correlationMaxCandidates, setCorrelationMaxCandidates] = useState(20);
  const [correlationFingerprintWindowMinutes, setCorrelationFingerprintWindowMinutes] = useState(1440);

  // Alert suppression fields
  const [suppressionEnabled, setSuppressionEnabled] = useState(false);
  const [suppressionThreshold, setSuppressionThreshold] = useState(0.7);

  // Recurrence stats for warning badges
  const [recurrenceStats, setRecurrenceStats] = useState<RecurrenceStats | null>(null);

  useEffect(() => {
    loadGeneralSettings();
    loadRecurrenceStats();
  }, []);

  const loadRecurrenceStats = async () => {
    try {
      const stats = await recurrenceStatsApi.get();
      setRecurrenceStats(stats);
    } catch {
      // Stats are best-effort; don't block the settings form on failure.
    }
  };

  const loadGeneralSettings = async () => {
    try {
      setGeneralLoading(true);
      const data = await generalSettingsApi.get();
      setGeneralSettings(data);
      setInstanceBaseUrl(data.base_url || '');
      setCorrelationEnabled(data.alert_correlation_enabled);
      setCorrelationWindowMinutes(data.alert_correlation_window_minutes);
      setCorrelationThreshold(data.alert_correlation_threshold);
      setCorrelationMaxCandidates(data.alert_correlation_max_candidates);
      setCorrelationFingerprintWindowMinutes(data.alert_correlation_fingerprint_window_minutes);
      setSuppressionEnabled(data.alert_suppression_enabled);
      setSuppressionThreshold(data.alert_suppression_threshold);
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
        alert_correlation_window_minutes: correlationWindowMinutes,
        alert_correlation_threshold: correlationThreshold,
        alert_correlation_max_candidates: correlationMaxCandidates,
        alert_correlation_fingerprint_window_minutes: correlationFingerprintWindowMinutes,
        alert_suppression_enabled: suppressionEnabled,
        alert_suppression_threshold: suppressionThreshold,
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

        <div className="flex items-center gap-2 mb-3">
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
          {!correlationEnabled && recurrenceStats && recurrenceStats.redundancy_rate_24h > 0.2 && (
            <span className="flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400 font-medium">
              <AlertTriangle className="w-3.5 h-3.5" />
              {Math.round(recurrenceStats.redundancy_rate_24h * 100)}% redundancy in last 24h
            </span>
          )}
        </div>

        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <div>
            <label className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">
              Window (minutes)
            </label>
            <input
              type="number"
              min={1}
              max={1440}
              value={correlationWindowMinutes}
              onChange={(e) => setCorrelationWindowMinutes(Number(e.target.value))}
              disabled={!correlationEnabled}
              className="input-field text-sm disabled:opacity-50"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">
              Fingerprint window (minutes)
            </label>
            <input
              type="number"
              min={1}
              max={10080}
              value={correlationFingerprintWindowMinutes}
              onChange={(e) => setCorrelationFingerprintWindowMinutes(Number(e.target.value))}
              disabled={!correlationEnabled}
              className="input-field text-sm disabled:opacity-50"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">
              Threshold (0–1)
            </label>
            <input
              type="number"
              min={0.01}
              max={1}
              step={0.01}
              value={correlationThreshold}
              onChange={(e) => setCorrelationThreshold(Number(e.target.value))}
              disabled={!correlationEnabled}
              className="input-field text-sm disabled:opacity-50"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">
              Max candidates
            </label>
            <input
              type="number"
              min={1}
              max={100}
              value={correlationMaxCandidates}
              onChange={(e) => setCorrelationMaxCandidates(Number(e.target.value))}
              disabled={!correlationEnabled}
              className="input-field text-sm disabled:opacity-50"
            />
          </div>
        </div>
      </div>

      {/* Alert Suppression */}
      <div className="border-t border-gray-200 dark:border-gray-700 pt-4">
        <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">Alert Suppression</h3>
        <p className="text-xs text-gray-500 dark:text-gray-400 mb-3">
          Suppress known false-positive alerts matched against memory entries flagged with{' '}
          <code className="font-mono">suppress: true</code>. Changes take effect on the next alert without a restart.
        </p>

        <div className="flex items-center gap-2 mb-3">
          <input
            id="suppression-enabled"
            type="checkbox"
            checked={suppressionEnabled}
            onChange={(e) => setSuppressionEnabled(e.target.checked)}
            className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
          />
          <label htmlFor="suppression-enabled" className="text-sm text-gray-700 dark:text-gray-300">
            Enable alert suppression gate
          </label>
          {!suppressionEnabled && recurrenceStats && recurrenceStats.redundancy_rate_24h > 0.2 && (
            <span className="flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400 font-medium">
              <AlertTriangle className="w-3.5 h-3.5" />
              {Math.round(recurrenceStats.redundancy_rate_24h * 100)}% redundancy in last 24h
            </span>
          )}
        </div>

        <div className="w-1/3">
          <label className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">
            Threshold (0–1)
          </label>
          <input
            type="number"
            min={0.01}
            max={1}
            step={0.01}
            value={suppressionThreshold}
            onChange={(e) => setSuppressionThreshold(Number(e.target.value))}
            disabled={!suppressionEnabled}
            className="input-field text-sm disabled:opacity-50"
          />
        </div>
      </div>

      <div className="flex items-center justify-between pt-4 border-t border-gray-200 dark:border-gray-700">
        <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-1.5">
          <Info className="w-3.5 h-3.5" />
          Correlation and suppression changes take effect immediately
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
