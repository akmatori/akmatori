import { describe, it, expect } from 'vitest';
import {
  SCHEDULE_PRESETS,
  ADVANCED_SCHEDULE_VALUE,
  matchesPreset,
  parseCron,
  validateCronExpression,
  nextRun,
  formatRelativeTime,
  lastRunBadge,
  runStatusLabel,
  EMPTY_CRON_FORM,
  formStateFromJob,
} from './cronJobHelpers';
import * as helpers from './cronJobHelpers';
import type { CronJob } from '../../types';

const makeJob = (overrides: Partial<CronJob>): CronJob => ({
  id: overrides.id ?? 1,
  uuid: overrides.uuid ?? 'cron-1',
  name: overrides.name ?? 'Test cron',
  schedule: overrides.schedule ?? '*/5 * * * *',
  prompt: overrides.prompt ?? 'do the thing',
  is_system: overrides.is_system ?? false,
  channel_id: overrides.channel_id ?? null,
  enabled: overrides.enabled ?? true,
  post_results: overrides.post_results ?? true,
  last_run_at: overrides.last_run_at ?? null,
  last_run_status: overrides.last_run_status ?? '',
  last_run_error: overrides.last_run_error ?? '',
  next_run_at: overrides.next_run_at ?? null,
  created_at: '',
  updated_at: '',
  channel: overrides.channel ?? null,
  tools: overrides.tools ?? [],
});

describe('SCHEDULE_PRESETS', () => {
  it('exposes the common 5-field expressions used by the form dropdown', () => {
    const values = SCHEDULE_PRESETS.map((p) => p.value);
    expect(values).toEqual(
      expect.arrayContaining(['*/5 * * * *', '0 * * * *', '0 9 * * *', '0 9 * * 1']),
    );
  });

  it('every preset is a valid cron expression so picking one always passes validation', () => {
    for (const preset of SCHEDULE_PRESETS) {
      expect(validateCronExpression(preset.value).valid).toBe(true);
    }
  });
});

describe('matchesPreset', () => {
  it('returns the preset value when the spec matches one verbatim', () => {
    expect(matchesPreset('*/5 * * * *')).toBe('*/5 * * * *');
  });

  it('returns the advanced sentinel when no preset matches so the form switches to raw input', () => {
    expect(matchesPreset('17 4 * * *')).toBe(ADVANCED_SCHEDULE_VALUE);
  });

  it('trims input before comparison so accidental whitespace still matches', () => {
    expect(matchesPreset('  */5 * * * *  ')).toBe('*/5 * * * *');
  });
});

describe('legacy mode/description helpers are removed', () => {
  // Task 6 of the cron redesign drops oneshot mode and the description field
  // entirely. The helper module must no longer expose MODE_OPTIONS / modeLabel,
  // because keeping them around invites the manager/form to bind to dead state.
  it('exports no MODE_OPTIONS', () => {
    expect((helpers as Record<string, unknown>).MODE_OPTIONS).toBeUndefined();
  });

  it('exports no modeLabel', () => {
    expect((helpers as Record<string, unknown>).modeLabel).toBeUndefined();
  });

  it('EMPTY_CRON_FORM has no description or mode fields', () => {
    const form = EMPTY_CRON_FORM as unknown as Record<string, unknown>;
    expect(form.description).toBeUndefined();
    expect(form.mode).toBeUndefined();
  });
});

describe('parseCron', () => {
  it('accepts wildcards, steps, ranges, lists, and literal numbers', () => {
    expect(parseCron('* * * * *')).not.toBeNull();
    expect(parseCron('*/15 * * * *')).not.toBeNull();
    expect(parseCron('0,15,30,45 * * * *')).not.toBeNull();
    expect(parseCron('0 9-17 * * 1-5')).not.toBeNull();
    expect(parseCron('0 0 1 1 *')).not.toBeNull();
  });

  it('rejects expressions with wrong field count', () => {
    expect(parseCron('* * * *')).toBeNull();
    expect(parseCron('* * * * * *')).toBeNull();
  });

  it('rejects out-of-range numbers per field', () => {
    expect(parseCron('60 * * * *')).toBeNull();
    expect(parseCron('* 24 * * *')).toBeNull();
    expect(parseCron('* * 0 * *')).toBeNull();
    expect(parseCron('* * 32 * *')).toBeNull();
    expect(parseCron('* * * 13 *')).toBeNull();
    expect(parseCron('* * * * 7')).toBeNull();
  });

  it('rejects malformed ranges and zero/negative steps', () => {
    expect(parseCron('* * * * 5-1')).toBeNull();
    expect(parseCron('*/0 * * * *')).toBeNull();
    expect(parseCron('abc * * * *')).toBeNull();
  });
});

describe('validateCronExpression', () => {
  it('flags empty input with a clear message so the form does not POST it', () => {
    const v = validateCronExpression('');
    expect(v.valid).toBe(false);
    expect(v.message).toMatch(/required/i);
  });

  it('flags malformed input with a hint about the 5-field grammar', () => {
    const v = validateCronExpression('not a cron');
    expect(v.valid).toBe(false);
    expect(v.message).toMatch(/5 fields/i);
  });

  it('passes well-formed expressions', () => {
    expect(validateCronExpression('*/5 * * * *').valid).toBe(true);
    expect(validateCronExpression('0 9 * * 1-5').valid).toBe(true);
  });
});

