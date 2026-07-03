import { useCallback, useEffect, useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { ArrowLeft, Check, X, AlertTriangle } from 'lucide-react';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import ProposalDiff from '../components/proposals/ProposalDiff';
import ProposalChatPanel from '../components/proposals/ProposalChatPanel';
import { proposalsApi } from '../api/client';
import type { Proposal } from '../types';
import {
  kindConfig,
  statusConfig,
  sourceIncidentUUIDs,
} from '../components/proposals/proposalHelpers';

export default function ProposalDetail() {
  const { uuid } = useParams<{ uuid: string }>();
  const [proposal, setProposal] = useState<Proposal | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [actionError, setActionError] = useState('');
  const [acting, setActing] = useState(false);

  const load = useCallback(async () => {
    if (!uuid) return;
    try {
      const data = await proposalsApi.get(uuid);
      setProposal(data);
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load proposal');
    } finally {
      setLoading(false);
    }
  }, [uuid]);

  useEffect(() => {
    load();
  }, [load]);

  const decide = async (action: 'approve' | 'reject') => {
    if (!uuid || acting) return;
    const confirmText =
      action === 'approve'
        ? 'Approve and apply this proposal now? The change takes effect immediately.'
        : 'Reject this proposal?';
    if (!window.confirm(confirmText)) return;
    setActing(true);
    setActionError('');
    try {
      const updated =
        action === 'approve' ? await proposalsApi.approve(uuid) : await proposalsApi.reject(uuid);
      setProposal(updated);
    } catch (err) {
      // Approve conflicts (stale/apply-failed) return the updated row in the
      // error payload server-side; re-fetch so the banner reflects reality.
      setActionError(err instanceof Error ? err.message : `Failed to ${action} proposal`);
      await load();
    } finally {
      setActing(false);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <LoadingSpinner />
      </div>
    );
  }

  if (error || !proposal) {
    return (
      <div className="max-w-4xl mx-auto">
        <BackLink />
        <ErrorMessage message={error || 'Proposal not found'} />
      </div>
    );
  }

  const kc = kindConfig(proposal.kind);
  const sc = statusConfig(proposal.status);
  const evidence = sourceIncidentUUIDs(proposal);
  const actionable = proposal.status === 'pending' || proposal.status === 'apply_failed';

  return (
    <div className="animate-fade-in max-w-6xl mx-auto">
      <BackLink />

      {/* Header */}
      <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 p-6 mb-4">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="flex items-center gap-2 mb-2">
              <span className="badge badge-primary">{kc.label}</span>
              <span className={`badge ${sc.badgeClass}`}>{sc.label}</span>
            </div>
            <h1 className="text-xl font-semibold text-gray-900 dark:text-white">
              {proposal.title}
            </h1>
            <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-gray-500 dark:text-gray-400">
              {proposal.target_ref && (
                <span>
                  {kc.target}: <code className="text-primary-600 dark:text-primary-400">{proposal.target_ref}</code>
                </span>
              )}
              <span>Created {new Date(proposal.created_at).toLocaleString()}</span>
              {proposal.applied_at && (
                <span>Applied {new Date(proposal.applied_at).toLocaleString()}</span>
              )}
            </div>
          </div>
          {actionable && (
            <div className="flex gap-2 flex-shrink-0">
              <button
                onClick={() => decide('reject')}
                disabled={acting}
                className="inline-flex items-center gap-2 px-4 py-2 text-sm rounded-lg border border-gray-200 dark:border-gray-700 text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700/50 disabled:opacity-50 transition-colors"
              >
                <X className="w-4 h-4" />
                Reject
              </button>
              <button
                onClick={() => decide('approve')}
                disabled={acting}
                className="inline-flex items-center gap-2 px-4 py-2 text-sm rounded-lg bg-primary-500 text-white hover:bg-primary-600 disabled:opacity-50 transition-colors"
              >
                <Check className="w-4 h-4" />
                {proposal.status === 'apply_failed' ? 'Retry apply' : 'Approve & apply'}
              </button>
            </div>
          )}
        </div>

        {/* Status banners */}
        {proposal.status === 'superseded' && (
          <Banner tone="warning">
            The target changed after this proposal was created. It was marked superseded and
            cannot be applied — the next evaluator run will re-propose against the current state
            if the change is still worthwhile.
          </Banner>
        )}
        {proposal.status === 'apply_failed' && proposal.apply_error && (
          <Banner tone="error">Apply failed: {proposal.apply_error}</Banner>
        )}
        {actionError && <Banner tone="error">{actionError}</Banner>}

        {/* Reasoning + evidence */}
        {proposal.reasoning && (
          <div className="mt-4">
            <h3 className="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400 mb-1.5">
              Why
            </h3>
            <p className="text-sm text-gray-700 dark:text-gray-300 whitespace-pre-wrap">
              {proposal.reasoning}
            </p>
          </div>
        )}
        {evidence.length > 0 && (
          <div className="mt-4">
            <h3 className="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400 mb-1.5">
              Evidence incidents
            </h3>
            <div className="flex flex-wrap gap-2">
              {evidence.map((iu) => (
                <Link
                  key={iu}
                  to={`/incidents/${iu}`}
                  className="text-xs font-mono px-2 py-1 rounded bg-gray-100 dark:bg-gray-700 text-primary-600 dark:text-primary-400 hover:underline"
                >
                  {iu.slice(0, 8)}
                </Link>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Diff + chat */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 p-6 overflow-y-auto max-h-[calc(100vh-360px)]">
          <h2 className="text-sm font-semibold text-gray-900 dark:text-white mb-4">
            Proposed change
          </h2>
          <ProposalDiff proposal={proposal} />
        </div>
        <div className="bg-white dark:bg-gray-800 rounded-xl shadow-sm border border-gray-200 dark:border-gray-700 flex flex-col max-h-[calc(100vh-360px)]">
          <div className="px-6 py-4 border-b border-gray-200 dark:border-gray-700">
            <h2 className="text-sm font-semibold text-gray-900 dark:text-white">
              Refine with the assistant
            </h2>
          </div>
          <ProposalChatPanel
            proposalUUID={proposal.uuid}
            disabled={!actionable}
            onTurnComplete={load}
          />
        </div>
      </div>
    </div>
  );
}

function BackLink() {
  return (
    <Link
      to="/proposals"
      className="inline-flex items-center gap-2 text-sm text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 mb-6"
    >
      <ArrowLeft className="w-4 h-4" />
      Back to Proposals
    </Link>
  );
}

function Banner({ tone, children }: { tone: 'warning' | 'error'; children: React.ReactNode }) {
  const classes =
    tone === 'warning'
      ? 'bg-amber-50 dark:bg-amber-900/20 border-amber-200 dark:border-amber-800 text-amber-800 dark:text-amber-300'
      : 'bg-red-50 dark:bg-red-900/20 border-red-200 dark:border-red-800 text-red-700 dark:text-red-300';
  return (
    <div className={`mt-4 flex items-start gap-2 rounded-lg border p-3 text-sm ${classes}`}>
      <AlertTriangle className="w-4 h-4 mt-0.5 flex-shrink-0" />
      <span>{children}</span>
    </div>
  );
}
