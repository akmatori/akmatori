import { useState, useEffect, useCallback } from 'react';
import { Save, MessageSquare, Cpu, Power, PowerOff, Info, Bell, ChevronDown, ChevronRight, CheckCircle2, AlertTriangle, LogIn, LogOut, Key, Users, ExternalLink, Copy, Loader2, Globe } from 'lucide-react';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage, WarningMessage } from '../components/ErrorMessage';
import AlertSourcesManager from '../components/AlertSourcesManager';
import ProxySettings from '../components/ProxySettings';
import { slackSettingsApi, openaiSettingsApi } from '../api/client';
import type { SlackSettings, SlackSettingsUpdate, OpenAISettings, OpenAISettingsUpdate, OpenAIModel, ReasoningEffort, AuthMethod, DeviceAuthStartResponse } from '../types';

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
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [authMethod, setAuthMethod] = useState<AuthMethod>('api_key');

  // Device auth modal state
  const [showDeviceAuthModal, setShowDeviceAuthModal] = useState(false);
  const [deviceAuthData, setDeviceAuthData] = useState<DeviceAuthStartResponse | null>(null);
  const [deviceAuthLoading, setDeviceAuthLoading] = useState(false);
  const [deviceAuthError, setDeviceAuthError] = useState<string | null>(null);
  const [codeCopied, setCodeCopied] = useState(false);

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
      setAuthMethod(data.auth_method || 'api_key');
      // Auto-expand advanced if any advanced settings are configured
      if (data.base_url || data.model !== 'gpt-5.1-codex' || data.model_reasoning_effort !== 'medium') {
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

  // Device Auth functions
  const handleStartDeviceAuth = async () => {
    try {
      setDeviceAuthLoading(true);
      setDeviceAuthError(null);
      const data = await openaiSettingsApi.startDeviceAuth();
      setDeviceAuthData(data);
      setShowDeviceAuthModal(true);
      // Start polling for completion
      pollDeviceAuthStatus(data.device_code);
    } catch (err) {
      setDeviceAuthError(err instanceof Error ? err.message : 'Failed to start device authentication');
    } finally {
      setDeviceAuthLoading(false);
    }
  };

  const pollDeviceAuthStatus = useCallback(async (deviceCode: string) => {
    const pollInterval = setInterval(async () => {
      try {
        const status = await openaiSettingsApi.getDeviceAuthStatus(deviceCode);
        if (status.status === 'complete') {
          clearInterval(pollInterval);
          setShowDeviceAuthModal(false);
          setDeviceAuthData(null);
          setOpenaiSuccess(true);
          setTimeout(() => setOpenaiSuccess(false), 3000);
          // Reload settings to get the new ChatGPT connection status
          loadOpenaiSettings();
        } else if (status.status === 'expired' || status.status === 'failed') {
          clearInterval(pollInterval);
          setDeviceAuthError(status.error || 'Authentication failed or expired');
        }
      } catch (err) {
        console.error('Failed to poll device auth status:', err);
      }
    }, 3000); // Poll every 3 seconds

    // Stop polling after 20 minutes
    setTimeout(() => {
      clearInterval(pollInterval);
    }, 20 * 60 * 1000);
  }, []);

  const handleCancelDeviceAuth = async () => {
    try {
      await openaiSettingsApi.cancelDeviceAuth();
    } catch (err) {
      console.error('Failed to cancel device auth:', err);
    } finally {
      setShowDeviceAuthModal(false);
      setDeviceAuthData(null);
      setDeviceAuthError(null);
    }
  };

  const handleDisconnectChatGPT = async () => {
    if (!confirm('Are you sure you want to disconnect your ChatGPT subscription?')) {
      return;
    }
    try {
      setOpenaiSaving(true);
      await openaiSettingsApi.disconnectChatGPT();
      setOpenaiSuccess(true);
      setTimeout(() => setOpenaiSuccess(false), 3000);
      loadOpenaiSettings();
    } catch (err) {
      setOpenaiError(err instanceof Error ? err.message : 'Failed to disconnect');
    } finally {
      setOpenaiSaving(false);
    }
  };

  const handleCopyCode = () => {
    if (deviceAuthData?.user_code) {
      navigator.clipboard.writeText(deviceAuthData.user_code);
      setCodeCopied(true);
      setTimeout(() => setCodeCopied(false), 2000);
    }
  };

  const handleAuthMethodChange = async (newMethod: AuthMethod) => {
    setAuthMethod(newMethod);
    try {
      await openaiSettingsApi.update({ auth_method: newMethod });
      loadOpenaiSettings();
    } catch (err) {
      setOpenaiError(err instanceof Error ? err.message : 'Failed to update auth method');
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

      const updated = await openaiSettingsApi.update(updates);
      setOpenaiSettings(updated);
      setApiKey('');
      setReasoningEffort(updated.model_reasoning_effort);
      setBaseUrl(updated.base_url || '');
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
              {deviceAuthError && <ErrorMessage message={deviceAuthError} />}

              {/* Authentication Method Toggle */}
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
                  Authentication Method
                </label>
                <div className="flex rounded-lg border border-gray-200 dark:border-gray-700 overflow-hidden">
                  <button
                    type="button"
                    onClick={() => handleAuthMethodChange('api_key')}
                    className={`flex-1 flex items-center justify-center gap-2 px-4 py-2.5 text-sm font-medium transition-colors ${
                      authMethod === 'api_key'
                        ? 'bg-primary-600 text-white'
                        : 'bg-white dark:bg-gray-800 text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-700'
                    }`}
                  >
                    <Key className="w-4 h-4" />
                    API Key
                  </button>
                  <button
                    type="button"
                    onClick={() => handleAuthMethodChange('chatgpt_subscription')}
                    className={`flex-1 flex items-center justify-center gap-2 px-4 py-2.5 text-sm font-medium transition-colors border-l border-gray-200 dark:border-gray-700 ${
                      authMethod === 'chatgpt_subscription'
                        ? 'bg-primary-600 text-white'
                        : 'bg-white dark:bg-gray-800 text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-700'
                    }`}
                  >
                    <Users className="w-4 h-4" />
                    ChatGPT Subscription
                  </button>
                </div>
              </div>

              {/* API Key Auth Section */}
              {authMethod === 'api_key' && (
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
              )}

              {/* ChatGPT Subscription Auth Section */}
              {authMethod === 'chatgpt_subscription' && (
                <div className="space-y-4">
                  {openaiSettings?.chatgpt_connected ? (
                    <div className="p-4 rounded-lg bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-3">
                          <CheckCircle2 className="w-5 h-5 text-green-600 dark:text-green-400" />
                          <div>
                            <p className="font-medium text-green-700 dark:text-green-300">
                              Connected as {openaiSettings.chatgpt_email || 'ChatGPT User'}
                            </p>
                            {openaiSettings.chatgpt_expires_at && (
                              <p className="text-sm text-green-600 dark:text-green-400">
                                Token expires: {new Date(openaiSettings.chatgpt_expires_at).toLocaleDateString()}
                              </p>
                            )}
                            {openaiSettings.chatgpt_expired && (
                              <p className="text-sm text-amber-600 dark:text-amber-400">
                                Session expired - please reconnect
                              </p>
                            )}
                          </div>
                        </div>
                        <button
                          type="button"
                          onClick={handleDisconnectChatGPT}
                          disabled={openaiSaving}
                          className="btn btn-secondary text-sm"
                        >
                          <LogOut className="w-4 h-4" />
                          Disconnect
                        </button>
                      </div>
                    </div>
                  ) : (
                    <div className="p-4 rounded-lg bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700">
                      <div className="text-center space-y-3">
                        <p className="text-sm text-gray-600 dark:text-gray-400">
                          Use your ChatGPT Plus or Team subscription instead of API credits.
                        </p>
                        <button
                          type="button"
                          onClick={handleStartDeviceAuth}
                          disabled={deviceAuthLoading}
                          className="btn btn-primary"
                        >
                          {deviceAuthLoading ? (
                            <Loader2 className="w-4 h-4 animate-spin" />
                          ) : (
                            <LogIn className="w-4 h-4" />
                          )}
                          {deviceAuthLoading ? 'Starting...' : 'Login with ChatGPT'}
                        </button>
                      </div>
                    </div>
                  )}
                </div>
              )}

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
                {(model !== 'gpt-5.1-codex' || reasoningEffort !== 'medium' || baseUrl) && (
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

        {/* Proxy Settings */}
        <SettingsSection
          title="Proxy"
          description="HTTP proxy configuration for outbound connections"
          icon={Globe}
          defaultExpanded={false}
        >
          <ProxySettings />
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

      {/* Device Auth Modal */}
      {showDeviceAuthModal && deviceAuthData && (
        <div className="fixed inset-0 z-50 overflow-y-auto">
          <div className="flex min-h-screen items-center justify-center p-4">
            {/* Backdrop */}
            <div
              className="fixed inset-0 bg-black/50 transition-opacity"
              onClick={handleCancelDeviceAuth}
            />

            {/* Modal */}
            <div className="relative bg-white dark:bg-gray-800 rounded-xl shadow-2xl max-w-md w-full p-6 z-10">
              <h3 className="text-lg font-semibold text-gray-900 dark:text-white mb-4 text-center">
                Login with ChatGPT
              </h3>

              <div className="space-y-6">
                {/* Step 1: Enable device code authorization */}
                <div className="text-center">
                  <p className="text-sm text-gray-600 dark:text-gray-400 mb-3">
                    1. First, enable device code authorization in your ChatGPT settings:
                  </p>
                  <a
                    href="https://chatgpt.com/#settings/Security"
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-2 text-primary-600 dark:text-primary-400 hover:underline font-medium"
                  >
                    chatgpt.com → Settings → Security
                    <ExternalLink className="w-4 h-4" />
                  </a>
                  <p className="text-xs text-gray-500 dark:text-gray-400 mt-2">
                    Turn on "Enable device code authorization for Codex"
                  </p>
                </div>

                {/* Step 2: Open URL */}
                <div className="text-center">
                  <p className="text-sm text-gray-600 dark:text-gray-400 mb-3">
                    2. Open this URL in your browser:
                  </p>
                  <a
                    href={deviceAuthData.verification_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-2 text-primary-600 dark:text-primary-400 hover:underline font-medium"
                  >
                    {deviceAuthData.verification_url}
                    <ExternalLink className="w-4 h-4" />
                  </a>
                </div>

                {/* Step 3: Enter Code */}
                <div className="text-center">
                  <p className="text-sm text-gray-600 dark:text-gray-400 mb-3">
                    3. Enter this code:
                  </p>
                  <div className="relative inline-block">
                    <div className="bg-gray-100 dark:bg-gray-700 rounded-lg px-6 py-4 font-mono text-2xl font-bold tracking-widest text-gray-900 dark:text-white">
                      {deviceAuthData.user_code}
                    </div>
                    <button
                      type="button"
                      onClick={handleCopyCode}
                      className="absolute -right-2 -top-2 p-2 bg-white dark:bg-gray-600 rounded-full shadow-md hover:bg-gray-50 dark:hover:bg-gray-500 transition-colors"
                      title="Copy code"
                    >
                      {codeCopied ? (
                        <CheckCircle2 className="w-4 h-4 text-green-500" />
                      ) : (
                        <Copy className="w-4 h-4 text-gray-500 dark:text-gray-400" />
                      )}
                    </button>
                  </div>
                  {codeCopied && (
                    <p className="text-sm text-green-600 dark:text-green-400 mt-2">
                      Code copied!
                    </p>
                  )}
                </div>

                {/* Waiting indicator */}
                <div className="flex items-center justify-center gap-3 py-4 border-t border-gray-200 dark:border-gray-700">
                  <Loader2 className="w-5 h-5 animate-spin text-primary-600 dark:text-primary-400" />
                  <span className="text-sm text-gray-600 dark:text-gray-400">
                    Waiting for authentication...
                  </span>
                </div>

                {/* Error message */}
                {deviceAuthError && (
                  <ErrorMessage message={deviceAuthError} />
                )}

                {/* Cancel button */}
                <div className="flex justify-center">
                  <button
                    type="button"
                    onClick={handleCancelDeviceAuth}
                    className="btn btn-secondary"
                  >
                    Cancel
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