describe('nextRun', () => {
  it('returns the next minute matching the schedule', () => {
    const from = new Date('2026-05-18T10:03:30Z');
    const next = nextRun('*/5 * * * *', from);
    expect(next).not.toBeNull();
    expect(next!.getTime()).toBeGreaterThan(from.getTime());
    expect(next!.getMinutes() % 5).toBe(0);
  });

  it('returns null for invalid expressions so callers can suppress the preview', () => {
    expect(nextRun('not a cron')).toBeNull();
  });

  it('honors specific hours and days', () => {
    const from = new Date('2026-05-18T08:00:00Z');
    const next = nextRun('0 9 * * *', from);
    expect(next).not.toBeNull();
    expect(next!.getHours()).toBe(9);
    expect(next!.getMinutes()).toBe(0);
  });
});

describe('formatRelativeTime', () => {
  const now = new Date('2026-05-18T10:00:00Z');

  it('formats seconds-scale offsets', () => {
    const t = new Date(now.getTime() + 30 * 1000);
    expect(formatRelativeTime(t, now)).toBe('in 30s');
  });

  it('formats minute-scale offsets with singular/plural', () => {
    expect(formatRelativeTime(new Date(now.getTime() + 60 * 1000), now)).toBe('in 1 minute');
    expect(formatRelativeTime(new Date(now.getTime() + 5 * 60 * 1000), now)).toBe('in 5 minutes');
  });

  it('formats hour and day offsets', () => {
    expect(formatRelativeTime(new Date(now.getTime() + 2 * 3600 * 1000), now)).toBe('in 2 hours');
    expect(formatRelativeTime(new Date(now.getTime() + 3 * 86400 * 1000), now)).toBe('in 3 days');
  });

  it('returns "now" for past or zero offsets so the preview does not flash negatives', () => {
    expect(formatRelativeTime(new Date(now.getTime() - 5000), now)).toBe('now');
  });
});

describe('lastRunBadge', () => {
  const now = new Date('2026-05-18T10:00:00Z');

  it('renders Pending for enabled jobs that have not yet fired but have a next_run_at', () => {
    const badge = lastRunBadge(
      makeJob({
        enabled: true,
        last_run_at: null,
        next_run_at: new Date(now.getTime() + 5 * 60 * 1000).toISOString(),
      }),
      now,
    );
    expect(badge.kind).toBe('pending');
    expect(badge.detail).toMatch(/Next/);
  });

  it('renders Never for never-run disabled jobs', () => {
    const badge = lastRunBadge(makeJob({ enabled: false, last_run_at: null }), now);
    expect(badge.kind).toBe('never');
  });

  it('renders Error and surfaces the last error message as the detail', () => {
    const badge = lastRunBadge(
      makeJob({
        last_run_at: new Date(now.getTime() - 60 * 1000).toISOString(),
        last_run_status: 'error',
        last_run_error: 'provider unavailable',
      }),
      now,
    );
    expect(badge.kind).toBe('error');
    expect(badge.detail).toBe('provider unavailable');
  });

  it('renders OK for successful runs', () => {
    const badge = lastRunBadge(
      makeJob({
        last_run_at: new Date(now.getTime() - 60 * 1000).toISOString(),
        last_run_status: 'ok',
      }),
      now,
    );
    expect(badge.kind).toBe('ok');
    expect(badge.className).toContain('success');
  });
});

describe('EMPTY_CRON_FORM', () => {
  it('uses a valid default schedule so the next-run preview renders immediately', () => {
    expect(validateCronExpression(EMPTY_CRON_FORM.schedule).valid).toBe(true);
  });

  it('starts with no tools and enabled=true', () => {
    expect(EMPTY_CRON_FORM.tool_instance_ids).toEqual([]);
    expect(EMPTY_CRON_FORM.enabled).toBe(true);
  });
});

describe('formStateFromJob', () => {
  it('maps the row into the form shape and pulls channel_uuid from the embedded channel', () => {
    const state = formStateFromJob(
      makeJob({
        name: 'Morning digest',
        schedule: '0 9 * * *',
        prompt: 'Summarise last 24 hours',
        enabled: false,
        channel: {
          id: 1,
          uuid: 'ch-uuid',
          integration_id: 1,
          external_id: 'C_X',
          display_name: '#sre',
          can_post: true,
          can_listen: false,
          is_default_post: false,
          extraction_prompt: '',
          process_bot_messages: true,
          process_human_messages: false,
          enabled: true,
          created_at: '',
          updated_at: '',
        },
      }),
    );
    expect(state.name).toBe('Morning digest');
    expect(state.channel_uuid).toBe('ch-uuid');
    expect(state.enabled).toBe(false);
    expect(state.tool_instance_ids).toEqual([]);
  });

  it('leaves channel_uuid null when the job has no channel association', () => {
    const state = formStateFromJob(makeJob({ channel: null }));
    expect(state.channel_uuid).toBeNull();
  });

  it('lifts the assigned tool IDs into tool_instance_ids in order', () => {
    const state = formStateFromJob(
      makeJob({
        tools: [
          { id: 7, name: 'zbx', logical_name: 'zbx', tool_type: 'zabbix', enabled: true },
          { id: 3, name: 'grafana', logical_name: 'grafana', tool_type: 'grafana', enabled: true },
        ],
      }),
    );
    expect(state.tool_instance_ids).toEqual([7, 3]);
  });

  it('treats a row with no tools field as an empty allowlist (defensive against older API responses)', () => {
    const job = makeJob({});
    // Simulate an older / partial server response that omitted the tools array.
    const stripped = { ...job, tools: undefined as unknown as CronJob['tools'] };
    const state = formStateFromJob(stripped as CronJob);
    expect(state.tool_instance_ids).toEqual([]);
  });
});

describe('runStatusLabel', () => {
  it('maps recognized statuses', () => {
    expect(runStatusLabel('ok')).toBe('Success');
    expect(runStatusLabel('error')).toBe('Error');
  });

  it('falls back to em-dash for empty / unknown statuses', () => {
    expect(runStatusLabel('')).toBe('—');
    expect(runStatusLabel('mystery')).toBe('—');
  });
});
