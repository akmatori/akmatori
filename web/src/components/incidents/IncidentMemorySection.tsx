import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Brain, RefreshCw } from 'lucide-react';
import type { Incident, Memory } from '../../types';
import { incidentsApi, memoriesApi } from '../../api/client';
import { getErrorMessage } from '../../utils/errors';
import { formatRelativeAgo } from '../../utils/format';
import EvidenceBadge from '../memory/EvidenceBadge';
import { memoryHref } from '../memory/memoryHelpers';
import { TONE_CLASSES } from './incidentStatus';
import { canRerunExtraction, extractionState } from './memoryExtractionHelpers';

interface IncidentMemorySectionProps {
  incident: Incident;
  // Called after the rerun was queued, so the page refreshes the incident.
  onChanged: () => void | Promise<void>;
}

// IncidentMemorySection is the "Memory" part of a closed incident's summary:
// the state of the post-incident memory pass, a button to run it again, the
// timeline it extracted and the memories it recorded for this incident.
export default function IncidentMemorySection({ incident, onChanged }: IncidentMemorySectionProps) {
  const [memories, setMemories] = useState<Memory[]>([]);
  const [rerunning, setRerunning] = useState(false);
  const [error, setError] = useState('');
  const state = extractionState(incident);
  const tone = TONE_CLASSES[state.tone];
  const extraction = incident.memory_extraction ?? '';

  // Reload the memories whenever the pass reaches an outcome.
  useEffect(() => {
    let cancelled = false;
    memoriesApi
      .list({ incidentUuid: incident.uuid })
      .then((list) => {
        if (!cancelled) setMemories(list);
      })
      .catch(() => {
        // The list is optional; the state line still shows.
      });
    return () => {
      cancelled = true;
    };
  }, [incident.uuid, extraction]);

  const rerun = async () => {
    setRerunning(true);
    setError('');
    try {
      await incidentsApi.extractMemory(incident.uuid);
      await onChanged();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to queue the memory pass'));
    } finally {
      setRerunning(false);
    }
  };

  return (
    <section aria-labelledby="incident-memory-title" className="mt-6 border-t border-line pt-5">
      <div className="flex flex-wrap items-center gap-3">
        <h3 id="incident-memory-title" className="flex items-center gap-2 text-sm font-semibold text-fg">
          <Brain size={15} className="text-primary-500" aria-hidden="true" />
          Memory
        </h3>
        <span className={`inline-flex items-center gap-1.5 text-sm ${tone.text}`}>
          <span className={`w-2 h-2 rounded-full ${tone.dot} ${state.tone === 'work' ? 'animate-pulse' : ''}`} aria-hidden="true" />
          {state.label}
        </span>
        {incident.memory_extraction_at && (
          <span className="text-xs text-fg-subtle">{formatRelativeAgo(incident.memory_extraction_at)}</span>
        )}
        {canRerunExtraction(incident) && (
          <button
            type="button"
            onClick={() => void rerun()}
            disabled={rerunning}
            title="Read the closed incident again and record its findings as memories. Memories from an earlier pass are updated in place."
            className="btn btn-secondary ml-auto h-8 px-3 text-xs inline-flex items-center gap-1.5"
          >
            <RefreshCw size={14} aria-hidden="true" />
            {rerunning ? 'Queuing…' : extraction ? 'Run again' : 'Run memory pass'}
          </button>
        )}
      </div>
      {state.detail && <p className="mt-1 text-sm text-fg-muted break-words">{state.detail}</p>}
      {error && <p className="mt-2 px-3 py-2 rounded-md bg-state-fail/10 text-sm text-state-fail">{error}</p>}

      {incident.memory_timeline && (
        <div className="mt-4">
          <h4 className="text-xs font-medium uppercase tracking-wide text-fg-subtle">Timeline</h4>
          <pre className="mt-1 whitespace-pre-wrap break-words rounded-lg bg-surface-2 px-3 py-2 text-sm text-fg">
            {incident.memory_timeline}
          </pre>
        </div>
      )}

      {memories.length > 0 && (
        <div className="mt-4">
          <h4 className="text-xs font-medium uppercase tracking-wide text-fg-subtle">
            {memories.length === 1 ? '1 memory from this incident' : `${memories.length} memories from this incident`}
          </h4>
          <ul className="mt-1 divide-y divide-line rounded-lg border border-line">
            {memories.map((m) => (
              <li key={m.id}>
                <Link to={memoryHref(m.id)} className="flex items-start gap-3 px-3 py-2 hover:bg-surface-2 transition-colors">
                  <div className="min-w-0 flex-1">
                    <p className="flex items-center gap-2 min-w-0">
                      <span className="font-mono text-sm text-fg truncate">
                        {m.scope === 'global' ? m.name : `${m.scope}/${m.name}`}
                      </span>
                      <EvidenceBadge memory={m} />
                    </p>
                    <p className="mt-0.5 text-sm text-fg-muted line-clamp-2">{m.description}</p>
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
