// Sampling parameter form helpers for the LLM configuration editor.
//
// Every parameter is optional. Blank means "unset": Akmatori omits it from the
// provider request entirely, which is what an unconfigured model has always
// done. That makes blank meaningfully different from 0 (a valid temperature),
// so the form carries raw strings and only converts at the save boundary.

export type SamplingParamKey = 'temperature' | 'top_p' | 'top_k' | 'max_tokens';

// Bounds mirror the Go constants in internal/database/models_settings.go —
// keep the two in sync. They are deliberately permissive supersets of what any
// single provider accepts; an in-range value a provider dislikes surfaces that
// provider's own error.
export const SAMPLING_BOUNDS: Record<
  SamplingParamKey,
  { min: number; max: number; integer: boolean; label: string }
> = {
  temperature: { min: 0, max: 2, integer: false, label: 'Temperature' },
  top_p: { min: 0, max: 1, integer: false, label: 'Top P' },
  top_k: { min: 1, max: 1000, integer: true, label: 'Top K' },
  max_tokens: { min: 1, max: 1000000, integer: true, label: 'Max Tokens' },
};

/** The four fields as held in form state: '' means unset. */
export type SamplingFormState = Record<SamplingParamKey, string>;

export const EMPTY_SAMPLING_FORM: SamplingFormState = {
  temperature: '',
  top_p: '',
  top_k: '',
  max_tokens: '',
};

/** Turn a stored config (null = unset) into editable strings. */
export function hydrateSampling(
  config: Partial<Record<SamplingParamKey, number | null | undefined>>,
): SamplingFormState {
  const out = { ...EMPTY_SAMPLING_FORM };
  for (const key of Object.keys(SAMPLING_BOUNDS) as SamplingParamKey[]) {
    const value = config[key];
    out[key] = value === null || value === undefined ? '' : String(value);
  }
  return out;
}

/** True when the operator has overridden at least one parameter. */
export function hasSamplingOverrides(form: SamplingFormState): boolean {
  return (Object.keys(SAMPLING_BOUNDS) as SamplingParamKey[]).some((k) => form[k].trim() !== '');
}

/**
 * Validate the whole form. Returns the first error message, or null when every
 * field is either blank or an in-range number.
 */
export function validateSampling(form: SamplingFormState): string | null {
  for (const key of Object.keys(SAMPLING_BOUNDS) as SamplingParamKey[]) {
    const raw = form[key].trim();
    if (raw === '') continue;
    const { min, max, integer, label } = SAMPLING_BOUNDS[key];
    const parsed = Number(raw);
    if (!Number.isFinite(parsed)) {
      return `${label} must be a number, or blank to use the provider default`;
    }
    if (integer && !Number.isInteger(parsed)) {
      return `${label} must be a whole number`;
    }
    if (parsed < min || parsed > max) {
      return `${label} must be between ${min} and ${max}`;
    }
  }
  return null;
}

/**
 * Save payload for a new config: blank fields are omitted so the row is created
 * with the column NULL.
 */
export function dehydrateSamplingForCreate(
  form: SamplingFormState,
): Partial<Record<SamplingParamKey, number>> {
  const out: Partial<Record<SamplingParamKey, number>> = {};
  for (const key of Object.keys(SAMPLING_BOUNDS) as SamplingParamKey[]) {
    const raw = form[key].trim();
    if (raw !== '') out[key] = Number(raw);
  }
  return out;
}

/**
 * Save payload for an edit: every field is sent, with blank as an explicit null
 * so clearing a box actually clears the stored override. (An omitted key would
 * leave the old value in place.)
 */
export function dehydrateSamplingForUpdate(
  form: SamplingFormState,
): Record<SamplingParamKey, number | null> {
  const out = {} as Record<SamplingParamKey, number | null>;
  for (const key of Object.keys(SAMPLING_BOUNDS) as SamplingParamKey[]) {
    const raw = form[key].trim();
    out[key] = raw === '' ? null : Number(raw);
  }
  return out;
}

/**
 * Short summary for the config list rows, e.g. "temp 0.2 · top_p 0.9".
 * Empty string when nothing is overridden.
 */
export function describeSampling(
  config: Partial<Record<SamplingParamKey, number | null | undefined>>,
): string {
  const labels: Record<SamplingParamKey, string> = {
    temperature: 'temp',
    top_p: 'top_p',
    top_k: 'top_k',
    max_tokens: 'max_tokens',
  };
  const parts: string[] = [];
  for (const key of Object.keys(SAMPLING_BOUNDS) as SamplingParamKey[]) {
    const value = config[key];
    if (value !== null && value !== undefined) parts.push(`${labels[key]} ${value}`);
  }
  return parts.join(' · ');
}
