import { describe, expect, it } from 'vitest';
import { runStatusLabel, runStatusTone, taskResultPath } from './runHelpers';

describe('runHelpers', () => {
  it('labels and tones run statuses', () => {
    expect(runStatusLabel('running')).toBe('Agent working');
    expect(runStatusLabel('superseded')).toBe('Replaced by a newer run');
    expect(runStatusTone('failed')).toBe('fail');
    expect(runStatusTone('completed')).toBe('ok');
    expect(runStatusTone('superseded')).toBe('closed');
  });

  it('routes a created task to its incident or request', () => {
    expect(taskResultPath({ uuid: 'i1', status: 'pending', event_kind: 'alert' })).toBe('/incidents/i1');
    expect(taskResultPath({ uuid: 'i2', status: 'linked', event_kind: 'alert' })).toBe('/incidents/i2');
    expect(taskResultPath({ uuid: 'r1', status: 'running', event_kind: 'request' })).toBe('/requests/r1');
    expect(taskResultPath({ uuid: '', status: 'recorded', event_kind: 'change' })).toBeNull();
  });
});
