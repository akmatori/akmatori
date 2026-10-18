import { describe, it, expect } from 'vitest';
import { closedReasonLabel, getIncidentStatusConfig, isFinishedStatus } from './incidentStatus';

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
