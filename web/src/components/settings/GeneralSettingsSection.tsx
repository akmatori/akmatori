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

  useEffect(() => {
    loadGeneralSettings();
  }, []);

  const loadGeneralSettings = async () => {
    try {
      setGeneralLoading(true);
      const data = await generalSettingsApi.get();
      setGeneralSettings(data);
      setInstanceBaseUrl(data.base_url || '');
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

      const updated = await generalSettingsApi.update({ base_url: instanceBaseUrl });
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

      <div className="flex items-center justify-between pt-4 border-t border-gray-200 dark:border-gray-700">
        <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-1.5">
          <Info className="w-3.5 h-3.5" />
          Takes effect on next investigation
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
