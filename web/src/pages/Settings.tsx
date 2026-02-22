import { useState, useEffect } from 'react';
import { Save, MessageSquare, Cpu, Power, PowerOff, Info, Bell, ChevronDown, ChevronRight, CheckCircle2, AlertTriangle, Globe, Layers } from 'lucide-react';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage, WarningMessage } from '../components/ErrorMessage';
import AlertSourcesManager from '../components/AlertSourcesManager';
import ProxySettings from '../components/ProxySettings';
import AggregationSettings from '../components/AggregationSettings';
import { slackSettingsApi, llmSettingsApi } from '../api/client';
import type { SlackSettings, SlackSettingsUpdate, LLMSettings, LLMSettingsUpdate, LLMProvider, ThinkingLevel } from '../types';

// Model suggestions per provider
const MODEL_SUGGESTIONS: Record<LLMProvider, { value: string; label: string }[]> = {
  openai: [
    { value: 'gpt-5.2-codex', label: 'gpt-5.2-codex (Recommended)' },
    { value: 'gpt-5.1-codex-max', label: 'gpt-5.1-codex-max' },
    { value: 'gpt-5.1-codex-mini', label: 'gpt-5.1-codex-mini (Fast)' },
  ],
  anthropic: [
    { value: 'claude-opus-4-6', label: 'claude-opus-4-6 (Most capable)' },
    { value: 'claude-sonnet-4-5', label: 'claude-sonnet-4-5 (Recommended)' },
    { value: 'claude-haiku-4-5', label: 'claude-haiku-4-5 (Fast)' },
  ],
  google: [
    { value: 'gemini-2.5-pro', label: 'gemini-2.5-pro (Recommended)' },
    { value: 'gemini-2.5-flash', label: 'gemini-2.5-flash (Fast)' },
  ],
  openrouter: [
    { value: 'anthropic/claude-sonnet-4-5', label: 'anthropic/claude-sonnet-4-5' },
    { value: 'openai/gpt-4o', label: 'openai/gpt-4o' },
    { value: 'google/gemini-2.5-pro', label: 'google/gemini-2.5-pro' },
  ],
  custom: [],
};

const THINKING_LEVELS: { value: ThinkingLevel; label: string }[] = [
  { value: 'off', label: 'Off' },
  { value: 'minimal', label: 'Minimal' },
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium (Default)' },
  { value: 'high', label: 'High' },
  { value: 'xhigh', label: 'Extra High' },
];

