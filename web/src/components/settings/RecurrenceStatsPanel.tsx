import { useState, useEffect } from 'react';
import { ShieldCheck } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { recurrenceStatsApi, memoriesApi } from '../../api/client';
import type { RecurrenceStats, Memory } from '../../types';

interface RecurrenceStatsPanelProps {
  onStatsLoaded?: (stats: RecurrenceStats) => void;
  onSignatureMarked?: () => void;
}

function rateLabel(hits: number, total: number): string {
  if (total === 0) return '0 / 0';
  return `${hits} / ${total}`;
}

export default function RecurrenceStatsPanel({ onStatsLoaded, onSignatureMarked }: RecurrenceStatsPanelProps) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [stats, setStats] = useState<RecurrenceStats | null>(null);
  const [toggling, setToggling] = useState<number | null>(null);

  useEffect(() => {
    loadStats();
  }, []);

  const loadStats = async () => {
    try {
      setLoading(true);
      setError(null);
      const data = await recurrenceStatsApi.get();
      setStats(data);
      onStatsLoaded?.(data);
    } catch (err) {
      setError('Failed to load recurrence stats');
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleMarkAsSignature = async (mem: Memory) => {
    setToggling(mem.id);
    try {
      await memoriesApi.setSuppress(mem.id, true);
      await loadStats();
      onSignatureMarked?.();
    } catch (err) {
      setError('Failed to flag signature');
      console.error(err);
    } finally {
      setToggling(null);
    }
  };

  if (loading) return <LoadingSpinner />;

  return (
    <div className="space-y-5">
      {error && <ErrorMessage message={error} />}

      {/* Fingerprint groups table */}
      <div>
        <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-1">
          Top Recurring Alert Identities (last 7d)
        </h3>
        <p className="text-xs text-gray-500 dark:text-gray-400 mb-3">
          Alert rule + host combinations with the most correlated recurrences. Each avoided
          re-investigation saves ~412k tokens.
        </p>
        {!stats || stats.fingerprint_groups.length === 0 ? (
          <p className="text-sm text-gray-500 dark:text-gray-400 italic">
            No fingerprint recurrences in the last 7 days.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-gray-200 dark:border-gray-700">
                  <th className="pb-2 text-left font-medium text-gray-500 dark:text-gray-400">Alert rule</th>
                  <th className="pb-2 text-left font-medium text-gray-500 dark:text-gray-400">Host</th>
                  <th className="pb-2 text-right font-medium text-gray-500 dark:text-gray-400">Recurrences</th>
                  <th className="pb-2 text-right font-medium text-gray-500 dark:text-gray-400">Est. tokens saved</th>
                </tr>
              </thead>
              <tbody>
                {stats.fingerprint_groups.map((g) => (
                  <tr
                    key={g.fingerprint}
                    className="border-b border-gray-100 dark:border-gray-800"
                  >
                    <td className="py-1.5 pr-3 font-mono text-gray-800 dark:text-gray-200 truncate max-w-[180px]">
                      {g.alert_name || '—'}
                    </td>
                    <td className="py-1.5 pr-3 text-gray-600 dark:text-gray-300 truncate max-w-[150px]">
                      {g.target_host || '—'}
                    </td>
                    <td className="py-1.5 pr-3 text-right text-gray-800 dark:text-gray-200">
                      {g.recurrence_count}
                    </td>
                    <td className="py-1.5 text-right text-green-700 dark:text-green-400 font-medium">
                      {(g.est_tokens_saved / 1000).toFixed(0)}k
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Gate hit-rate numbers */}
      {stats && (
        <div className="border-t border-gray-200 dark:border-gray-700 pt-4">
          <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">
            Gate Hit-Rates
          </h3>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div className="rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2">
              <p className="text-xs text-gray-500 dark:text-gray-400 mb-1">Correlation 24h</p>
              <p className="text-sm font-semibold text-gray-800 dark:text-gray-100">
                {rateLabel(stats.gate_hit_rates.correlation_24h.hits, stats.gate_hit_rates.correlation_24h.total)}
              </p>
            </div>
            <div className="rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2">
              <p className="text-xs text-gray-500 dark:text-gray-400 mb-1">Correlation 7d</p>
              <p className="text-sm font-semibold text-gray-800 dark:text-gray-100">
                {rateLabel(stats.gate_hit_rates.correlation_7d.hits, stats.gate_hit_rates.correlation_7d.total)}
              </p>
            </div>
            <div className="rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2">
              <p className="text-xs text-gray-500 dark:text-gray-400 mb-1">Suppression 24h</p>
              <p className="text-sm font-semibold text-gray-800 dark:text-gray-100">
                {rateLabel(stats.gate_hit_rates.suppression_24h.hits, stats.gate_hit_rates.suppression_24h.total)}
              </p>
            </div>
            <div className="rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2">
              <p className="text-xs text-gray-500 dark:text-gray-400 mb-1">Suppression 7d</p>
              <p className="text-sm font-semibold text-gray-800 dark:text-gray-100">
                {rateLabel(stats.gate_hit_rates.suppression_7d.hits, stats.gate_hit_rates.suppression_7d.total)}
              </p>
            </div>
          </div>
        </div>
      )}

      {/* Candidate signatures quick-action */}
      {stats && stats.candidate_signatures.length > 0 && (
        <div className="border-t border-gray-200 dark:border-gray-700 pt-4">
          <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-1">
            Candidate Signatures (last 7d)
          </h3>
          <p className="text-xs text-gray-500 dark:text-gray-400 mb-3">
            Recent incident-pattern and feedback memories not yet flagged as suppressions.
          </p>
          <div className="space-y-2">
            {stats.candidate_signatures.map((m) => (
              <div
                key={m.id}
                className="flex items-start justify-between gap-3 rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-xs font-mono font-semibold text-gray-800 dark:text-gray-200 truncate">
                      {m.name}
                    </span>
                    <span className="text-xs text-gray-500 dark:text-gray-400">{m.scope}</span>
                  </div>
                  <p className="text-xs text-gray-600 dark:text-gray-300 mt-0.5 line-clamp-2">
                    {m.description}
                  </p>
                </div>
                <button
                  onClick={() => handleMarkAsSignature(m)}
                  disabled={toggling === m.id}
                  className="shrink-0 flex items-center gap-1 text-xs text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-200 disabled:opacity-50"
                  title="Mark as suppression signature"
                >
                  <ShieldCheck size={14} />
                  Mark as signature
                </button>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
