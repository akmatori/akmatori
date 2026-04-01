import { useState, useEffect } from 'react';
import { Save, Plus, Trash2, Edit2, X, ChevronDown, ChevronRight } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { SuccessMessage } from '../ErrorMessage';
import { llmSettingsApi } from '../../api/client';
import type { LLMConfig, LLMProvider, ThinkingLevel } from '../../types';

const MODEL_SUGGESTIONS: Record<LLMProvider, { value: string; label: string }[]> = {
  openai: [
    { value: 'gpt-5.4', label: 'gpt-5.4 (Recommended)' },
    { value: 'gpt-5.4-mini', label: 'gpt-5.4-mini (Fast)' },
    { value: 'gpt-5.3-codex', label: 'gpt-5.3-codex' },
    { value: 'gpt-5-mini', label: 'gpt-5-mini (Budget)' },
    { value: 'o4-mini', label: 'o4-mini (Reasoning)' },
  ],
  anthropic: [
    { value: 'claude-opus-4-6', label: 'claude-opus-4-6 (Most capable)' },
    { value: 'claude-sonnet-4-6', label: 'claude-sonnet-4-6 (Recommended)' },
    { value: 'claude-sonnet-4-5', label: 'claude-sonnet-4-5' },
    { value: 'claude-haiku-4-5', label: 'claude-haiku-4-5 (Fast)' },
  ],
  google: [
    { value: 'gemini-2.5-pro', label: 'gemini-2.5-pro (Recommended)' },
    { value: 'gemini-2.5-flash', label: 'gemini-2.5-flash (Fast)' },
    { value: 'gemini-2.0-flash', label: 'gemini-2.0-flash (Stable)' },
  ],
  openrouter: [
    { value: 'anthropic/claude-sonnet-4-6', label: 'anthropic/claude-sonnet-4-6' },
    { value: 'openai/gpt-5.4', label: 'openai/gpt-5.4' },
    { value: 'openai/gpt-5.4-mini', label: 'openai/gpt-5.4-mini' },
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

const PROVIDER_OPTIONS: { value: LLMProvider; label: string }[] = [
  { value: 'openai', label: 'OpenAI' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'google', label: 'Google' },
  { value: 'openrouter', label: 'OpenRouter' },
  { value: 'custom', label: 'Custom' },
];

const PROVIDER_LABELS: Record<string, string> = Object.fromEntries(
  PROVIDER_OPTIONS.map((o) => [o.value, o.label])
);

interface LLMSettingsSectionProps {
  onStatusChange?: (status: 'configured' | 'not-configured') => void;
}

type FormMode = 'closed' | 'create' | 'edit';

interface FormState {
  name: string;
  provider: LLMProvider;
  apiKey: string;
  model: string;
  thinkingLevel: ThinkingLevel;
  baseUrl: string;
}

const emptyForm: FormState = {
  name: '',
  provider: 'openai',
  apiKey: '',
  model: 'gpt-5.4',
  thinkingLevel: 'medium',
  baseUrl: '',
};

export default function LLMSettingsSection({ onStatusChange }: LLMSettingsSectionProps) {
  const [configs, setConfigs] = useState<LLMConfig[]>([]);
  const [activeId, setActiveId] = useState<number>(0);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  const [formMode, setFormMode] = useState<FormMode>('closed');
  const [editingId, setEditingId] = useState<number | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [deleteConfirmId, setDeleteConfirmId] = useState<number | null>(null);

  useEffect(() => {
    loadConfigs();
  }, []);

  const loadConfigs = async () => {
    try {
      setLoading(true);
      const data = await llmSettingsApi.list();
      setConfigs(data.configs || []);
      setActiveId(data.active_id);
      setError(null);
      const activeConfig = (data.configs || []).find(c => c.id === data.active_id);
      const isConfigured = activeConfig?.is_configured ?? false;
      onStatusChange?.(isConfigured ? 'configured' : 'not-configured');
    } catch (err) {
      setError('Failed to load LLM settings');
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const showSuccess = (msg: string) => {
    setSuccess(msg);
    setTimeout(() => setSuccess(null), 3000);
  };

  const openCreateForm = () => {
    setForm(emptyForm);
    setFormMode('create');
    setEditingId(null);
    setShowAdvanced(false);
    setError(null);
  };

  const openEditForm = (config: LLMConfig) => {
    setForm({
      name: config.name,
      provider: config.provider,
      apiKey: '',
      model: config.model,
      thinkingLevel: config.thinking_level || 'medium',
      baseUrl: config.base_url || '',
    });
    setFormMode('edit');
    setEditingId(config.id);
    setShowAdvanced(!!config.base_url || (config.thinking_level && config.thinking_level !== 'medium'));
    setError(null);
  };

  const closeForm = () => {
    setFormMode('closed');
    setEditingId(null);
    setError(null);
  };

  const handleFormProviderChange = (provider: LLMProvider) => {
    const suggestions = MODEL_SUGGESTIONS[provider];
    setForm(prev => ({
      ...prev,
      provider,
      model: suggestions.length > 0 ? suggestions[0].value : '',
    }));
  };

  const handleSave = async () => {
    try {
      setSaving(true);
      setError(null);

      if (formMode === 'create') {
        await llmSettingsApi.create({
          provider: form.provider,
          name: form.name,
          api_key: form.apiKey || undefined,
          model: form.model || undefined,
          thinking_level: form.thinkingLevel || undefined,
          base_url: form.baseUrl || undefined,
        });
        showSuccess('Configuration created');
      } else if (formMode === 'edit' && editingId) {
        const updates: Record<string, string | undefined> = {};
        updates.name = form.name;
        updates.model = form.model;
        updates.thinking_level = form.thinkingLevel;
        updates.base_url = form.baseUrl;
        if (form.apiKey && !form.apiKey.startsWith('****')) {
          updates.api_key = form.apiKey;
        }
        await llmSettingsApi.update(editingId, updates);
        showSuccess('Configuration updated');
      }

      closeForm();
      await loadConfigs();
    } catch (err: any) {
      setError(err?.message || 'Failed to save configuration');
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: number) => {
    try {
      setSaving(true);
      setError(null);
      await llmSettingsApi.delete(id);
      setDeleteConfirmId(null);
      showSuccess('Configuration deleted');
      await loadConfigs();
    } catch (err: any) {
      setError(err?.message || 'Failed to delete configuration');
    } finally {
      setSaving(false);
    }
  };

  const handleActivate = async (id: number) => {
    try {
      setSaving(true);
      setError(null);
      await llmSettingsApi.activate(id);
      showSuccess('Configuration activated');
      await loadConfigs();
    } catch (err: any) {
      setError(err?.message || 'Failed to activate configuration');
    } finally {
      setSaving(false);
    }
  };

  const getApiKeyPlaceholder = (p: LLMProvider): string => {
    switch (p) {
      case 'openai': return 'sk-...';
      case 'anthropic': return 'sk-ant-...';
      case 'google': return 'AIza...';
      case 'openrouter': return 'sk-or-...';
      default: return 'Enter API key';
    }
  };

  const showBaseUrl = form.provider === 'custom' || form.provider === 'openrouter';

  if (loading) {
    return <LoadingSpinner />;
  }

  const activeConfig = configs.find(c => c.id === activeId);
  const otherConfigured = configs.filter(c => c.is_configured && c.id !== activeId);
  const hasConfigured = configs.some(c => c.is_configured);

  const renderForm = () => {
    const isCreate = formMode === 'create';
    const currentProvider = form.provider;
    const suggestions = MODEL_SUGGESTIONS[currentProvider] || [];

    return (
      <div className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg p-5 space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-white">
            {isCreate ? 'New Configuration' : 'Edit Configuration'}
          </h3>
          <button onClick={closeForm} className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300">
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Provider (only on create) */}
        {isCreate && (
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
              Provider <span className="text-red-500">*</span>
            </label>
            <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
              {PROVIDER_OPTIONS.map((opt) => (
                <button
                  key={opt.value}
                  type="button"
                  onClick={() => handleFormProviderChange(opt.value)}
                  className={`flex items-center justify-center p-2 rounded-lg border text-sm transition-colors ${
                    currentProvider === opt.value
                      ? 'bg-primary-50 dark:bg-primary-900/20 border-primary-300 dark:border-primary-700 text-primary-700 dark:text-primary-300 font-medium'
                      : 'bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-700 text-gray-900 dark:text-white'
                  }`}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>
        )}

        {/* Name */}
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
            Name <span className="text-red-500">*</span>
          </label>
          <input
            type="text"
            value={form.name}
            onChange={(e) => setForm(prev => ({ ...prev, name: e.target.value }))}
            placeholder={`e.g., ${PROVIDER_LABELS[currentProvider] || currentProvider} Production`}
            className="input-field"
            maxLength={100}
          />
        </div>

        {/* API Key */}
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
            API Key {isCreate && <span className="text-red-500">*</span>}
          </label>
          <input
            type="password"
            value={form.apiKey}
            onChange={(e) => setForm(prev => ({ ...prev, apiKey: e.target.value }))}
            placeholder={!isCreate && editingId
              ? configs.find(c => c.id === editingId)?.api_key || getApiKeyPlaceholder(currentProvider)
              : getApiKeyPlaceholder(currentProvider)
            }
            className="input-field"
          />
          {!isCreate && editingId && (
            <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
              Leave blank to keep existing key
            </p>
          )}
        </div>

        {/* Model Selection */}
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
            Model
          </label>
          {suggestions.length > 0 ? (
            <div>
              <select
                value={suggestions.some(s => s.value === form.model) ? form.model : '__custom__'}
                onChange={(e) => {
                  if (e.target.value === '__custom__') {
                    setForm(prev => ({ ...prev, model: '' }));
                  } else {
                    setForm(prev => ({ ...prev, model: e.target.value }));
                  }
                }}
                className="input-field"
              >
                {suggestions.map((s) => (
                  <option key={s.value} value={s.value}>{s.label}</option>
                ))}
                {!suggestions.some(s => s.value === form.model) && form.model && (
                  <option value="__custom__">Custom: {form.model}</option>
                )}
                <option value="__custom__">Other (enter manually)</option>
              </select>
              {(!suggestions.some(s => s.value === form.model) || form.model === '') && (
                <input
                  type="text"
                  value={form.model}
                  onChange={(e) => setForm(prev => ({ ...prev, model: e.target.value }))}
                  placeholder="Enter model name"
                  className="input-field mt-2"
                />
              )}
            </div>
          ) : (
            <input
              type="text"
              value={form.model}
              onChange={(e) => setForm(prev => ({ ...prev, model: e.target.value }))}
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
          {showAdvanced ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          Advanced settings
          {(form.thinkingLevel !== 'medium' || form.baseUrl) && (
            <span className="text-xs text-primary-600 dark:text-primary-400">(customized)</span>
          )}
        </button>

        {showAdvanced && (
          <div className="space-y-4 pl-4 border-l-2 border-gray-200 dark:border-gray-700">
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
                Thinking Level
              </label>
              <select
                value={form.thinkingLevel}
                onChange={(e) => setForm(prev => ({ ...prev, thinkingLevel: e.target.value as ThinkingLevel }))}
                className="input-field"
              >
                {THINKING_LEVELS.map((level) => (
                  <option key={level.value} value={level.value}>{level.label}</option>
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
                  value={form.baseUrl}
                  onChange={(e) => setForm(prev => ({ ...prev, baseUrl: e.target.value }))}
                  placeholder={currentProvider === 'openrouter' ? 'https://openrouter.ai/api/v1' : 'https://your-endpoint.example.com/v1'}
                  className="input-field"
                />
                <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {currentProvider === 'openrouter'
                    ? 'OpenRouter API endpoint (defaults to https://openrouter.ai/api/v1)'
                    : 'Custom OpenAI-compatible API endpoint'}
                </p>
              </div>
            )}
          </div>
        )}

        {/* Save / Cancel */}
        <div className="flex items-center justify-end gap-3 pt-3 border-t border-gray-200 dark:border-gray-700">
          <button onClick={closeForm} className="btn btn-secondary" disabled={saving}>
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={saving || !form.name.trim() || (isCreate && !form.apiKey)}
            className="btn btn-primary"
          >
            <Save className="w-4 h-4" />
            {saving ? 'Saving...' : isCreate ? 'Create' : 'Save'}
          </button>
        </div>
      </div>
    );
  };

  return (
    <div className="space-y-5">
      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message={success} />}

      {/* Create/Edit Form */}
      {formMode !== 'closed' && renderForm()}

      {/* Active Provider hero card */}
      {activeConfig && activeConfig.is_configured ? (
        <div className="rounded-lg border-l-4 border-l-primary-500 border border-gray-200 dark:border-gray-700 bg-primary-50 dark:bg-primary-900/20 p-4">
          <div className="flex items-center justify-between">
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-primary-100 dark:bg-primary-800 text-primary-700 dark:text-primary-300">
                  Active
                </span>
                <span className="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase">
                  {PROVIDER_LABELS[activeConfig.provider] || activeConfig.provider}
                </span>
              </div>
              <p className="text-sm font-semibold text-gray-900 dark:text-white mt-1">
                {activeConfig.name}
              </p>
              <p className="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                {activeConfig.model || 'No model set'}
                {activeConfig.thinking_level && activeConfig.thinking_level !== 'medium' && ` \u00B7 Thinking: ${activeConfig.thinking_level}`}
                {activeConfig.base_url && ` \u00B7 Custom URL`}
              </p>
            </div>
            <button
              onClick={() => openEditForm(activeConfig)}
              disabled={saving || formMode !== 'closed'}
              className="p-1.5 rounded text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
              title="Edit"
            >
              <Edit2 className="w-4 h-4" />
            </button>
          </div>
        </div>
      ) : activeConfig && !activeConfig.is_configured ? (
        <div className="rounded-lg border border-yellow-300 dark:border-yellow-600 bg-yellow-50 dark:bg-yellow-900/20 p-4">
          <p className="text-sm text-yellow-700 dark:text-yellow-400">
            Your active LLM provider has no API key configured.
          </p>
          <button
            onClick={() => openEditForm(activeConfig)}
            disabled={saving || formMode !== 'closed'}
            className="mt-2 text-sm font-medium text-yellow-700 dark:text-yellow-400 underline hover:no-underline"
          >
            Configure it now
          </button>
        </div>
      ) : null}

      {/* Other configured providers */}
      {otherConfigured.length > 0 && (
        <div>
          <h3 className="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider mb-2">
            Other Configured Providers
          </h3>
          <div className="space-y-2">
            {otherConfigured.map((config) => (
              <div
                key={config.id}
                className="relative flex items-center justify-between p-4 rounded-lg border bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700 transition-colors"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-gray-900 dark:text-white truncate">
                      {config.name}
                    </span>
                    <span className="text-xs text-gray-400 dark:text-gray-500">
                      {PROVIDER_LABELS[config.provider] || config.provider}
                    </span>
                  </div>
                  <p className="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                    {config.model || 'No model set'}
                    {config.thinking_level && config.thinking_level !== 'medium' && ` \u00B7 Thinking: ${config.thinking_level}`}
                    {config.base_url && ` \u00B7 Custom URL`}
                  </p>
                </div>
                <div className="flex items-center gap-1 ml-3">
                  <button
                    onClick={() => handleActivate(config.id)}
                    disabled={saving}
                    className="px-2.5 py-1 rounded text-xs font-medium text-primary-600 dark:text-primary-400 bg-primary-50 dark:bg-primary-900/20 hover:bg-primary-100 dark:hover:bg-primary-900/40 transition-colors"
                  >
                    Activate
                  </button>
                  <button
                    onClick={() => openEditForm(config)}
                    disabled={saving || formMode !== 'closed'}
                    className="p-1.5 rounded text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                    title="Edit"
                  >
                    <Edit2 className="w-4 h-4" />
                  </button>
                  {deleteConfirmId === config.id ? (
                    <div className="flex items-center gap-1">
                      <button
                        onClick={() => handleDelete(config.id)}
                        disabled={saving}
                        className="px-2 py-1 rounded text-xs font-medium text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-900/20 hover:bg-red-100 dark:hover:bg-red-900/40 transition-colors"
                      >
                        Confirm
                      </button>
                      <button
                        onClick={() => setDeleteConfirmId(null)}
                        className="px-2 py-1 rounded text-xs text-gray-500 hover:text-gray-700 dark:hover:text-gray-300 transition-colors"
                      >
                        Cancel
                      </button>
                    </div>
                  ) : (
                    <button
                      onClick={() => setDeleteConfirmId(config.id)}
                      disabled={saving || formMode !== 'closed'}
                      className="p-1.5 rounded text-gray-400 hover:text-red-500 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                      title="Delete"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Add Configuration button */}
      {formMode === 'closed' && (
        <button onClick={openCreateForm} className="btn btn-primary">
          <Plus className="w-4 h-4" />
          Add Configuration
        </button>
      )}

      {!hasConfigured && formMode === 'closed' && (
        <p className="text-sm text-gray-500 dark:text-gray-400 text-center py-6">
          No LLM providers configured yet. Add one to get started.
        </p>
      )}
    </div>
  );
}