const PROVIDER_OPTIONS: { value: LLMProvider; label: string; description: string }[] = [
  { value: 'openai', label: 'OpenAI', description: 'GPT-5.2 Codex, GPT-5.1 Codex' },
  { value: 'anthropic', label: 'Anthropic', description: 'Claude Opus, Sonnet, Haiku' },
  { value: 'google', label: 'Google', description: 'Gemini 2.5 Pro, Flash' },
  { value: 'openrouter', label: 'OpenRouter', description: 'Multi-provider gateway' },
  { value: 'custom', label: 'Custom', description: 'Custom OpenAI-compatible endpoint' },
];

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

  // LLM settings state
  const [llmSettings, setLlmSettings] = useState<LLMSettings | null>(null);
  const [llmLoading, setLlmLoading] = useState(true);
  const [llmSaving, setLlmSaving] = useState(false);
  const [llmError, setLlmError] = useState<string | null>(null);
  const [llmSuccess, setLlmSuccess] = useState(false);

  // LLM form state
  const [provider, setProvider] = useState<LLMProvider>('openai');
  const [apiKey, setApiKey] = useState('');
  const [model, setModel] = useState('gpt-5.2-codex');
  const [thinkingLevel, setThinkingLevel] = useState<ThinkingLevel>('medium');
  const [baseUrl, setBaseUrl] = useState('');
  const [showAdvanced, setShowAdvanced] = useState(false);
  // Per-provider settings cache: stores unsaved edits so switching providers preserves input
  const [providerCache, setProviderCache] = useState<Record<string, { apiKey: string; model: string; thinkingLevel: ThinkingLevel; baseUrl: string }>>({});

  useEffect(() => {
    loadSlackSettings();
    loadLlmSettings();
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

  const loadLlmSettings = async () => {
    try {
      setLlmLoading(true);
      const data = await llmSettingsApi.get();
      setLlmSettings(data);

      // Build per-provider cache from API response
      const cache: Record<string, { apiKey: string; model: string; thinkingLevel: ThinkingLevel; baseUrl: string }> = {};
      if (data.providers) {
        for (const [p, settings] of Object.entries(data.providers)) {
          cache[p] = {
            apiKey: settings.is_configured ? settings.api_key : '',
            model: settings.model || '',
            thinkingLevel: settings.thinking_level || 'medium',
            baseUrl: settings.base_url || '',
          };
        }
      }
      setProviderCache(cache);

      // Set active provider form state
      const activeProvider = data.active_provider || data.provider || 'openai';
      setProvider(activeProvider);
      const activeSettings = cache[activeProvider];
      if (activeSettings) {
        setApiKey('');  // Don't pre-fill masked key
        setModel(activeSettings.model || 'gpt-5.2-codex');
        setThinkingLevel(activeSettings.thinkingLevel || 'medium');
        setBaseUrl(activeSettings.baseUrl || '');
      } else {
        setModel(data.model || 'gpt-5.2-codex');
        setThinkingLevel(data.thinking_level || 'medium');
        setBaseUrl(data.base_url || '');
      }
      // Auto-expand advanced if any advanced settings are configured
      if (data.base_url || data.thinking_level !== 'medium') {
        setShowAdvanced(true);
      }
      setLlmError(null);
    } catch (err) {
      setLlmError('Failed to load LLM settings');
      console.error(err);
    } finally {
      setLlmLoading(false);
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

  const handleLlmSave = async () => {
    try {
      setLlmSaving(true);
      setLlmError(null);
      setLlmSuccess(false);

      const updates: LLMSettingsUpdate = {
        provider,
        model,
        thinking_level: thinkingLevel,
        base_url: baseUrl,
      };

      if (apiKey && !apiKey.startsWith('****')) {
        updates.api_key = apiKey;
      }

      await llmSettingsApi.update(updates);

      // Reload all settings to refresh the cache
      await loadLlmSettings();
      // Restore the provider we just saved (loadLlmSettings sets the active one)
      setProvider(provider);
      setApiKey('');

      setLlmSuccess(true);
      setTimeout(() => setLlmSuccess(false), 3000);
    } catch (err) {
      setLlmError('Failed to save LLM settings');
      console.error(err);
    } finally {
      setLlmSaving(false);
    }
  };

  const handleProviderChange = (newProvider: LLMProvider) => {
    // Save current form state to cache before switching
    setProviderCache(prev => ({
      ...prev,
      [provider]: { apiKey, model, thinkingLevel, baseUrl },
    }));

    setProvider(newProvider);

    // Restore from cache if available, otherwise use defaults
    const cached = providerCache[newProvider];
    if (cached) {
      setApiKey(cached.apiKey || '');
      setModel(cached.model || MODEL_SUGGESTIONS[newProvider]?.[0]?.value || '');
      setThinkingLevel(cached.thinkingLevel || 'medium');
      setBaseUrl(cached.baseUrl || '');
    } else {
      setApiKey('');
      const suggestions = MODEL_SUGGESTIONS[newProvider];
      setModel(suggestions.length > 0 ? suggestions[0].value : '');
      setThinkingLevel('medium');
      setBaseUrl('');
    }
  };

  // Show base URL field for custom and openrouter providers
  const showBaseUrl = provider === 'custom' || provider === 'openrouter';

  // Get API key placeholder per provider
  const getApiKeyPlaceholder = (p: LLMProvider): string => {
    switch (p) {
      case 'openai': return 'sk-...';
      case 'anthropic': return 'sk-ant-...';
      case 'google': return 'AIza...';
      case 'openrouter': return 'sk-or-...';
      default: return 'Enter API key';
    }
  };

  // Determine LLM status
  const llmStatus = llmSettings?.is_configured ? 'configured' : 'not-configured';

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
        {/* LLM Provider Section - Most Important, Default Expanded */}
        <SettingsSection
          title="AI Configuration"
          description="LLM provider settings for incident analysis"
          icon={Cpu}
          status={llmStatus}
          defaultExpanded={!llmSettings?.is_configured}
        >
          {llmLoading ? (
            <LoadingSpinner />
          ) : (
            <div className="space-y-5">
              {llmError && <ErrorMessage message={llmError} />}
              {llmSuccess && <SuccessMessage message="Settings saved" />}

              {/* Provider Selection */}
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
                  LLM Provider
                </label>
                <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
                  {PROVIDER_OPTIONS.map((opt) => (
                    <button
                      key={opt.value}
                      type="button"
                      onClick={() => handleProviderChange(opt.value)}
                      className={`flex flex-col items-start p-3 rounded-lg border text-left transition-colors ${
                        provider === opt.value
                          ? 'bg-primary-50 dark:bg-primary-900/20 border-primary-300 dark:border-primary-700'
                          : 'bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-700'
                      }`}
                    >
                      <span className={`text-sm font-medium ${
                        provider === opt.value
                          ? 'text-primary-700 dark:text-primary-300'
                          : 'text-gray-900 dark:text-white'
                      }`}>
                        {opt.label}
                      </span>
                      <span className="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                        {opt.description}
                      </span>
                    </button>
                  ))}
                </div>
              </div>

              {/* API Key */}
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
                  API Key <span className="text-red-500">*</span>
                </label>
                <input
                  type="password"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  placeholder={llmSettings?.providers?.[provider]?.api_key || getApiKeyPlaceholder(provider)}
                  className="input-field"
                />
                {llmSettings?.providers?.[provider]?.is_configured && (
                  <p className="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    Current: {llmSettings.providers[provider].api_key}
                  </p>
                )}
              </div>

              {/* Model Selection */}
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
                  Model
                </label>
                {MODEL_SUGGESTIONS[provider].length > 0 ? (
                  <div>
                    <select
                      value={MODEL_SUGGESTIONS[provider].some(s => s.value === model) ? model : '__custom__'}
                      onChange={(e) => {
                        if (e.target.value === '__custom__') {
                          setModel('');
                        } else {
                          setModel(e.target.value);
                        }
                      }}
                      className="input-field"
                    >
                      {MODEL_SUGGESTIONS[provider].map((suggestion) => (
                        <option key={suggestion.value} value={suggestion.value}>
                          {suggestion.label}
                        </option>
                      ))}
                      {!MODEL_SUGGESTIONS[provider].some(s => s.value === model) && model && (
                        <option value="__custom__">Custom: {model}</option>
                      )}
                      <option value="__custom__">Other (enter manually)</option>
                    </select>
                    {(!MODEL_SUGGESTIONS[provider].some(s => s.value === model) || model === '') && (
                      <input
                        type="text"
                        value={model}
                        onChange={(e) => setModel(e.target.value)}
                        placeholder="Enter model name"
                        className="input-field mt-2"
                      />
                    )}
                  </div>
                ) : (
                  <input
                    type="text"
                    value={model}
                    onChange={(e) => setModel(e.target.value)}
                    placeholder="Enter model name"
                    className="input-field"
                  />
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
                {(thinkingLevel !== 'medium' || baseUrl) && (
                  <span className="text-xs text-primary-600 dark:text-primary-400">(customized)</span>
                )}
              </button>

              {/* Advanced Settings */}
              {showAdvanced && (
                <div className="space-y-4 pl-4 border-l-2 border-gray-200 dark:border-gray-700">
                  {/* Thinking Level */}
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
                      Thinking Level
                    </label>
                    <select
                      value={thinkingLevel}
                      onChange={(e) => setThinkingLevel(e.target.value as ThinkingLevel)}
                      className="input-field"
                    >
                      {THINKING_LEVELS.map((level) => (
                        <option key={level.value} value={level.value}>
                          {level.label}
                        </option>
                      ))}
                    </select>
                    <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                      Controls how much the model reasons before responding
                    </p>
                  </div>

                  {/* Base URL - shown for custom/openrouter or always in advanced */}
                  {showBaseUrl && (
                    <div>
                      <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
                        Base URL
                      </label>
                      <input
                        type="text"
                        value={baseUrl}
                        onChange={(e) => setBaseUrl(e.target.value)}
                        placeholder={provider === 'openrouter' ? 'https://openrouter.ai/api/v1' : 'https://your-endpoint.example.com/v1'}
                        className="input-field"
                      />
                      <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                        {provider === 'openrouter'
                          ? 'OpenRouter API endpoint (defaults to https://openrouter.ai/api/v1)'
                          : 'Custom OpenAI-compatible API endpoint'}
                      </p>
                    </div>
                  )}
                </div>
              )}

              {/* Save Button */}
              <div className="flex items-center justify-between pt-4 border-t border-gray-200 dark:border-gray-700">
                <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-1.5">
                  <Info className="w-3.5 h-3.5" />
                  Takes effect immediately
                </p>
                <button
                  onClick={handleLlmSave}
                  disabled={llmSaving}
                  className="btn btn-primary"
                >
                  <Save className="w-4 h-4" />
                  {llmSaving ? 'Saving...' : 'Save'}
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

        {/* Alert Aggregation Settings */}
        <SettingsSection
          title="Alert Aggregation"
          description="Automatically group related alerts into incidents"
          icon={Layers}
          defaultExpanded={false}
        >
          <AggregationSettings />
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
