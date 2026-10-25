import { useEffect, useState, useCallback } from 'react';
import { llmSettingsApi, subscribeApiAvailability, systemApi } from '../api/client';
import { workerState, type WorkerState } from '../components/layout/workerStatus';
import { useInterval } from './useInterval';
import { useLatestRef } from './useLatestRef';

const LLM_POLL_INTERVAL = 60_000;
// Twice per worker heartbeat, so a lost worker shows within ~15s.
const WORKER_POLL_INTERVAL = 15_000;

export interface SystemStatus {
  apiAvailable: boolean;
  // null while the first load is in flight or when it failed: the indicator
  // then shows nothing rather than a guess.
  llm: { configured: boolean; model: string } | null;
  // null until the first answer, or when the API has no status endpoint
  // (an older backend): the indicator then hides the worker.
  worker: WorkerState | null;
}

// useSystemStatus feeds the top-bar status: API reachability (shared state
// from the API client), the agent worker connection and the active LLM config.
export function useSystemStatus(): SystemStatus {
  const [apiAvailable, setApiAvailable] = useState(true);
  const [llm, setLlm] = useState<SystemStatus['llm']>(null);
  const [worker, setWorker] = useState<WorkerState | null>(null);

  useEffect(() => subscribeApiAvailability(setApiAvailable), []);

  const loadLlm = useCallback(async () => {
    try {
      const res = await llmSettingsApi.list();
      const active = res.configs.find((c) => c.id === res.active_id);
      setLlm({ configured: active?.is_configured ?? false, model: active?.model ?? '' });
    } catch {
      setLlm(null);
    }
  }, []);

  const loadWorker = useCallback(async () => {
    try {
      setWorker(workerState(await systemApi.status()));
    } catch {
      setWorker(null);
    }
  }, []);

  const loadLlmRef = useLatestRef(loadLlm);
  const loadWorkerRef = useLatestRef(loadWorker);
  useEffect(() => {
    void loadLlmRef.current();
    void loadWorkerRef.current();
  }, [loadLlmRef, loadWorkerRef]);
  useInterval(() => { void loadLlm(); }, LLM_POLL_INTERVAL);
  useInterval(() => { void loadWorker(); }, WORKER_POLL_INTERVAL);

  return { apiAvailable, llm, worker };
}
