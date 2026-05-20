import type { CronJob, CronRunStatus } from '../../types';

// CronJobFormState is the in-memory shape of the CronJobForm component. Kept
// in this helper module so the manager's save callback can type its input
// without importing the React component file.
export interface CronJobFormState {
  name: string;
  schedule: string;
  prompt: string;
  channel_uuid: string | null;
  enabled: boolean;
  tool_instance_ids: number[];
}

export const EMPTY_CRON_FORM: CronJobFormState = {
  name: '',
  schedule: '*/15 * * * *',
  prompt: '',
  channel_uuid: null,
  enabled: true,
  tool_instance_ids: [],
};

// formStateFromJob lifts an existing CronJob row into the form's local state.
// Centralised so the manager and any future detail surface stay in sync on
// shape changes.
export function formStateFromJob(job: CronJob): CronJobFormState {
  return {
    name: job.name,
    schedule: job.schedule,
    prompt: job.prompt,
    channel_uuid: job.channel?.uuid ?? null,
    enabled: job.enabled,
    tool_instance_ids: (job.tools ?? []).map((t) => t.id),
  };
}

// Schedule presets surfaced in the CronJobForm dropdown. The "advanced" entry
// is sentinel-only; selecting it switches the form to a raw cron text input.
export interface SchedulePreset {
  value: string;
  label: string;
}

export const ADVANCED_SCHEDULE_VALUE = '__advanced__';

export const SCHEDULE_PRESETS: SchedulePreset[] = [
  { value: '*/5 * * * *', label: 'Every 5 minutes' },
  { value: '*/15 * * * *', label: 'Every 15 minutes' },
  { value: '*/30 * * * *', label: 'Every 30 minutes' },
  { value: '0 * * * *', label: 'Hourly (top of the hour)' },
  { value: '0 */6 * * *', label: 'Every 6 hours' },
  { value: '0 9 * * *', label: 'Daily at 09:00' },
  { value: '0 0 * * *', label: 'Daily at midnight' },
  { value: '0 9 * * 1', label: 'Mondays at 09:00' },
];

// matchesPreset returns the matching preset value when the supplied cron spec
// is one of the known presets, or ADVANCED_SCHEDULE_VALUE otherwise. The form
// uses this to decide whether to show the dropdown or the raw cron input when
// loading an existing CronJob for edit.
export function matchesPreset(spec: string): string {
  const trimmed = spec.trim();
  if (SCHEDULE_PRESETS.find((p) => p.value === trimmed)) return trimmed;
  return ADVANCED_SCHEDULE_VALUE;
}

// Cron field parsing — the form lives entirely on the client so we need a
// small parser to validate expressions before hitting the API and to render
// the "Next run at" preview. This implementation supports the standard
// 5-field grammar used by robfig/cron's parser configured server-side:
// minute hour dom month dow, with `*`, `*/N`, `N`, `N-M`, `N-M/N`, comma
// lists. Whitespace handling is forgiving on input.

type FieldDef = {
  min: number;
  max: number;
  index: number;
};

const FIELDS: FieldDef[] = [
  { min: 0, max: 59, index: 0 }, // minute
  { min: 0, max: 23, index: 1 }, // hour
  { min: 1, max: 31, index: 2 }, // dom
  { min: 1, max: 12, index: 3 }, // month
  { min: 0, max: 6, index: 4 }, // dow
];

export interface ParsedCron {
  minute: Set<number>;
  hour: Set<number>;
  dom: Set<number>;
  month: Set<number>;
  dow: Set<number>;
  domRestricted: boolean;
  dowRestricted: boolean;
}

function parseField(spec: string, def: FieldDef): Set<number> | null {
  if (spec === '') return null;
  const result = new Set<number>();
  for (const piece of spec.split(',')) {
    if (piece === '') return null;
    let rangePart = piece;
    let stepPart: string | undefined;
    if (piece.includes('/')) {
      const idx = piece.indexOf('/');
      rangePart = piece.slice(0, idx);
      stepPart = piece.slice(idx + 1);
    }
    let step = 1;
    if (stepPart !== undefined) {
      step = parseInt(stepPart, 10);
      if (!Number.isFinite(step) || step <= 0) return null;
    }
    let start: number;
    let end: number;
    if (rangePart === '*') {
      start = def.min;
      end = def.max;
    } else if (rangePart.includes('-')) {
      const [a, b] = rangePart.split('-');
      start = parseInt(a, 10);
      end = parseInt(b, 10);
    } else {
      start = end = parseInt(rangePart, 10);
    }
    if (!Number.isFinite(start) || !Number.isFinite(end)) return null;
    if (start < def.min || end > def.max || start > end) return null;
    for (let v = start; v <= end; v += step) {
      result.add(v);
    }
  }
  return result;
}

