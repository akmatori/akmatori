import { describe, it, expect } from 'vitest';
import {
  buildFormattingUpdatePayload,
  clampMaxTokens,
  clampTemperature,
  systemPromptByteLength,
  DEFAULT_FORMATTING_PROMPT_PLACEHOLDER,
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
      });
      expect(payload).toEqual({
        enabled: true,
        system_prompt: 'reformat please',
        max_tokens: 1234,
        temperature: 0.7,
      });
    });

    it('preserves an empty system prompt so the user can opt out of a custom prompt', () => {
      const payload = buildFormattingUpdatePayload({
        enabled: false,
        systemPrompt: '',
        maxTokens: 1500,
        temperature: 0.2,
      });
      expect(payload.system_prompt).toBe('');
      expect(payload.enabled).toBe(false);
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
    it('mentions the four output sections so the placeholder matches the backend default', () => {
      expect(DEFAULT_FORMATTING_PROMPT_PLACEHOLDER).toContain('Status');
      expect(DEFAULT_FORMATTING_PROMPT_PLACEHOLDER).toContain('Summary');
      expect(DEFAULT_FORMATTING_PROMPT_PLACEHOLDER).toContain('Actions taken');
      expect(DEFAULT_FORMATTING_PROMPT_PLACEHOLDER).toContain('Recommendations');
    });
  });

  describe('SYSTEM_PROMPT_MAX_BYTES', () => {
    it('matches the 8 KB limit enforced by the backend handler', () => {
      expect(SYSTEM_PROMPT_MAX_BYTES).toBe(8 * 1024);
    });
  });
});
