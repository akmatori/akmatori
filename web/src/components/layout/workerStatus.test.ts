import { describe, it, expect } from 'vitest';
import { workerState, WORKER_STALE_SECONDS } from './workerStatus';

describe('workerState', () => {
  it('is offline when no worker is connected', () => {
    expect(workerState({ worker: { connected: false } })).toBe('offline');
  });

  it('is connected while heartbeats arrive', () => {
    expect(workerState({ worker: { connected: true, last_message_age_seconds: 12 } })).toBe('connected');
    expect(workerState({ worker: { connected: true, last_message_age_seconds: WORKER_STALE_SECONDS } })).toBe('connected');
  });

  it('is unresponsive after three missed heartbeats', () => {
    expect(workerState({ worker: { connected: true, last_message_age_seconds: WORKER_STALE_SECONDS + 1 } })).toBe('unresponsive');
  });
});
