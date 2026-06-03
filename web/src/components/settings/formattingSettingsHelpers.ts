import type { FormattingSettingsUpdate } from '../../types';

export const DEFAULT_FORMATTING_PROMPT_PLACEHOLDER = `You are a senior incident-response writer. Reformat the agent's investigation into a structured incident summary aimed at on-call engineers.

Use the full reasoning trace as context but base the output on the agent's final response. Do not invent facts that are not supported by the trace.

Keep the tone factual and concise. Preserve specific identifiers (hosts, services, timestamps, error codes) the agent mentioned. The required output shape is enforced separately.`;

export const SYSTEM_PROMPT_MAX_BYTES = 8 * 1024;

// Matches the Go `defaultSchemaExample` constant in internal/services/formatter_schema.go.
// Used only as a textarea placeholder when the field is empty; "Reset to default" clears to ''
// so the backend treats it as usingDefaultSchema=true and applies the full built-in constraints.
export const DEFAULT_OUTPUT_SCHEMA_EXAMPLE = JSON.stringify(
  {
    status: 'resolved',
    summary: '1-3 sentence description of what happened and how it was resolved.',
    actions_taken: ['action 1'],
    recommendations: ['recommendation 1'],
  },
  null,
  2,
);

export const OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES = 8 * 1024;

// hydrateField turns an empty stored value into the editable default text so the
// textarea opens pre-filled with the real default the operator can tweak. A
// non-empty stored value (a custom edit) is passed through unchanged.
export function hydrateField(stored: string, defaultText: string): string {
  return stored.trim() ? stored : defaultText;
}

// dehydrateField is the inverse of hydrateField for the save path: if the box
// still holds the verbatim default, persist '' so the backend keeps treating it
// as the built-in default (e.g. usingDefaultSchema=true, which applies the
// status enum + non-empty summary constraints). Any real edit is sent as-is.
export function dehydrateField(current: string, defaultText: string): string {
  return current.trim() === defaultText.trim() ? '' : current;
}

export interface FormattingSettingsFormState {
  enabled: boolean;
  systemPrompt: string;
  maxTokens: number;
  temperature: number;
  outputSchemaExample: string;
}

export function buildFormattingUpdatePayload(
  state: FormattingSettingsFormState,
): FormattingSettingsUpdate {
  return {
    enabled: state.enabled,
    system_prompt: dehydrateField(state.systemPrompt, DEFAULT_FORMATTING_PROMPT_PLACEHOLDER),
    max_tokens: state.maxTokens,
    temperature: state.temperature,
    output_schema_example: dehydrateField(state.outputSchemaExample, DEFAULT_OUTPUT_SCHEMA_EXAMPLE),
  };
}

export function clampMaxTokens(raw: number): number {
  if (Number.isNaN(raw)) return 1;
  return Math.min(8000, Math.max(1, Math.round(raw)));
}

export function clampTemperature(raw: number): number {
  if (Number.isNaN(raw)) return 0;
  if (raw < 0) return 0;
  if (raw > 2) return 2;
  return raw;
}

export function systemPromptByteLength(prompt: string): number {
  return new TextEncoder().encode(prompt).length;
}
