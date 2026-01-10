import { useState, useEffect } from 'react';
import { Save, MessageSquare, Cpu, Power, PowerOff, Info, Bell, ChevronDown, ChevronRight, CheckCircle2, AlertTriangle } from 'lucide-react';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage, WarningMessage } from '../components/ErrorMessage';
import AlertSourcesManager from '../components/AlertSourcesManager';
import { slackSettingsApi, openaiSettingsApi } from '../api/client';
import type { SlackSettings, SlackSettingsUpdate, OpenAISettings, OpenAISettingsUpdate, OpenAIModel, ReasoningEffort } from '../types';

// Collapsible Section Component
function SettingsSection({
  title,
  description,
  icon: Icon,
  status,
  children,
  defaultExpanded = false,
}: {
  title: string;
  description: string;
  icon: React.ElementType;
  status?: 'configured' | 'not-configured' | 'disabled';
  children: React.ReactNode;
  defaultExpanded?: boolean;
}) {
  const [expanded, setExpanded] = useState(defaultExpanded);

  return (
    <div className="border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center justify-between p-5 hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors text-left"
      >
        <div className="flex items-center gap-4">
          <div className="p-2.5 rounded-lg bg-gray-100 dark:bg-gray-800">
            <Icon className="w-5 h-5 text-gray-600 dark:text-gray-400" />
          </div>
          <div>
            <h3 className="font-semibold text-gray-900 dark:text-white">{title}</h3>
            <p className="text-sm text-gray-500 dark:text-gray-400">{description}</p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          {status === 'configured' && (
            <span className="flex items-center gap-1.5 text-sm text-green-600 dark:text-green-400">
              <CheckCircle2 className="w-4 h-4" />
              Configured
            </span>
          )}
          {status === 'not-configured' && (
            <span className="flex items-center gap-1.5 text-sm text-amber-600 dark:text-amber-400">
              <AlertTriangle className="w-4 h-4" />
              Setup required
            </span>
          )}
          {status === 'disabled' && (
            <span className="text-sm text-gray-400 dark:text-gray-500">Disabled</span>
          )}
          {expanded ? (
            <ChevronDown className="w-5 h-5 text-gray-400" />
          ) : (
            <ChevronRight className="w-5 h-5 text-gray-400" />
          )}
        </div>
      </button>
      {expanded && (
        <div className="border-t border-gray-200 dark:border-gray-700 p-6 bg-gray-50/50 dark:bg-gray-900/30">
          {children}
        </div>
      )}
    </div>
  );
}

