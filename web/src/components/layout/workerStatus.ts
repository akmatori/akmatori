import type { SystemStatusResponse } from '../../types';

// The worker sends a heartbeat every 30s. Three missed heartbeats mean the
// socket is still open but the worker no longer answers (hung event loop,
// stuck LLM call), which looks "connected" yet runs nothing.
export const WORKER_STALE_SECONDS = 90;

export type WorkerState = 'connected' | 'unresponsive' | 'offline';

export function workerState(status: SystemStatusResponse): WorkerState {
  if (!status.worker.connected) return 'offline';
  const age = status.worker.last_message_age_seconds ?? 0;
  return age > WORKER_STALE_SECONDS ? 'unresponsive' : 'connected';
}

export const WORKER_LABEL: Record<WorkerState, string> = {
  connected: 'Worker connected',
  unresponsive: 'Worker not responding',
  offline: 'Worker offline',
};
