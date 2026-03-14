import { useState, useEffect } from 'react';
import { Save, Info, ChevronDown, ChevronRight } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { SuccessMessage } from '../ErrorMessage';
import { llmSettingsApi } from '../../api/client';
import type { LLMSettings, LLMSettingsUpdate, LLMProvider, ThinkingLevel } from '../../types';

const MODEL_SUGGESTIONS: Record<LLMProvider, { value: string; label: string }[]> = {
  openai: [
    { value: 'gpt-5.4', label: 'gpt-5.4 (Recommended)' },
    { value: 'gpt-5.3-codex', label: 'gpt-5.3-codex' },
    { value: 'gpt-5.2-codex', label: 'gpt-5.2-codex' },
  ],
  anthropic: [
    { value: 'claude-opus-4-6', label: 'claude-opus-4-6 (Most capable)' },
    { value: 'claude-sonnet-4-6', label: 'claude-sonnet-4-6 (Recommended)' },
    { value: 'claude-haiku-4-5', label: 'claude-haiku-4-5 (Fast)' },
  ],
  google: [
    { value: 'gemini-2.5-pro', label: 'gemini-2.5-pro (Recommended)' },
    { value: 'gemini-2.5-flash', label: 'gemini-2.5-flash (Fast)' },
  ],
  openrouter: [
    { value: 'anthropic/claude-sonnet-4-6', label: 'anthropic/claude-sonnet-4-6' },
    { value: 'openai/gpt-5.4', label: 'openai/gpt-5.4' },
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
  { value: 'openai', label: 'OpenAI', description: 'GPT-5.4, GPT-5.3 Codex' },
  { value: 'anthropic', label: 'Anthropic', description: 'Claude Opus 4.6, Sonnet 4.6, Haiku' },
  { value: 'google', label: 'Google', description: 'Gemini 2.5 Pro, Flash' },
  { value: 'openrouter', label: 'OpenRouter', description: 'Multi-provider gateway' },
  { value: 'custom', label: 'Custom', description: 'Custom OpenAI-compatible endpoint' },
];

interface LLMSettingsSectionProps {
  onStatusChange?: (status: 'configured' | 'not-configured') => void;
}

export default function LLMSettingsSection({ onStatusChange }: LLMSettingsSectionProps) {
  const [llmSettings, setLlmSettings] = useState<LLMSettings | null>(null);
  const [llmLoading, setLlmLoading] = useState(true);
  const [llmSaving, setLlmSaving] = useState(false);
  const [llmError, setLlmError] = useState<string | null>(null);
  const [llmSuccess, setLlmSuccess] = useState(false);

  const [provider, setProvider] = useState<LLMProvider>('openai');
  const [apiKey, setApiKey] = useState('');
  const [model, setModel] = useState('gpt-5.4');
  const [thinkingLevel, setThinkingLevel] = useState<ThinkingLevel>('medium');
  const [baseUrl, setBaseUrl] = useState('');
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [providerCache, setProviderCache] = useState<Record<string, { apiKey: string; model: string; thinkingLevel: ThinkingLevel; baseUrl: string }>>({});

  useEffect(() => {
    loadLlmSettings();
  }, []);

  const loadLlmSettings = async () => {
    try {
      setLlmLoading(true);
      const data = await llmSettingsApi.get();
      setLlmSettings(data);

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

      const activeProvider = data.active_provider || data.provider || 'openai';
      setProvider(activeProvider);
      const activeSettings = cache[activeProvider];
      if (activeSettings) {
        setApiKey('');
        setModel(activeSettings.model || 'gpt-5.4');
        setThinkingLevel(activeSettings.thinkingLevel || 'medium');
        setBaseUrl(activeSettings.baseUrl || '');
      } else {
        setModel(data.model || 'gpt-5.4');
        setThinkingLevel(data.thinking_level || 'medium');
        setBaseUrl(data.base_url || '');
      }
      if (data.base_url || (data.thinking_level && data.thinking_level !== 'medium')) {
        setShowAdvanced(true);
      }
      setLlmError(null);
      onStatusChange?.(data.is_configured ? 'configured' : 'not-configured');
    } catch (err) {
      setLlmError('Failed to load LLM settings');
      console.error(err);
    } finally {
      setLlmLoading(false);
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
      await loadLlmSettings();
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
    setProviderCache(prev => ({
      ...prev,
      [provider]: { apiKey, model, thinkingLevel, baseUrl },
    }));

    setProvider(newProvider);

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

  const showBaseUrl = provider === 'custom' || provider === 'openrouter';

  const getApiKeyPlaceholder = (p: LLMProvider): string => {
    switch (p) {
      case 'openai': return 'sk-...';
      case 'anthropic': return 'sk-ant-...';
      case 'google': return 'AIza...';
      case 'openrouter': return 'sk-or-...';
      default: return 'Enter API key';
    }
  };

  if (llmLoading) {
    return <LoadingSpinner />;
  }

  return (
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
  );
}
