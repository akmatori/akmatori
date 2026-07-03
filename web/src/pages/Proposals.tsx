import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Lightbulb, RefreshCw } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { proposalsApi } from '../api/client';
import type { Proposal, ProposalStatus } from '../types';
import {
  kindConfig,
  statusConfig,
  sourceIncidentUUIDs,
  formatAge,
} from '../components/proposals/proposalHelpers';

const STATUS_TABS: { value: ProposalStatus; label: string }[] = [
  { value: 'pending', label: 'Pending' },
  { value: 'approved', label: 'Applied' },
  { value: 'rejected', label: 'Rejected' },
  { value: 'apply_failed', label: 'Failed' },
  { value: 'superseded', label: 'Superseded' },
];

export default function Proposals() {
  const navigate = useNavigate();
  const [status, setStatus] = useState<ProposalStatus>('pending');
  const [proposals, setProposals] = useState<Proposal[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      setLoading(true);
      const res = await proposalsApi.list(status);
      setProposals(res.data ?? []);
      setTotal(res.pagination?.total ?? 0);
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load proposals');
    } finally {
      setLoading(false);
    }
  }, [status]);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <div className="animate-fade-in">
      <PageHeader
        title="Proposals"
        description="Self-improvement suggestions generated from past incidents and operator feedback. Review, refine in chat, then approve to apply."
        action={
          <button
            onClick={load}
            className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded-lg border border-gray-200 dark:border-gray-700 text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700/50 transition-colors"
          >
            <RefreshCw className="w-4 h-4" />
            Refresh
          </button>
        }
      />

      {error && <ErrorMessage message={error} onDismiss={() => setError('')} />}

      {/* Status filter tabs */}
      <div className="flex items-center gap-1 mb-4 border-b border-gray-200 dark:border-gray-700">
        {STATUS_TABS.map((tab) => (
          <button
            key={tab.value}
            onClick={() => setStatus(tab.value)}
            className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
              status === tab.value
                ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200'
            }`}
          >
            {tab.label}
            {status === tab.value && total > 0 && (
              <span className="ml-2 text-xs text-gray-400">({total})</span>
            )}
          </button>
        ))}
      </div>

      {loading ? (
        <div className="flex items-center justify-center min-h-[300px]">
          <LoadingSpinner />
        </div>
      ) : proposals.length === 0 ? (
        <div className="text-center py-16 bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700">
          <Lightbulb className="w-10 h-10 mx-auto text-gray-300 dark:text-gray-600 mb-3" />
          <p className="text-gray-500 dark:text-gray-400">
            {status === 'pending'
              ? 'No pending proposals. Enable the improvement-evaluator cron job to generate them from recent incidents.'
              : `No ${STATUS_TABS.find((t) => t.value === status)?.label.toLowerCase()} proposals.`}
          </p>
        </div>
      ) : (
        <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-gray-200 dark:border-gray-700 text-left text-xs uppercase tracking-wide text-gray-500 dark:text-gray-400">
                <th className="px-4 py-3 font-medium">Type</th>
                <th className="px-4 py-3 font-medium">Title</th>
                <th className="px-4 py-3 font-medium">Target</th>
                <th className="px-4 py-3 font-medium">Evidence</th>
                <th className="px-4 py-3 font-medium">Age</th>
                <th className="px-4 py-3 font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {proposals.map((p) => {
                const kc = kindConfig(p.kind);
                const sc = statusConfig(p.status);
                const evidence = sourceIncidentUUIDs(p).length;
                return (
                  <tr
                    key={p.uuid}
                    onClick={() => navigate(`/proposals/${p.uuid}`)}
                    className="border-b border-gray-100 dark:border-gray-700/50 last:border-0 cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700/30 transition-colors"
                  >
                    <td className="px-4 py-3 whitespace-nowrap">
                      <span className="badge badge-primary">{kc.label}</span>
                    </td>
                    <td className="px-4 py-3 font-medium text-gray-900 dark:text-white">
                      {p.title}
                    </td>
                    <td className="px-4 py-3 text-gray-500 dark:text-gray-400 whitespace-nowrap">
                      {p.target_ref || '—'}
                    </td>
                    <td className="px-4 py-3 text-gray-500 dark:text-gray-400 whitespace-nowrap">
                      {evidence > 0 ? `${evidence} incident${evidence > 1 ? 's' : ''}` : '—'}
                    </td>
                    <td className="px-4 py-3 text-gray-500 dark:text-gray-400 whitespace-nowrap">
                      {formatAge(p.created_at)}
                    </td>
                    <td className="px-4 py-3 whitespace-nowrap">
                      <span className={`badge ${sc.badgeClass}`}>{sc.label}</span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