export default function Settings() {
  // Slack settings state
  const [settings, setSettings] = useState<SlackSettings | null>(null);
  const [slackLoading, setSlackLoading] = useState(true);
  const [slackSaving, setSlackSaving] = useState(false);
  const [slackError, setSlackError] = useState<string | null>(null);
  const [slackSuccess, setSlackSuccess] = useState(false);

  // Slack form state
  const [botToken, setBotToken] = useState('');
  const [signingSecret, setSigningSecret] = useState('');
  const [appToken, setAppToken] = useState('');
  const [alertsChannel, setAlertsChannel] = useState('');
  const [slackEnabled, setSlackEnabled] = useState(false);

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
  const [baseUrl, setBaseUrl] = useState('');
  const [proxyUrl, setProxyUrl] = useState('');
  const [noProxy, setNoProxy] = useState('');
  const [showAdvanced, setShowAdvanced] = useState(false);

  useEffect(() => {
    loadSlackSettings();
    loadOpenaiSettings();
  }, []);

  const loadSlackSettings = async () => {
    try {
      setSlackLoading(true);
      const data = await slackSettingsApi.get();
      setSettings(data);
      setAlertsChannel(data.alerts_channel || '');
      setSlackEnabled(data.enabled);
      setSlackError(null);
    } catch (err) {
      setSlackError('Failed to load Slack settings');
      console.error(err);
    } finally {
      setSlackLoading(false);
    }
  };

  const loadOpenaiSettings = async () => {
    try {
      setOpenaiLoading(true);
      const data = await openaiSettingsApi.get();
      setOpenaiSettings(data);
      setModel(data.model);
      setReasoningEffort(data.model_reasoning_effort);
      setBaseUrl(data.base_url || '');
      setProxyUrl(data.proxy_url || '');
      setNoProxy(data.no_proxy || '');
      // Auto-expand advanced if any advanced settings are configured
      if (data.base_url || data.proxy_url || data.no_proxy || data.model !== 'gpt-5.1-codex' || data.model_reasoning_effort !== 'medium') {
        setShowAdvanced(true);
      }
      setOpenaiError(null);
    } catch (err) {
      setOpenaiError('Failed to load OpenAI settings');
      console.error(err);
    } finally {
      setOpenaiLoading(false);
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
      setSlackSuccess(true);
      setTimeout(() => setSlackSuccess(false), 3000);
    } catch (err) {
      setSlackError('Failed to save settings');
      console.error(err);
    } finally {
      setSlackSaving(false);
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

      updates.base_url = baseUrl;
      updates.proxy_url = proxyUrl;
      updates.no_proxy = noProxy;

      const updated = await openaiSettingsApi.update(updates);
      setOpenaiSettings(updated);
      setApiKey('');
      setReasoningEffort(updated.model_reasoning_effort);
      setBaseUrl(updated.base_url || '');
      setProxyUrl(updated.proxy_url || '');
      setNoProxy(updated.no_proxy || '');
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

  // Determine OpenAI status
  const openaiStatus = openaiSettings?.is_configured ? 'configured' : 'not-configured';

  // Determine Slack status
  const slackStatus = !settings ? undefined :
    settings.is_configured && settings.enabled ? 'configured' :
    settings.is_configured && !settings.enabled ? 'disabled' : 'not-configured';

  return (
    <div className="animate-fade-in max-w-3xl mx-auto">
      {/* Page Header */}
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Settings</h1>
        <p className="mt-1 text-gray-600 dark:text-gray-400">
          Configure your Akmatori instance
        </p>
      </div>

      {/* Settings Sections */}
      <div className="space-y-4">
        {/* OpenAI Section - Most Important, Default Expanded */}
        <SettingsSection
          title="AI Configuration"
          description="OpenAI API settings for incident analysis"
          icon={Cpu}
          status={openaiStatus}
          defaultExpanded={!openaiSettings?.is_configured}
        >
          {openaiLoading ? (
            <LoadingSpinner />
          ) : (
            <div className="space-y-5">
              {openaiError && <ErrorMessage message={openaiError} />}
              {openaiSuccess && <SuccessMessage message="Settings saved" />}

              {/* API Key - Always Visible */}
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
                  OpenAI API Key <span className="text-red-500">*</span>
                </label>
                <input
                  type="password"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  placeholder={openaiSettings?.api_key || 'sk-...'}
                  className="input-field"
                />
                {openaiSettings?.api_key && (
                  <p className="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    Current: {openaiSettings.api_key}
                  </p>
                )}
              </div>

              {/* Advanced Settings Toggle */}
              <button
                type="button"
                onClick={() => setShowAdvanced(!showAdvanced)}
                className="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-200 transition-colors"
              >
                {showAdvanced ? (
                  <ChevronDown className="w-4 h-4" />
                ) : (
                  <ChevronRight className="w-4 h-4" />
                )}
                Advanced settings
                {(model !== 'gpt-5.1-codex' || reasoningEffort !== 'medium' || baseUrl || proxyUrl) && (
                  <span className="text-xs text-primary-600 dark:text-primary-400">(customized)</span>
                )}
              </button>

              {/* Advanced Settings */}
              {showAdvanced && (
                <div className="space-y-4 pl-4 border-l-2 border-gray-200 dark:border-gray-700">
                  {/* Model Selection */}
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
                      Model
                    </label>
                    <select
                      value={model}
                      onChange={(e) => handleModelChange(e.target.value as OpenAIModel)}
                      className="input-field"
                    >
                      <option value="gpt-5.1-codex">gpt-5.1-codex (Recommended)</option>
                      <option value="gpt-5.2-codex">gpt-5.2-codex (Latest)</option>
                      <option value="gpt-5.2">gpt-5.2</option>
                      <option value="gpt-5.1-codex-max">gpt-5.1-codex-max</option>
                      <option value="gpt-5.1-codex-mini">gpt-5.1-codex-mini (Fast)</option>
                      <option value="gpt-5.1">gpt-5.1</option>
                    </select>
                  </div>

                  {/* Reasoning Effort */}
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
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
                          {effort === 'medium' ? ' (Default)' : ''}
                        </option>
                      ))}
                    </select>
                  </div>

                  {/* Base URL */}
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
                      Custom Base URL
                    </label>
                    <input
                      type="text"
                      value={baseUrl}
                      onChange={(e) => setBaseUrl(e.target.value)}
                      placeholder="https://api.openai.com/v1 (default)"
                      className="input-field"
                    />
                    <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                      For Azure OpenAI, local LLMs, or API gateways
                    </p>
                  </div>

                  {/* Proxy URL */}
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
                      HTTP Proxy
                    </label>
                    <input
                      type="text"
                      value={proxyUrl}
                      onChange={(e) => setProxyUrl(e.target.value)}
                      placeholder="http://proxy:8080"
                      className="input-field"
                    />
                  </div>

                  {/* No Proxy */}
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
                      No Proxy
                    </label>
                    <input
                      type="text"
                      value={noProxy}
                      onChange={(e) => setNoProxy(e.target.value)}
                      placeholder="localhost,127.0.0.1"
                      className="input-field"
                    />
                  </div>
                </div>
              )}

              {/* Save Button */}
              <div className="flex items-center justify-between pt-4 border-t border-gray-200 dark:border-gray-700">
                <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-1.5">
                  <Info className="w-3.5 h-3.5" />
                  Takes effect immediately
                </p>
                <button
                  onClick={handleOpenaiSave}
                  disabled={openaiSaving}
                  className="btn btn-primary"
                >
                  <Save className="w-4 h-4" />
                  {openaiSaving ? 'Saving...' : 'Save'}
                </button>
              </div>
            </div>
          )}
        </SettingsSection>

        {/* Slack Section */}
        <SettingsSection
          title="Slack Integration"
          description="Receive alerts and interact via Slack"
          icon={MessageSquare}
          status={slackStatus}
        >
          {slackLoading ? (
            <LoadingSpinner />
          ) : (
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
          )}
        </SettingsSection>

        {/* Alert Sources Section */}
        <SettingsSection
          title="Alert Sources"
          description="Webhook integrations for monitoring systems"
          icon={Bell}
        >
          <AlertSourcesManager />
        </SettingsSection>
      </div>
    </div>
  );
}
