import { describe, it, expect } from 'vitest';
import { getErrorMessage } from './errors';

describe('getErrorMessage', () => {
  it('returns the message of an Error', () => {
    expect(getErrorMessage(new Error('boom'), 'fallback')).toBe('boom');
  });

  it('falls back when the Error message is empty', () => {
    expect(getErrorMessage(new Error(''), 'fallback')).toBe('fallback');
  });

  it('accepts plain string errors', () => {
    expect(getErrorMessage('plain', 'fallback')).toBe('plain');
  });

  it('falls back for non-error values', () => {
    expect(getErrorMessage(undefined, 'fallback')).toBe('fallback');
    expect(getErrorMessage(null, 'fallback')).toBe('fallback');
    expect(getErrorMessage({ message: 'x' }, 'fallback')).toBe('fallback');
    expect(getErrorMessage(42, 'fallback')).toBe('fallback');
  });
});
