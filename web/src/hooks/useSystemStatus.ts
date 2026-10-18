import { useEffect, useState, useCallback } from 'react';
import { llmSettingsApi, subscribeApiAvailability } from '../api/client';
import { useInterval } from './useInterval';
import { useLatestRef } from './useLatestRef';

const LLM_POLL_INTERVAL = 60_000;

export interface SystemStatus {
  apiAvailable: boolean;
  // null while the first load is in flight or when it failed: the indicator
  // then shows nothing rather than a guess.
  llm: { configured: boolean; model: string } | null;
}

// useSystemStatus feeds the top-bar status: API reachability (shared state
// from the API client) and the active LLM config. Worker connectivity is not
// exposed by the API yet.
export function useSystemStatus(): SystemStatus {
  const [apiAvailable, setApiAvailable] = useState(true);
  const [llm, setLlm] = useState<SystemStatus['llm']>(null);

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

  const loadLlmRef = useLatestRef(loadLlm);
  useEffect(() => {
    void loadLlmRef.current();
  }, [loadLlmRef]);
  useInterval(() => { void loadLlm(); }, LLM_POLL_INTERVAL);

  return { apiAvailable, llm };
}
