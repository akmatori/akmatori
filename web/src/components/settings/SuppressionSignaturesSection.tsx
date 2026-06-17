import { useState, useEffect } from 'react';
import { ShieldCheck, ShieldOff } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { memoriesApi } from '../../api/client';
import type { Memory } from '../../types';

export default function SuppressionSignaturesSection() {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [signatures, setSignatures] = useState<Memory[]>([]);
  const [candidates, setCandidates] = useState<Memory[]>([]);
  const [toggling, setToggling] = useState<number | null>(null);

  useEffect(() => {
    loadMemories();
  }, []);

  const loadMemories = async () => {
    try {
      setLoading(true);
      setError(null);
      const all = await memoriesApi.list();
      setSignatures(all.filter((m) => m.suppress));
      // Candidate signatures: incident_pattern memories without suppress=true
      setCandidates(all.filter((m) => (m.type === 'incident_pattern' || m.type === 'feedback') && !m.suppress));
    } catch (err) {
      setError('Failed to load memories');
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleToggle = async (mem: Memory, suppress: boolean) => {
    setToggling(mem.id);
    try {
      await memoriesApi.setSuppress(mem.id, suppress);
      await loadMemories();
    } catch (err) {
      setError(`Failed to ${suppress ? 'flag' : 'unflag'} signature`);
      console.error(err);
    } finally {
      setToggling(null);
    }
  };

  if (loading) return <LoadingSpinner />;

  return (
    <div className="space-y-5">
      {error && <ErrorMessage message={error} />}

      {/* Active suppression signatures */}
      <div>
        <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-1">
          Active Suppression Signatures
        </h3>
        <p className="text-xs text-gray-500 dark:text-gray-400 mb-3">
          Memories marked as known false positives. The suppressor matches
          incoming alerts against these before spawning new incidents.
        </p>
        {signatures.length === 0 ? (
          <p className="text-sm text-gray-500 dark:text-gray-400 italic">
            No suppression signatures yet. Flag a candidate below or wait for
            the agent to record a false-positive verdict.
          </p>
        ) : (
          <div className="space-y-2">
            {signatures.map((m) => (
              <div
                key={m.id}
                className="flex items-start justify-between gap-3 rounded-lg border border-green-200 dark:border-green-800 bg-green-50 dark:bg-green-900/20 px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-xs font-mono font-semibold text-green-800 dark:text-green-300 truncate">
                      {m.name}
                    </span>
                    <span className="text-xs text-gray-500 dark:text-gray-400">
                      {m.scope}
                    </span>
                  </div>
                  <p className="text-xs text-gray-600 dark:text-gray-300 mt-0.5 line-clamp-2">
                    {m.description}
                  </p>
                </div>
                <button
                  onClick={() => handleToggle(m, false)}
                  disabled={toggling === m.id}
                  className="shrink-0 flex items-center gap-1 text-xs text-red-600 dark:text-red-400 hover:text-red-800 dark:hover:text-red-200 disabled:opacity-50"
                  title="Remove suppression flag"
                >
                  <ShieldOff size={14} />
                  Unflag
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Candidate signatures */}
      {candidates.length > 0 && (
        <div className="border-t border-gray-200 dark:border-gray-700 pt-4">
          <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-1">
            Candidate Signatures
          </h3>
          <p className="text-xs text-gray-500 dark:text-gray-400 mb-3">
            Incident-pattern memories not yet flagged as suppressions. Review
            and flag any that represent known false positives.
          </p>
          <div className="space-y-2">
            {candidates.map((m) => (
              <div
                key={m.id}
                className="flex items-start justify-between gap-3 rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-xs font-mono font-semibold text-gray-800 dark:text-gray-200 truncate">
                      {m.name}
                    </span>
                    <span className="text-xs text-gray-500 dark:text-gray-400">
                      {m.scope}
                    </span>
                  </div>
                  <p className="text-xs text-gray-600 dark:text-gray-300 mt-0.5 line-clamp-2">
                    {m.description}
                  </p>
                </div>
                <button
                  onClick={() => handleToggle(m, true)}
                  disabled={toggling === m.id}
                  className="shrink-0 flex items-center gap-1 text-xs text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-200 disabled:opacity-50"
                  title="Mark as suppression signature"
                >
                  <ShieldCheck size={14} />
                  Flag
                </button>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
