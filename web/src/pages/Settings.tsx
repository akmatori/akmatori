import { useState, useEffect } from 'react';
import { Save, MessageSquare, Cpu, Power, PowerOff, Check, Info } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage, WarningMessage } from '../components/ErrorMessage';
import { slackSettingsApi, openaiSettingsApi } from '../api/client';
import type { SlackSettings, SlackSettingsUpdate, OpenAISettings, OpenAISettingsUpdate, OpenAIModel, ReasoningEffort } from '../types';

export default function Settings() {
  // Slack settings state
  const [settings, setSettings] = useState<SlackSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  // Slack form state
  const [botToken, setBotToken] = useState('');
  const [signingSecret, setSigningSecret] = useState('');
  const [appToken, setAppToken] = useState('');
  const [alertsChannel, setAlertsChannel] = useState('');
  const [enabled, setEnabled] = useState(false);

  // OpenAI settings state
  const [openaiSettings, setOpenaiSettings] = useState<OpenAISettings | null>(null);
  const [openaiLoading, setOpenaiLoading] = useState(true);
  const [openaiSaving, setOpenaiSaving] = useState(false);
  const [openaiError, setOpenaiError] = useState<string | null>(null);
  const [openaiSuccess, setOpenaiSuccess] = useState(false);

  // OpenAI form state
  const [apiKey, setApiKey] = useState('');
  const [model, setModel] = useState<OpenAIModel>('gpt-5.1-codex');
  const [reasoningEffort, setReasoningEffort] = useState<ReasoningEffort>('medium');

  useEffect(() => {
    loadSettings();
    loadOpenaiSettings();
  }, []);

  const loadSettings = async () => {
    try {
      setLoading(true);
      const data = await slackSettingsApi.get();
      setSettings(data);
      setAlertsChannel(data.alerts_channel || '');
      setEnabled(data.enabled);
      setError(null);
    } catch (err) {
      setError('Failed to load Slack settings');
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const loadOpenaiSettings = async () => {
    try {
      setOpenaiLoading(true);
      const data = await openaiSettingsApi.get();
      setOpenaiSettings(data);
      setModel(data.model);
      setReasoningEffort(data.model_reasoning_effort);
      setOpenaiError(null);
    } catch (err) {
      setOpenaiError('Failed to load OpenAI settings');
      console.error(err);
    } finally {
      setOpenaiLoading(false);
    }
  };

  const handleSave = async () => {
    try {
      setSaving(true);
      setError(null);
      setSuccess(false);

      const updates: SlackSettingsUpdate = {
        alerts_channel: alertsChannel,
        enabled,
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
      setSuccess(true);
      setTimeout(() => setSuccess(false), 3000);
    } catch (err) {
      setError('Failed to save settings');
      console.error(err);
    } finally {
      setSaving(false);
    }
  };

  const handleOpenaiSave = async () => {
    try {
      setOpenaiSaving(true);
      setOpenaiError(null);
      setOpenaiSuccess(false);

      const updates: OpenAISettingsUpdate = {
        model,
        model_reasoning_effort: reasoningEffort,
      };

      if (apiKey && !apiKey.startsWith('****')) {
        updates.api_key = apiKey;
      }

      const updated = await openaiSettingsApi.update(updates);
      setOpenaiSettings(updated);
      setApiKey('');
      setReasoningEffort(updated.model_reasoning_effort);
      setOpenaiSuccess(true);
      setTimeout(() => setOpenaiSuccess(false), 3000);
    } catch (err) {
      setOpenaiError('Failed to save OpenAI settings');
      console.error(err);
    } finally {
      setOpenaiSaving(false);
    }
  };

  const getValidReasoningEfforts = (selectedModel: OpenAIModel): ReasoningEffort[] => {
    const modelEfforts: Record<OpenAIModel, ReasoningEffort[]> = {
      'gpt-5.2': ['low', 'medium', 'high', 'extra_high'],
      'gpt-5.2-codex': ['low', 'medium', 'high', 'extra_high'],
      'gpt-5.1-codex-max': ['low', 'medium', 'high', 'extra_high'],
      'gpt-5.1-codex': ['low', 'medium', 'high'],
      'gpt-5.1-codex-mini': ['medium', 'high'],
      'gpt-5.1': ['low', 'medium', 'high'],
    };
    return modelEfforts[selectedModel] || ['medium'];
  };

  const handleModelChange = (newModel: OpenAIModel) => {
    setModel(newModel);
    const validEfforts = getValidReasoningEfforts(newModel);
    if (!validEfforts.includes(reasoningEffort)) {
      setReasoningEffort(validEfforts.includes('medium') ? 'medium' : validEfforts[0]);
    }
  };

  if (loading && openaiLoading) {
    return (
      <div>
        <PageHeader
          title="Settings"
          description="Configure system-wide settings and preferences"
        />
        <LoadingSpinner />
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title="Settings"
        description="Configure system-wide settings and integration preferences"
      />

      {/* Slack Integration Section */}
      <div className="card mb-8 animate-fade-in">
        {/* Section Header */}
        <div className="flex items-center justify-between mb-6 pb-4 border-b border-gray-200 dark:border-gray-700">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-lg bg-purple-50 dark:bg-purple-900/20">
              <MessageSquare className="w-5 h-5 text-purple-600 dark:text-purple-400" />
            </div>
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white">Slack Integration</h2>
          </div>
          <div className="flex items-center gap-2">
            <span className={`badge ${settings?.is_configured ? 'badge-success' : 'badge-warning'}`}>
              <Check className="w-3 h-3" />
              {settings?.is_configured ? 'Configured' : 'Not Configured'}
            </span>
            <span className={`badge ${settings?.enabled ? 'badge-success' : 'badge-default'}`}>
              {settings?.enabled ? 'Enabled' : 'Disabled'}
            </span>
          </div>
        </div>

        {error && <ErrorMessage message={error} />}
        {success && <SuccessMessage message="Slack settings saved successfully!" />}

        <p className="text-sm text-gray-600 dark:text-gray-400 mb-6">
          Configure Slack integration to receive alerts and interact with the bot via Slack.
          The system can work without Slack - you can use the API or UI to create incidents directly.
        </p>

        <div className="space-y-4">
          {/* Bot Token */}
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              Bot Token (xoxb-...)
            </label>
            <input
              type="password"
              value={botToken}
              onChange={(e) => setBotToken(e.target.value)}
              placeholder={settings?.bot_token || 'Enter Bot Token'}
              className="input-field"
            />
            {settings?.bot_token && (
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">Current: {settings.bot_token}</p>
            )}
          </div>

          {/* Signing Secret */}
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              Signing Secret
            </label>
            <input
              type="password"
              value={signingSecret}
              onChange={(e) => setSigningSecret(e.target.value)}
              placeholder={settings?.signing_secret || 'Enter Signing Secret'}
              className="input-field"
            />
            {settings?.signing_secret && (
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">Current: {settings.signing_secret}</p>
            )}
          </div>

          {/* App Token */}
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              App Token (xapp-...)
            </label>
            <input
              type="password"
              value={appToken}
              onChange={(e) => setAppToken(e.target.value)}
              placeholder={settings?.app_token || 'Enter App Token'}
              className="input-field"
            />
            {settings?.app_token && (
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">Current: {settings.app_token}</p>
            )}
          </div>

          {/* Alerts Channel */}
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              Alerts Channel (for Zabbix alerts)
            </label>
            <input
              type="text"
              value={alertsChannel}
              onChange={(e) => setAlertsChannel(e.target.value)}
              placeholder="e.g., #alerts or C01234567890"
              className="input-field"
            />
            <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
              Channel name (without #) or Channel ID where Zabbix alerts will be posted
            </p>
          </div>

          {/* Enabled Toggle */}
          <div className="flex items-center gap-3 p-4 rounded-lg bg-gray-50 dark:bg-gray-900/50">
            <input
              type="checkbox"
              id="slackEnabled"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
            />
            <label htmlFor="slackEnabled" className="flex items-center gap-2 cursor-pointer">
              {enabled ? (
                <Power className="w-4 h-4 text-green-500" />
              ) : (
                <PowerOff className="w-4 h-4 text-gray-400" />
              )}
              <span className="text-sm text-gray-700 dark:text-gray-300">
                Enable Slack Integration
              </span>
            </label>
          </div>

          {enabled && !settings?.is_configured && (
            <WarningMessage message="Please configure all three tokens (Bot Token, Signing Secret, App Token) to enable Slack integration." />
          )}
        </div>

        {/* Save Button */}
        <div className="flex items-center justify-between mt-6 pt-4 border-t border-gray-200 dark:border-gray-700">
          <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-2">
            <Info className="w-3 h-3" />
            Changes require a server restart to take effect
          </p>
          <button onClick={handleSave} disabled={saving} className="btn btn-primary">
            <Save className="w-4 h-4" />
            {saving ? 'Saving...' : 'Save Slack Settings'}
          </button>
        </div>
      </div>

      {/* OpenAI Configuration Section */}
      <div className="card animate-fade-in" style={{ animationDelay: '100ms' }}>
        {/* Section Header */}
        <div className="flex items-center justify-between mb-6 pb-4 border-b border-gray-200 dark:border-gray-700">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-lg bg-amber-50 dark:bg-amber-900/20">
              <Cpu className="w-5 h-5 text-amber-600 dark:text-amber-400" />
            </div>
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white">OpenAI Configuration</h2>
          </div>
          <span className={`badge ${openaiSettings?.is_configured ? 'badge-success' : 'badge-warning'}`}>
            <Check className="w-3 h-3" />
            {openaiSettings?.is_configured ? 'Configured' : 'Not Configured'}
          </span>
        </div>

        {openaiError && <ErrorMessage message={openaiError} />}
        {openaiSuccess && <SuccessMessage message="OpenAI settings saved successfully!" />}

        <p className="text-sm text-gray-600 dark:text-gray-400 mb-6">
          Configure the OpenAI API settings for Codex. Select the model and reasoning effort level.
        </p>

        {openaiLoading ? (
          <div className="py-8 text-center">
            <div className="inline-block w-8 h-8 rounded-full border-3 border-gray-200 dark:border-gray-700 border-t-primary-500 animate-spin" />
            <p className="mt-4 text-gray-500 dark:text-gray-400 text-sm">Loading...</p>
          </div>
        ) : (
          <div className="space-y-4">
            {/* API Key */}
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                API Key
              </label>
              <input
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder={openaiSettings?.api_key || 'Enter OpenAI API Key'}
                className="input-field"
              />
              {openaiSettings?.api_key && (
                <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">Current: {openaiSettings.api_key}</p>
              )}
            </div>

            {/* Model Selection */}
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Model
              </label>
              <select
                value={model}
                onChange={(e) => handleModelChange(e.target.value as OpenAIModel)}
                className="input-field"
              >
                <option value="gpt-5.2">gpt-5.2 (Latest, extra high reasoning)</option>
                <option value="gpt-5.2-codex">gpt-5.2-codex (Latest Codex, extra high reasoning)</option>
                <option value="gpt-5.1-codex-max">gpt-5.1-codex-max (Most capable, extra high reasoning)</option>
                <option value="gpt-5.1-codex">gpt-5.1-codex (Recommended)</option>
                <option value="gpt-5.1-codex-mini">gpt-5.1-codex-mini (Fast, limited reasoning)</option>
                <option value="gpt-5.1">gpt-5.1 (Standard)</option>
              </select>
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                Select the OpenAI model to use for AI tasks
              </p>
            </div>

            {/* Reasoning Effort */}
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Reasoning Effort
              </label>
              <select
                value={reasoningEffort}
                onChange={(e) => setReasoningEffort(e.target.value as ReasoningEffort)}
                className="input-field"
              >
                {getValidReasoningEfforts(model).map((effort) => (
                  <option key={effort} value={effort}>
                    {effort === 'extra_high' ? 'Extra High' : effort.charAt(0).toUpperCase() + effort.slice(1)}
                  </option>
                ))}
              </select>
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                Higher reasoning effort increases accuracy but uses more tokens and time
              </p>
            </div>

          </div>
        )}

        {/* Save Button */}
        <div className="flex items-center justify-between mt-6 pt-4 border-t border-gray-200 dark:border-gray-700">
          <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-2">
            <Info className="w-3 h-3" />
            Settings take effect immediately for new executions
          </p>
          <button
            onClick={handleOpenaiSave}
            disabled={openaiSaving || openaiLoading}
            className="btn btn-primary"
          >
            <Save className="w-4 h-4" />
            {openaiSaving ? 'Saving...' : 'Save OpenAI Settings'}
          </button>
        </div>
      </div>
    </div>
  );
}
