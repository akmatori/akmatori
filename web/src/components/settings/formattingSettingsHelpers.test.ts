import { describe, it, expect } from 'vitest';
import {
  buildFormattingUpdatePayload,
  clampMaxTokens,
  clampTemperature,
  systemPromptByteLength,
  DEFAULT_FORMATTING_PROMPT_PLACEHOLDER,
  DEFAULT_OUTPUT_SCHEMA_EXAMPLE,
  OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES,
  SYSTEM_PROMPT_MAX_BYTES,
} from './formattingSettingsHelpers';

describe('FormattingSettings helpers', () => {
  describe('buildFormattingUpdatePayload', () => {
    it('maps form state into the API request payload', () => {
      const payload = buildFormattingUpdatePayload({
        enabled: true,
        systemPrompt: 'reformat please',
        maxTokens: 1234,
        temperature: 0.7,
        outputSchemaExample: '{"severity":"high"}',
      });
      expect(payload).toEqual({
        enabled: true,
        system_prompt: 'reformat please',
        max_tokens: 1234,
        temperature: 0.7,
        output_schema_example: '{"severity":"high"}',
      });
    });

    it('preserves an empty system prompt so the user can opt out of a custom prompt', () => {
      const payload = buildFormattingUpdatePayload({
        enabled: false,
        systemPrompt: '',
        maxTokens: 1500,
        temperature: 0.2,
        outputSchemaExample: '',
      });
      expect(payload.system_prompt).toBe('');
      expect(payload.enabled).toBe(false);
    });

    it('sends empty string for output_schema_example when the field is empty (resets to built-in default)', () => {
      const payload = buildFormattingUpdatePayload({
        enabled: true,
        systemPrompt: '',
        maxTokens: 1500,
        temperature: 0.2,
        outputSchemaExample: '',
      });
      expect(payload.output_schema_example).toBe('');
    });

    it('sends the value when output_schema_example is non-empty', () => {
      const example = '{"foo":"bar"}';
      const payload = buildFormattingUpdatePayload({
        enabled: true,
        systemPrompt: '',
        maxTokens: 1500,
        temperature: 0.2,
        outputSchemaExample: example,
      });
      expect(payload.output_schema_example).toBe(example);
    });
  });

  describe('clampMaxTokens', () => {
    it('clamps to the [1, 8000] range', () => {
      expect(clampMaxTokens(0)).toBe(1);
      expect(clampMaxTokens(-50)).toBe(1);
      expect(clampMaxTokens(1)).toBe(1);
      expect(clampMaxTokens(1500)).toBe(1500);
      expect(clampMaxTokens(8000)).toBe(8000);
      expect(clampMaxTokens(99999)).toBe(8000);
    });

    it('rounds to integers and falls back to 1 for NaN', () => {
      expect(clampMaxTokens(123.7)).toBe(124);
      expect(clampMaxTokens(NaN)).toBe(1);
    });
  });

  describe('clampTemperature', () => {
    it('clamps to the [0, 2] range', () => {
      expect(clampTemperature(-0.1)).toBe(0);
      expect(clampTemperature(0)).toBe(0);
      expect(clampTemperature(0.5)).toBe(0.5);
      expect(clampTemperature(2)).toBe(2);
      expect(clampTemperature(2.5)).toBe(2);
    });

    it('falls back to 0 for NaN', () => {
      expect(clampTemperature(NaN)).toBe(0);
    });
  });

  describe('systemPromptByteLength', () => {
    it('returns 0 for an empty string', () => {
      expect(systemPromptByteLength('')).toBe(0);
    });

    it('counts bytes (not code units) for multi-byte characters', () => {
      // "é" = 2 bytes in UTF-8, but 1 code unit
      expect(systemPromptByteLength('é')).toBe(2);
      // Emoji is 4 bytes in UTF-8
      expect(systemPromptByteLength('🚀')).toBe(4);
      // ASCII is 1 byte each
      expect(systemPromptByteLength('hello')).toBe(5);
    });

    it('keeps the default prompt placeholder under the API byte limit', () => {
      expect(systemPromptByteLength(DEFAULT_FORMATTING_PROMPT_PLACEHOLDER)).toBeLessThanOrEqual(
        SYSTEM_PROMPT_MAX_BYTES,
      );
    });
  });

  describe('DEFAULT_FORMATTING_PROMPT_PLACEHOLDER', () => {
    it('describes tone only, not output fields (schema instruction is injected automatically)', () => {
      expect(DEFAULT_FORMATTING_PROMPT_PLACEHOLDER).toContain('incident-response writer');
      expect(DEFAULT_FORMATTING_PROMPT_PLACEHOLDER).toContain('output shape is enforced separately');
      expect(DEFAULT_FORMATTING_PROMPT_PLACEHOLDER).not.toContain('Actions taken');
      expect(DEFAULT_FORMATTING_PROMPT_PLACEHOLDER).not.toContain('Recommendations');
    });
  });

  describe('SYSTEM_PROMPT_MAX_BYTES', () => {
    it('matches the 8 KB limit enforced by the backend handler', () => {
      expect(SYSTEM_PROMPT_MAX_BYTES).toBe(8 * 1024);
    });
  });

  describe('DEFAULT_OUTPUT_SCHEMA_EXAMPLE', () => {
    it('is valid JSON', () => {
      expect(() => JSON.parse(DEFAULT_OUTPUT_SCHEMA_EXAMPLE)).not.toThrow();
    });

    it('is a JSON object (not array or scalar)', () => {
      const parsed = JSON.parse(DEFAULT_OUTPUT_SCHEMA_EXAMPLE);
      expect(typeof parsed).toBe('object');
      expect(Array.isArray(parsed)).toBe(false);
      expect(parsed).not.toBeNull();
    });

    it('contains the four built-in keys matching the Go defaultSchemaExample', () => {
      const parsed = JSON.parse(DEFAULT_OUTPUT_SCHEMA_EXAMPLE);
      expect(parsed).toHaveProperty('status');
      expect(parsed).toHaveProperty('summary');
      expect(parsed).toHaveProperty('actions_taken');
      expect(parsed).toHaveProperty('recommendations');
    });

    it('fits within OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES', () => {
      const bytes = new TextEncoder().encode(DEFAULT_OUTPUT_SCHEMA_EXAMPLE).length;
      expect(bytes).toBeLessThanOrEqual(OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES);
    });
  });

  describe('OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES', () => {
    it('matches the 8 KB limit enforced by the backend handler', () => {
      expect(OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES).toBe(8 * 1024);
    });
  });
});
