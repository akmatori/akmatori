import { describe, it, expect } from 'vitest';
import { closedReasonLabel, getIncidentState, getIncidentStatusConfig, isFinishedStatus } from './incidentStatus';

describe('closedReasonLabel', () => {
  it('maps known reasons and leaves unknown ones blank', () => {
    expect(closedReasonLabel('auto_stale')).toBe('Auto-closed — inactive');
    expect(closedReasonLabel('monitor_expired')).toBe('Monitor window ended');
    expect(closedReasonLabel('manual')).toBe('Closed by operator');
    expect(closedReasonLabel(undefined)).toBeUndefined();
    expect(closedReasonLabel('something_else')).toBeUndefined();
  });
});

describe('getIncidentStatusConfig', () => {
  it('treats a completed alert-sourced incident as still ongoing', () => {
    expect(getIncidentStatusConfig('completed', undefined, 'alert').label).toBe('Ongoing');
    expect(getIncidentStatusConfig('completed', undefined, 'manual').label).toBe('Completed');
  });

  it('shows the monitor countdown as a sub-label', () => {
    const future = new Date(Date.now() + 30 * 60_000).toISOString();
    const cfg = getIncidentStatusConfig('monitor', future);
    expect(cfg.label).toBe('Monitoring');
    expect(cfg.subLabel).toMatch(/left$/);
    expect(getIncidentStatusConfig('monitor').subLabel).toBeUndefined();
  });

  it('carries the closed reason as a sub-label', () => {
    expect(getIncidentStatusConfig('closed', undefined, undefined, 'manual').subLabel).toBe('Closed by operator');
    expect(getIncidentStatusConfig('closed').subLabel).toBeUndefined();
  });

  it('falls back to Pending for unknown statuses', () => {
    expect(getIncidentStatusConfig('weird').label).toBe('Pending');
  });
});

describe('isFinishedStatus', () => {
  it('is true only for completed, monitor and failed', () => {
    expect(isFinishedStatus('completed')).toBe(true);
    expect(isFinishedStatus('monitor')).toBe(true);
    expect(isFinishedStatus('failed')).toBe(true);
    expect(isFinishedStatus('running')).toBe(false);
    expect(isFinishedStatus('closed')).toBe(false);
  });
});

describe('getIncidentState', () => {
  it('maps statuses to tones', () => {
    expect(getIncidentState({ status: 'running' })).toEqual({ tone: 'work', label: 'Agent working' });
    expect(getIncidentState({ status: 'running', updated_at: new Date().toISOString() }).tone).toBe('work');
    expect(getIncidentState({ status: 'running', updated_at: new Date(Date.now() - 2 * 3600_000).toISOString() }))
      .toEqual({ tone: 'fail', label: 'No progress for 2h' });
    expect(getIncidentState({ status: 'diagnosed' }).tone).toBe('need');
    expect(getIncidentState({ status: 'completed', source_kind: 'alert' })).toEqual({ tone: 'need', label: 'Alert still firing' });
    expect(getIncidentState({ status: 'completed' }).tone).toBe('ok');
    expect(getIncidentState({ status: 'failed' }).tone).toBe('fail');
    expect(getIncidentState({ status: 'closed', closed_reason: 'manual' })).toEqual({ tone: 'closed', label: 'Closed by operator' });
  });
});
