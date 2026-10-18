import { RotateCcw } from 'lucide-react';
import {
  DEFAULT_FORMATTING_PROMPT_PLACEHOLDER,
  DEFAULT_OUTPUT_SCHEMA_EXAMPLE,
  OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES,
  SYSTEM_PROMPT_MAX_BYTES,
  clampMaxTokens,
  clampTemperature,
  systemPromptByteLength,
  validateSchemaExampleText,
} from './formattingSettingsHelpers';

export interface FormattingConfigValues {
  systemPrompt: string;
  outputSchemaExample: string;
  maxTokens: number;
  temperature: number;
}

interface FormattingConfigFieldsProps {
  values: FormattingConfigValues;
  onChange: (patch: Partial<FormattingConfigValues>) => void;
  disabled?: boolean;
  schemaJsonError: string | null;
  onSchemaJsonError: (error: string | null) => void;
}

// FormattingConfigFields renders the four format-config inputs (system
// prompt, output shape, max tokens, temperature) shared by the rule editor.
export default function FormattingConfigFields({
  values,
  onChange,
  disabled = false,
  schemaJsonError,
  onSchemaJsonError,
}: FormattingConfigFieldsProps) {
  const promptBytes = systemPromptByteLength(values.systemPrompt);
  const promptTooLong = promptBytes > SYSTEM_PROMPT_MAX_BYTES;
  const schemaBytes = new TextEncoder().encode(values.outputSchemaExample).length;
  const schemaTooLong = schemaBytes > OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES;

  return (
    <div className="space-y-5">
      <div>
        <div className="flex items-center justify-between mb-1.5">
          <label className="block text-sm font-medium text-fg">
            System prompt
          </label>
          <button
            type="button"
            onClick={() => onChange({ systemPrompt: '' })}
            disabled={disabled}
            className="flex items-center gap-1 text-xs text-fg-subtle hover:text-fg disabled:opacity-40 disabled:cursor-not-allowed"
          >
            <RotateCcw className="w-3 h-3" />
            Clear
          </button>
        </div>
        <textarea
          value={values.systemPrompt}
          onChange={(e) => onChange({ systemPrompt: e.target.value })}
          disabled={disabled}
          rows={10}
          placeholder={DEFAULT_FORMATTING_PROMPT_PLACEHOLDER}
          className="input-field font-mono text-xs"
        />
        <div className="mt-1 flex items-center justify-between">
          <p className="text-xs text-fg-subtle">
            Instructs the LLM how to structure the summary. Clear the box to fall back to the
            built-in default.
          </p>
          <p className={`text-xs ${promptTooLong ? 'text-state-fail' : 'text-fg-subtle'}`}>
            {promptBytes} / {SYSTEM_PROMPT_MAX_BYTES} bytes
          </p>
        </div>
      </div>

      <div>
        <div className="flex items-center justify-between mb-1.5">
          <label className="block text-sm font-medium text-fg">
            Output shape
          </label>
          <button
            type="button"
            onClick={() => {
              onChange({ outputSchemaExample: '' });
              onSchemaJsonError(null);
            }}
            disabled={disabled}
            className="flex items-center gap-1 text-xs text-fg-subtle hover:text-fg disabled:opacity-40 disabled:cursor-not-allowed"
          >
            <RotateCcw className="w-3 h-3" />
            Clear
          </button>
        </div>
        <textarea
          value={values.outputSchemaExample}
          onChange={(e) => {
            onChange({ outputSchemaExample: e.target.value });
            if (schemaJsonError) onSchemaJsonError(null);
          }}
          onBlur={() => onSchemaJsonError(validateSchemaExampleText(values.outputSchemaExample))}
          disabled={disabled}
          rows={7}
          placeholder={DEFAULT_OUTPUT_SCHEMA_EXAMPLE}
          className={`input-field font-mono text-xs ${schemaJsonError || schemaTooLong ? 'border-red-500 dark:border-red-500' : ''}`}
        />
        <div className="mt-1 flex items-start justify-between gap-2">
          <div className="flex-1">
            {schemaJsonError ? (
              <p className="text-xs text-state-fail">{schemaJsonError}</p>
            ) : schemaTooLong ? (
              <p className="text-xs text-state-fail">
                Output shape must be {OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES} bytes or fewer
              </p>
            ) : (
              <p className="text-xs text-fg-subtle">
                An example of the JSON object you want as the final summary. The LLM will be
                instructed to return exactly this shape.
              </p>
            )}
          </div>
          <p className={`text-xs flex-shrink-0 ${schemaTooLong ? 'text-state-fail' : 'text-fg-subtle'}`}>
            {schemaBytes} / {OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES} bytes
          </p>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-fg mb-1.5">
            Max tokens
          </label>
          <input
            type="number"
            min={1}
            max={8000}
            value={values.maxTokens}
            onChange={(e) => onChange({ maxTokens: clampMaxTokens(parseInt(e.target.value, 10)) })}
            disabled={disabled}
            className="input-field"
          />
          <p className="mt-1 text-xs text-fg-subtle">
            Upper bound on the formatted response length (1 - 8000).
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-fg mb-1.5">
            Temperature
          </label>
          <input
            type="number"
            min={0}
            max={2}
            step={0.1}
            value={values.temperature}
            onChange={(e) => onChange({ temperature: clampTemperature(parseFloat(e.target.value)) })}
            disabled={disabled}
            className="input-field"
          />
          <p className="mt-1 text-xs text-fg-subtle">
            Sampling temperature (0 - 2). Lower values produce more deterministic output.
          </p>
        </div>
      </div>
    </div>
  );
}
