import { useState, useEffect, useRef } from 'react';
import { Save, Info, RotateCcw } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { SuccessMessage } from '../ErrorMessage';
import { formattingSettingsApi } from '../../api/client';
import {
  DEFAULT_FORMATTING_PROMPT_PLACEHOLDER,
  DEFAULT_OUTPUT_SCHEMA_EXAMPLE,
  OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES,
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
  const [outputSchemaExample, setOutputSchemaExample] = useState('');
  const [schemaJsonError, setSchemaJsonError] = useState<string | null>(null);
  const successTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    loadSettings();
    return () => {
      if (successTimeoutRef.current !== null) {
        clearTimeout(successTimeoutRef.current);
      }
    };
  }, []);

  const loadSettings = async () => {
    try {
      setLoading(true);
      const data = await formattingSettingsApi.get();
      setEnabled(data.enabled);
      setSystemPrompt(data.system_prompt);
      setMaxTokens(data.max_tokens);
      setTemperature(data.temperature);
      setOutputSchemaExample(data.output_schema_example ?? '');
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
  const schemaBytes = new TextEncoder().encode(outputSchemaExample).length;
  const schemaTooLong = schemaBytes > OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES;
  const schemaInvalid = schemaJsonError !== null || schemaTooLong;

  const handleSchemaBlur = () => {
    if (!outputSchemaExample.trim()) {
      setSchemaJsonError(null);
      return;
    }
    try {
      const parsed = JSON.parse(outputSchemaExample);
      if (typeof parsed !== 'object' || Array.isArray(parsed) || parsed === null) {
        setSchemaJsonError('Must be a JSON object (not an array or scalar)');
      } else {
        setSchemaJsonError(null);
      }
    } catch (e) {
      setSchemaJsonError(e instanceof Error ? e.message : 'Invalid JSON');
    }
  };

  const handleResetSchema = () => {
    setOutputSchemaExample(DEFAULT_OUTPUT_SCHEMA_EXAMPLE);
    setSchemaJsonError(null);
  };

  const handleSave = async () => {
    if (promptTooLong) {
      setError(`System prompt must be ${SYSTEM_PROMPT_MAX_BYTES} bytes or fewer (current: ${promptBytes})`);
      return;
    }
    if (schemaTooLong) {
      setError(`Output shape must be ${OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES} bytes or fewer (current: ${schemaBytes})`);
      return;
    }
    // Validate inline in case the textarea was never blurred
    if (!schemaJsonError && outputSchemaExample.trim()) {
      try {
        const parsed = JSON.parse(outputSchemaExample);
        if (typeof parsed !== 'object' || Array.isArray(parsed) || parsed === null) {
          const msg = 'Must be a JSON object (not an array or scalar)';
          setSchemaJsonError(msg);
          setError(`Output shape: ${msg}`);
          return;
        }
      } catch (e) {
        const msg = e instanceof Error ? e.message : 'Invalid JSON';
        setSchemaJsonError(msg);
        setError(`Output shape: ${msg}`);
        return;
      }
    }
    if (schemaJsonError) {
      setError(`Output shape: ${schemaJsonError}`);
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
        outputSchemaExample,
      });
      const updated = await formattingSettingsApi.update(payload);
      setEnabled(updated.enabled);
      setSystemPrompt(updated.system_prompt);
      setMaxTokens(updated.max_tokens);
      setTemperature(updated.temperature);
      setOutputSchemaExample(updated.output_schema_example ?? '');
      onStatusChange?.(updated.enabled ? 'configured' : 'disabled');
      setSuccess(true);
      if (successTimeoutRef.current !== null) {
        clearTimeout(successTimeoutRef.current);
      }
      successTimeoutRef.current = setTimeout(() => {
        setSuccess(false);
        successTimeoutRef.current = null;
      }, 3000);
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

      <div>
        <div className="flex items-center justify-between mb-1.5">
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
            Output shape
          </label>
          <button
            type="button"
            onClick={handleResetSchema}
            disabled={!enabled}
            className="flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 disabled:opacity-40 disabled:cursor-not-allowed"
          >
            <RotateCcw className="w-3 h-3" />
            Reset to default
          </button>
        </div>
        <textarea
          value={outputSchemaExample}
          onChange={(e) => {
            setOutputSchemaExample(e.target.value);
            if (schemaJsonError) setSchemaJsonError(null);
          }}
          onBlur={handleSchemaBlur}
          disabled={!enabled}
          rows={8}
          placeholder={DEFAULT_OUTPUT_SCHEMA_EXAMPLE}
          className={`input-field font-mono text-xs ${schemaJsonError || schemaTooLong ? 'border-red-500 dark:border-red-500' : ''}`}
        />
        <div className="mt-1 flex items-start justify-between gap-2">
          <div className="flex-1">
            {schemaJsonError ? (
              <p className="text-xs text-red-600 dark:text-red-400">{schemaJsonError}</p>
            ) : schemaTooLong ? (
              <p className="text-xs text-red-600 dark:text-red-400">
                Output shape must be {OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES} bytes or fewer
              </p>
            ) : (
              <p className="text-xs text-gray-500 dark:text-gray-400">
                Paste an example of the JSON object you want as the final summary. Leave blank to use
                the built-in four-key default. The LLM will be instructed to return exactly this shape.
              </p>
            )}
          </div>
          <p className={`text-xs flex-shrink-0 ${schemaTooLong ? 'text-red-600 dark:text-red-400' : 'text-gray-500 dark:text-gray-400'}`}>
            {schemaBytes} / {OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES} bytes
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
          disabled={saving || promptTooLong || schemaInvalid}
          className="btn btn-primary"
        >
          <Save className="w-4 h-4" />
          {saving ? 'Saving...' : 'Save'}
        </button>
      </div>
    </div>
  );
}
