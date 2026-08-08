import { describe, it, expect } from 'vitest';
import {
  EMPTY_SAMPLING_FORM,
  dehydrateSamplingForCreate,
  dehydrateSamplingForUpdate,
  describeSampling,
  hasSamplingOverrides,
  hydrateSampling,
  validateSampling,
} from './samplingParamsHelpers';

describe('hydrateSampling', () => {
  it('turns unset (null) values into blank boxes', () => {
    expect(hydrateSampling({ temperature: null, top_p: null, top_k: null, max_tokens: null }))
      .toEqual(EMPTY_SAMPLING_FORM);
  });

  it('keeps 0 visible rather than collapsing it to blank', () => {
    const form = hydrateSampling({ temperature: 0 });
    expect(form.temperature).toBe('0');
    expect(hasSamplingOverrides(form)).toBe(true);
  });
});

describe('validateSampling', () => {
  it('accepts an all-blank form — the default state', () => {
    expect(validateSampling(EMPTY_SAMPLING_FORM)).toBeNull();
  });

  it('accepts in-range values including the bounds', () => {
    expect(validateSampling({ temperature: '0', top_p: '1', top_k: '1', max_tokens: '1' })).toBeNull();
    expect(validateSampling({ ...EMPTY_SAMPLING_FORM, temperature: '2' })).toBeNull();
  });

  it('rejects out-of-range values', () => {
    expect(validateSampling({ ...EMPTY_SAMPLING_FORM, temperature: '2.1' })).toMatch(/Temperature/);
    expect(validateSampling({ ...EMPTY_SAMPLING_FORM, top_p: '1.1' })).toMatch(/Top P/);
    expect(validateSampling({ ...EMPTY_SAMPLING_FORM, top_k: '0' })).toMatch(/Top K/);
    expect(validateSampling({ ...EMPTY_SAMPLING_FORM, max_tokens: '0' })).toMatch(/Max Tokens/);
  });

  it('rejects non-numeric and fractional integer fields', () => {
    expect(validateSampling({ ...EMPTY_SAMPLING_FORM, temperature: 'warm' })).toMatch(/number/);
    expect(validateSampling({ ...EMPTY_SAMPLING_FORM, top_k: '2.5' })).toMatch(/whole number/);
  });
});

describe('dehydrateSamplingForCreate', () => {
  it('omits blank fields so the row is created with NULLs', () => {
    expect(dehydrateSamplingForCreate(EMPTY_SAMPLING_FORM)).toEqual({});
  });

  it('sends 0 rather than treating it as blank', () => {
    expect(dehydrateSamplingForCreate({ ...EMPTY_SAMPLING_FORM, temperature: '0' }))
      .toEqual({ temperature: 0 });
  });
});

describe('dehydrateSamplingForUpdate', () => {
  it('sends explicit nulls for blanks so clearing an override sticks', () => {
    expect(dehydrateSamplingForUpdate(EMPTY_SAMPLING_FORM)).toEqual({
      temperature: null,
      top_p: null,
      top_k: null,
      max_tokens: null,
    });
  });

  it('survives JSON.stringify with the nulls intact', () => {
    const body = JSON.parse(
      JSON.stringify(dehydrateSamplingForUpdate({ ...EMPTY_SAMPLING_FORM, top_p: '0.9' })),
    );
    expect(body).toHaveProperty('temperature', null);
    expect(body).toHaveProperty('top_p', 0.9);
  });
});

describe('describeSampling', () => {
  it('is empty when nothing is overridden', () => {
    expect(describeSampling({ temperature: null, top_p: null, top_k: null, max_tokens: null })).toBe('');
  });

  it('lists only the overridden parameters', () => {
    expect(describeSampling({ temperature: 0, top_p: null, top_k: 40, max_tokens: null }))
      .toBe('temp 0 · top_k 40');
  });
});
