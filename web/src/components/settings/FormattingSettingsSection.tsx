import { useState, useEffect } from 'react';
import { Save, Info } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { SuccessMessage } from '../ErrorMessage';
import { formattingSettingsApi } from '../../api/client';
import {
  DEFAULT_FORMATTING_PROMPT_PLACEHOLDER,
  SYSTEM_PROMPT_MAX_BYTES,
  buildFormattingUpdatePayload,
  clampMaxTokens,
  clampTemperature,
  systemPromptByteLength,
} from './formattingSettingsHelpers';

interface FormattingSettingsSectionProps {
  onStatusChange?: (status: 'configured' | 'disabled' | undefined) => void;
}

export default function FormattingSettingsSection({ onStatusChange }: FormattingSettingsSectionProps) {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [systemPrompt, setSystemPrompt] = useState('');
  const [maxTokens, setMaxTokens] = useState(1500);
  const [temperature, setTemperature] = useState(0.2);

  useEffect(() => {
    loadSettings();
  }, []);

  const loadSettings = async () => {
    try {
      setLoading(true);
      const data = await formattingSettingsApi.get();
      setEnabled(data.enabled);
      setSystemPrompt(data.system_prompt);
      setMaxTokens(data.max_tokens);
      setTemperature(data.temperature);
      setError(null);
      onStatusChange?.(data.enabled ? 'configured' : 'disabled');
    } catch (err) {
      setError('Failed to load formatting settings');
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const promptBytes = systemPromptByteLength(systemPrompt);
  const promptTooLong = promptBytes > SYSTEM_PROMPT_MAX_BYTES;

  const handleSave = async () => {
    if (promptTooLong) {
      setError(`System prompt must be ${SYSTEM_PROMPT_MAX_BYTES} bytes or fewer (current: ${promptBytes})`);
      return;
    }
    try {
      setSaving(true);
      setError(null);
      setSuccess(false);

      const payload = buildFormattingUpdatePayload({
        enabled,
        systemPrompt,
        maxTokens,
        temperature,
      });
      const updated = await formattingSettingsApi.update(payload);
      setEnabled(updated.enabled);
      setSystemPrompt(updated.system_prompt);
      setMaxTokens(updated.max_tokens);
      setTemperature(updated.temperature);
      onStatusChange?.(updated.enabled ? 'configured' : 'disabled');
      setSuccess(true);
      setTimeout(() => setSuccess(false), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save formatting settings');
      console.error(err);
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return <LoadingSpinner />;
  }

  return (
    <div className="space-y-5">
      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message="Formatting settings saved" />}

      <div className="rounded-lg bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 p-3 text-xs text-blue-900 dark:text-blue-200 flex gap-2">
        <Info className="w-4 h-4 flex-shrink-0 mt-0.5" />
        <span>
          When enabled, the formatter sends the agent's final response and full reasoning trace to the
          active LLM in a one-shot call. The prompt below controls the structure of the output that
          replaces <code>incident.response</code>. The raw reasoning is preserved in
          {' '}
          <code>incident.full_log</code>.
        </span>
      </div>

      <div className="flex items-center justify-between">
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
            Enable response formatter
          </label>
          <p className="text-xs text-gray-500 dark:text-gray-400">
            Reformat the agent's final response with an extra LLM pass before storing it
          </p>
        </div>
        <button
          type="button"
          role="switch"
          aria-checked={enabled}
          onClick={() => setEnabled(!enabled)}
          className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
            enabled ? 'bg-blue-600' : 'bg-gray-300 dark:bg-gray-600'
          }`}
        >
          <span
            className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
              enabled ? 'translate-x-6' : 'translate-x-1'
            }`}
          />
        </button>
      </div>

      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
          System prompt
        </label>
        <textarea
          value={systemPrompt}
          onChange={(e) => setSystemPrompt(e.target.value)}
          disabled={!enabled}
          rows={12}
          placeholder={DEFAULT_FORMATTING_PROMPT_PLACEHOLDER}
          className="input-field font-mono text-xs"
        />
        <div className="mt-1 flex items-center justify-between">
          <p className="text-xs text-gray-500 dark:text-gray-400">
            Instructs the LLM how to structure the incident summary. Leave blank to use the default
            prompt shown as placeholder.
          </p>
          <p className={`text-xs ${promptTooLong ? 'text-red-600 dark:text-red-400' : 'text-gray-500 dark:text-gray-400'}`}>
            {promptBytes} / {SYSTEM_PROMPT_MAX_BYTES} bytes
          </p>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
            Max tokens
          </label>
          <input
            type="number"
            min={1}
            max={8000}
            value={maxTokens}
            onChange={(e) => setMaxTokens(clampMaxTokens(parseInt(e.target.value, 10)))}
            disabled={!enabled}
            className="input-field"
          />
          <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
            Upper bound on the formatted response length (1 - 8000).
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1.5">
            Temperature
          </label>
          <input
            type="number"
            min={0}
            max={2}
            step={0.1}
            value={temperature}
            onChange={(e) => setTemperature(clampTemperature(parseFloat(e.target.value)))}
            disabled={!enabled}
            className="input-field"
          />
          <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
            Sampling temperature (0 - 2). Lower values produce more deterministic output.
          </p>
        </div>
      </div>

      <div className="flex items-center justify-between pt-4 border-t border-gray-200 dark:border-gray-700">
        <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-1.5">
          <Info className="w-3.5 h-3.5" />
          Changes take effect on the next incident finalization
        </p>
        <button
          onClick={handleSave}
          disabled={saving || promptTooLong}
          className="btn btn-primary"
        >
          <Save className="w-4 h-4" />
          {saving ? 'Saving...' : 'Save'}
        </button>
      </div>
    </div>
  );
}