// parseCron returns the parsed spec or null when invalid. Caller passes the
// raw text from the form — we strip outer whitespace and split on runs of
// whitespace, matching the robfig/cron behaviour.
export function parseCron(spec: string): ParsedCron | null {
  const trimmed = spec.trim();
  if (trimmed === '') return null;
  const parts = trimmed.split(/\s+/);
  if (parts.length !== 5) return null;
  const sets: Array<Set<number> | null> = parts.map((p, i) => parseField(p, FIELDS[i]));
  if (sets.some((s) => s === null)) return null;
  return {
    minute: sets[0]!,
    hour: sets[1]!,
    dom: sets[2]!,
    month: sets[3]!,
    dow: sets[4]!,
    domRestricted: parts[2] !== '*',
    dowRestricted: parts[4] !== '*',
  };
}

export interface ScheduleValidation {
  valid: boolean;
  message?: string;
}

// validateCronExpression returns a structured result so the form can render
// a green check / red message line directly. Empty input is treated as
// invalid because every CronJob requires a schedule at write time.
export function validateCronExpression(spec: string): ScheduleValidation {
  const trimmed = spec.trim();
  if (trimmed === '') {
    return { valid: false, message: 'Schedule is required' };
  }
  const parsed = parseCron(trimmed);
  if (!parsed) {
    return {
      valid: false,
      message:
        'Expected 5 fields (minute hour day-of-month month day-of-week) using *, */N, N, N-M, or comma lists',
    };
  }
  return { valid: true };
}

// nextRun computes the next firing time after `from` (default: now). Returns
// null when the expression is invalid or no future tick exists within ~4
// years. Iterates minute-by-minute — the simple form keeps the parser tiny
// and the worst-case (e.g. Feb 29) is still bounded by the day-limit guard.
export function nextRun(spec: string, from: Date = new Date()): Date | null {
  const parsed = parseCron(spec);
  if (!parsed) return null;
  const cursor = new Date(from.getTime());
  cursor.setSeconds(0, 0);
  cursor.setMinutes(cursor.getMinutes() + 1);
  const limit = 4 * 366 * 24 * 60;
  for (let i = 0; i < limit; i++) {
    const minuteOk = parsed.minute.has(cursor.getMinutes());
    const hourOk = parsed.hour.has(cursor.getHours());
    const monthOk = parsed.month.has(cursor.getMonth() + 1);
    const dayMatch = matchesDay(parsed, cursor);
    if (minuteOk && hourOk && monthOk && dayMatch) {
      return new Date(cursor.getTime());
    }
    cursor.setMinutes(cursor.getMinutes() + 1);
  }
  return null;
}

// matchesDay implements the standard cron day-of-month / day-of-week rule:
// when both fields are restricted, either matching triggers the cron. When
// only one is restricted, the unrestricted field is treated as a wildcard.
function matchesDay(parsed: ParsedCron, d: Date): boolean {
  const domOk = parsed.dom.has(d.getDate());
  const dowOk = parsed.dow.has(d.getDay());
  if (parsed.domRestricted && parsed.dowRestricted) {
    return domOk || dowOk;
  }
  return domOk && dowOk;
}

// formatRelativeTime renders a short relative description ("in 5 minutes",
// "in 2 hours", "in 3 days") for the form's next-run preview. Plain absolute
// time stays nearby so operators can sanity-check.
export function formatRelativeTime(target: Date, from: Date = new Date()): string {
  const diffMs = target.getTime() - from.getTime();
  if (diffMs <= 0) return 'now';
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `in ${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `in ${minutes} minute${minutes === 1 ? '' : 's'}`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `in ${hours} hour${hours === 1 ? '' : 's'}`;
  const days = Math.floor(hours / 24);
  return `in ${days} day${days === 1 ? '' : 's'}`;
}

// Last-run badge helpers — kept in this file so the list page and the form's
// own status line stay consistent.
export type LastRunBadgeKind = 'never' | 'ok' | 'error' | 'pending';

export interface LastRunBadge {
  kind: LastRunBadgeKind;
  label: string;
  className: string;
  detail?: string;
}

// lastRunBadge derives the chip rendered in the CronJobsManager table. The
// "pending" state surfaces when a row exists but has not yet been recorded
// (next_run_at set but last_run_at empty) — useful right after creation so
// operators see something other than "Never".
export function lastRunBadge(job: CronJob, now: Date = new Date()): LastRunBadge {
  if (!job.last_run_at) {
    if (job.enabled && job.next_run_at) {
      return {
        kind: 'pending',
        label: 'Pending',
        className: 'badge badge-default',
        detail: `Next: ${formatRelativeTime(new Date(job.next_run_at), now)}`,
      };
    }
    return { kind: 'never', label: 'Never', className: 'badge badge-default' };
  }
  if (job.last_run_status === 'error') {
    return {
      kind: 'error',
      label: 'Error',
      className: 'badge badge-warning',
      detail: job.last_run_error || undefined,
    };
  }
  if (job.last_run_status === 'ok') {
    return { kind: 'ok', label: 'OK', className: 'badge badge-success' };
  }
  return { kind: 'never', label: 'Never', className: 'badge badge-default' };
}

// runStatusLabel maps the raw status string to a short human label.
export function runStatusLabel(status: CronRunStatus | string): string {
  switch (status) {
    case 'ok':
      return 'Success';
    case 'error':
      return 'Error';
    default:
      return '—';
  }
}
