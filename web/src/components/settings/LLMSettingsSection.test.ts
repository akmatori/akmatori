import { describe, it, expect } from 'vitest';
import { MODEL_SUGGESTIONS } from './LLMSettingsSection';

describe('MODEL_SUGGESTIONS', () => {
  const ids = (provider: keyof typeof MODEL_SUGGESTIONS) =>
    MODEL_SUGGESTIONS[provider].map((s) => s.value);

  it('includes the new OpenAI frontier models', () => {
    expect(ids('openai')).toEqual(expect.arrayContaining(['gpt-5.5', 'gpt-5.5-pro']));
  });

  it('includes Anthropic claude-opus-4-7', () => {
    expect(ids('anthropic')).toEqual(expect.arrayContaining(['claude-opus-4-7']));
  });

  it('includes Google gemini-3 preview models', () => {
    expect(ids('google')).toEqual(
      expect.arrayContaining([
        'gemini-3-pro-preview',
        'gemini-3.1-pro-preview',
        'gemini-3-flash-preview',
      ]),
    );
  });

  it('includes the new OpenRouter aliases', () => {
    expect(ids('openrouter')).toEqual(
      expect.arrayContaining([
        'anthropic/claude-opus-4-7',
        'openai/gpt-5.5',
        'google/gemini-3-pro-preview',
      ]),
    );
  });

  it('keeps existing models for backward compatibility', () => {
    expect(ids('openai')).toEqual(expect.arrayContaining(['gpt-5.4', 'gpt-5.4-mini']));
    expect(ids('anthropic')).toEqual(expect.arrayContaining(['claude-opus-4-6', 'claude-sonnet-4-6']));
    expect(ids('google')).toEqual(expect.arrayContaining(['gemini-2.5-pro']));
    expect(ids('openrouter')).toEqual(expect.arrayContaining(['anthropic/claude-sonnet-4-6']));
  });
});
