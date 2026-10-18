import { useCallback, useEffect, useState } from 'react';
import { incidentsApi, proposalsApi } from '../api/client';
import { getErrorMessage } from '../utils/errors';
import type { Incident, Proposal } from '../types';
import { useInterval } from './useInterval';
import { useLatestRef } from './useLatestRef';

const POLL_INTERVAL = 15_000;
const DAY_SECONDS = 24 * 60 * 60;
const COLUMN_LIMIT = 8;
// A running incident with no update for this long is shown as stalled.
const STALL_MS = 30 * 60 * 1000;

export interface OverviewData {
  // Work that waits for an operator: proposals to review, diagnosed
  // incidents, alerts that still fire after the agent finished, stalled runs
  // and recent failed investigations.
  latestProposal: Proposal | null;
  needsAttention: Incident[];
  working: Incident[];
  monitoring: Incident[];
  day: { incidents: number; failed: number; monitoring: number; proposals: number };
}

// useOverviewData loads the Overview columns and refreshes them every 15s.
// Count-only queries ask for one row and read pagination.total.
export function useOverviewData() {
  const [data, setData] = useState<OverviewData | null>(null);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    const now = Math.floor(Date.now() / 1000);
    const dayAgo = now - DAY_SECONDS;
    try {
      const [proposals, attention, failed, active, monitoring, dayAll] = await Promise.all([
        proposalsApi.list('pending', undefined, 1, 1),
        incidentsApi.list(undefined, undefined, 1, COLUMN_LIMIT, undefined, 'diagnosed,alert_active'),
        incidentsApi.list(dayAgo, now, 1, COLUMN_LIMIT, undefined, 'failed'),
        incidentsApi.list(undefined, undefined, 1, COLUMN_LIMIT, undefined, 'pending,running'),
        incidentsApi.list(undefined, undefined, 1, COLUMN_LIMIT, undefined, 'monitor'),
        incidentsApi.list(dayAgo, now, 1, 1),
      ]);
      const isStalled = (i: Incident) =>
        i.status === 'running' && Date.now() - new Date(i.updated_at).getTime() > STALL_MS;
      setData({
        latestProposal: proposals.data[0] ?? null,
        needsAttention: [...attention.data, ...active.data.filter(isStalled), ...failed.data].sort(
          (a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime(),
        ),
        working: active.data.filter((i) => !isStalled(i)),
        monitoring: monitoring.data,
        day: {
          incidents: dayAll.pagination.total,
          failed: failed.pagination.total,
          monitoring: monitoring.pagination.total,
          proposals: proposals.pagination.total,
        },
      });
      setError('');
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load overview'));
    }
  }, []);

  const loadRef = useLatestRef(load);
  useEffect(() => {
    void loadRef.current();
  }, [loadRef]);
  useInterval(() => { void load(); }, POLL_INTERVAL);

  return { data, error, reload: load };
}
