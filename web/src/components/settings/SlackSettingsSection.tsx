import { useState, useEffect } from 'react';
import { Save, Power, PowerOff, Info } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { SuccessMessage, WarningMessage } from '../ErrorMessage';
import { slackSettingsApi } from '../../api/client';
import type { SlackSettings, SlackSettingsUpdate } from '../../types';

interface SlackSettingsSectionProps {
  onStatusChange?: (status: 'configured' | 'not-configured' | 'disabled') => void;
}

export default function SlackSettingsSection({ onStatusChange }: SlackSettingsSectionProps) {
  const [settings, setSettings] = useState<SlackSettings | null>(null);
  const [slackLoading, setSlackLoading] = useState(true);
  const [slackSaving, setSlackSaving] = useState(false);
  const [slackError, setSlackError] = useState<string | null>(null);
  const [slackSuccess, setSlackSuccess] = useState(false);

  const [botToken, setBotToken] = useState('');
  const [signingSecret, setSigningSecret] = useState('');
  const [appToken, setAppToken] = useState('');
  const [alertsChannel, setAlertsChannel] = useState('');
  const [slackEnabled, setSlackEnabled] = useState(false);

  useEffect(() => {
    loadSlackSettings();
  }, []);

  const loadSlackSettings = async () => {
    try {
      setSlackLoading(true);
      const data = await slackSettingsApi.get();
      setSettings(data);
      setAlertsChannel(data.alerts_channel || '');
      setSlackEnabled(data.enabled);
      setSlackError(null);

      if (data.is_configured && data.enabled) {
        onStatusChange?.('configured');
      } else if (data.is_configured && !data.enabled) {
        onStatusChange?.('disabled');
      } else {
        onStatusChange?.('not-configured');
      }
    } catch (err) {
      setSlackError('Failed to load Slack settings');
      console.error(err);
    } finally {
      setSlackLoading(false);
    }
  };

  const handleSlackSave = async () => {
    try {
      setSlackSaving(true);
      setSlackError(null);
      setSlackSuccess(false);

      const updates: SlackSettingsUpdate = {
        alerts_channel: alertsChannel,
        enabled: slackEnabled,
      };

      if (botToken && !botToken.startsWith('****')) {
        updates.bot_token = botToken;
      }
      if (signingSecret && !signingSecret.startsWith('****')) {
        updates.signing_secret = signingSecret;
      }
      if (appToken && !appToken.startsWith('****')) {
        updates.app_token = appToken;
      }

      const updated = await slackSettingsApi.update(updates);
      setSettings(updated);
      setBotToken('');
      setSigningSecret('');
      setAppToken('');
      if (updated.is_configured && slackEnabled) {
        onStatusChange?.('configured');
      } else if (updated.is_configured && !slackEnabled) {
        onStatusChange?.('disabled');
      } else {
        onStatusChange?.('not-configured');
      }
      setSlackSuccess(true);
      setTimeout(() => setSlackSuccess(false), 3000);
    } catch (err) {
      setSlackError('Failed to save settings');
      console.error(err);
    } finally {
      setSlackSaving(false);
    }
  };

  if (slackLoading) {
    return <LoadingSpinner />;
  }

  return (
    <div className="space-y-5">
      {slackError && <ErrorMessage message={slackError} />}
      {slackSuccess && <SuccessMessage message="Settings saved" />}

      <p className="text-sm text-gray-600 dark:text-gray-400">
        Optional. The system works without Slack - you can use the dashboard to create incidents directly.
      </p>

      {/* Bot Token */}
      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
          Bot Token
        </label>
        <input
          type="password"
          value={botToken}
          onChange={(e) => setBotToken(e.target.value)}
          placeholder={settings?.bot_token || 'xoxb-...'}
          className="input-field"
        />
        {settings?.bot_token && (
          <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">Current: {settings.bot_token}</p>
        )}
      </div>

      {/* Signing Secret */}
      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
          Signing Secret
        </label>
        <input
          type="password"
          value={signingSecret}
          onChange={(e) => setSigningSecret(e.target.value)}
          placeholder={settings?.signing_secret || 'Enter signing secret'}
          className="input-field"
        />
        {settings?.signing_secret && (
          <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">Current: {settings.signing_secret}</p>
        )}
      </div>

      {/* App Token */}
      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
          App Token
        </label>
        <input
          type="password"
          value={appToken}
          onChange={(e) => setAppToken(e.target.value)}
          placeholder={settings?.app_token || 'xapp-...'}
          className="input-field"
        />
        {settings?.app_token && (
          <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">Current: {settings.app_token}</p>
        )}
      </div>

      {/* Alerts Channel */}
      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
          Alerts Channel
        </label>
        <input
          type="text"
          value={alertsChannel}
          onChange={(e) => setAlertsChannel(e.target.value)}
          placeholder="alerts"
          className="input-field"
        />
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
          Channel name (without #) or Channel ID
        </p>
      </div>

      {/* Enabled Toggle */}
      <div className="flex items-center gap-3 p-4 rounded-lg bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700">
        <input
          type="checkbox"
          id="slackEnabled"
          checked={slackEnabled}
          onChange={(e) => setSlackEnabled(e.target.checked)}
        />
        <label htmlFor="slackEnabled" className="flex items-center gap-2 cursor-pointer">
          {slackEnabled ? (
            <Power className="w-4 h-4 text-green-500" />
          ) : (
            <PowerOff className="w-4 h-4 text-gray-400" />
          )}
          <span className="text-sm text-gray-700 dark:text-gray-300">
            Enable Slack Integration
          </span>
        </label>
      </div>

      {slackEnabled && !settings?.is_configured && (
        <WarningMessage message="Configure all three tokens to enable Slack." />
      )}

      {/* Save Button */}
      <div className="flex items-center justify-between pt-4 border-t border-gray-200 dark:border-gray-700">
        <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-1.5">
          <Info className="w-3.5 h-3.5" />
          Requires server restart
        </p>
        <button onClick={handleSlackSave} disabled={slackSaving} className="btn btn-primary">
          <Save className="w-4 h-4" />
          {slackSaving ? 'Saving...' : 'Save'}
        </button>
      </div>
    </div>
  );
}
