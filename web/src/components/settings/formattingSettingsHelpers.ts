import type { FormattingSettingsUpdate } from '../../types';

export const DEFAULT_FORMATTING_PROMPT_PLACEHOLDER = `You are a senior incident-response writer. Reformat the agent's investigation into a clean, structured incident summary aimed at on-call engineers.

Use the full reasoning trace as context but base the user-facing output on the agent's final response. Do not invent facts that are not supported by the trace.

Output sections (omit a section only if there is nothing to say):
- Status: one short line (resolved / unresolved / escalated, plus headline impact).
- Summary: 1-3 sentences describing what happened and the suspected root cause.
- Actions taken: bullet list of concrete steps the agent performed.
- Recommendations / Next steps: bullet list of what a human should do next.

Keep the tone factual and concise. Use plain prose and bullet lists; do not wrap the response in code fences. Preserve any specific identifiers (hosts, services, timestamps, error codes) the agent mentioned.`;

export const SYSTEM_PROMPT_MAX_BYTES = 8 * 1024;

export interface FormattingSettingsFormState {
  enabled: boolean;
  systemPrompt: string;
  maxTokens: number;
  temperature: number;
}

export function buildFormattingUpdatePayload(
  state: FormattingSettingsFormState,
): FormattingSettingsUpdate {
  return {
    enabled: state.enabled,
    system_prompt: state.systemPrompt,
    max_tokens: state.maxTokens,
    temperature: state.temperature,
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
