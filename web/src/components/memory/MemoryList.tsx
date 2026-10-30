import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Brain, Download, Search, Send, X } from 'lucide-react';
import { memoriesApi } from '../../api/client';
import type { Memory } from '../../types';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import EmptyState from '../shared/EmptyState';
import { getErrorMessage } from '../../utils/errors';
import { formatRelativeAgo } from '../../utils/format';
import { GLOBAL_SCOPE, MEMORY_TYPE_LABELS, compareScopes, filterMemories, groupByScope, memoryHref } from './memoryHelpers';

interface MemoryListProps {
  // When set, only memories that originated from this incident are shown.
  incidentUuid: string;
}

// MemoryList is the Settings → Memory overview: the "Tell Akmatori" input,
// a search box, export, and every memory grouped by scope.
export default function MemoryList({ incidentUuid }: MemoryListProps) {
  const [memories, setMemories] = useState<Memory[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [exporting, setExporting] = useState(false);

  const load = useCallback(async () => {
    try {
      const data = await memoriesApi.list(incidentUuid ? { incidentUuid } : undefined);
      setMemories(data);
      setError(null);
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load memories'));
    } finally {
      setLoading(false);
    }
  }, [incidentUuid]);

  useEffect(() => {
    void load();
  }, [load]);

  const groups = useMemo(() => groupByScope(filterMemories(memories, query)), [memories, query]);
  const scopes = useMemo(() => {
    const set = new Set(memories.map((m) => m.scope));
    set.add(GLOBAL_SCOPE);
    return [...set].sort(compareScopes);
  }, [memories]);

  const handleExport = async () => {
    setExporting(true);
    try {
      const blob = await memoriesApi.exportZip();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `akmatori-memory-${new Date().toISOString().slice(0, 10)}.zip`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to export memories'));
    } finally {
      setExporting(false);
    }
  };

  if (loading) return <LoadingSpinner />;

  return (
    <div className="space-y-6">
      {error && <ErrorMessage message={error} />}

      {!incidentUuid && <TellAkmatori scopes={scopes} onCreated={load} />}

      {incidentUuid && (
        <div className="flex items-center gap-3 px-4 py-3 rounded-lg bg-surface-2 text-sm">
          <span className="text-fg">
            Memories from incident{' '}
            <Link to={`/incidents/${incidentUuid}`} className="font-mono text-primary-500 hover:underline">
              {incidentUuid.slice(0, 8)}
            </Link>
          </span>
          <Link to="/settings/memory" className="ml-auto flex items-center gap-1 text-fg-muted hover:text-fg">
            <X size={14} aria-hidden="true" />
            Show all
          </Link>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-3">
        <label className="relative flex-1 min-w-48">
          <span className="sr-only">Search memories</span>
          <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-fg-subtle pointer-events-none" aria-hidden="true" />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search memories"
            className="input-field h-9 !pl-8"
          />
        </label>
        <button type="button" onClick={handleExport} disabled={exporting || memories.length === 0} className="btn btn-secondary">
          <Download size={15} aria-hidden="true" />
          {exporting ? 'Exporting…' : 'Export'}
        </button>
      </div>

      {groups.length === 0 ? (
        <EmptyState
          icon={Brain}
          title={memories.length === 0 ? 'No memories yet' : 'No memories match your search'}
          description={
            memories.length === 0
              ? 'The agent saves what it learns from incidents here. You can also tell it something above.'
              : undefined
          }
        />
      ) : (
        groups.map((group) => (
          <section key={group.scope} aria-labelledby={`memory-scope-${group.scope}`}>
            <h2 id={`memory-scope-${group.scope}`} className="mb-2 text-sm font-semibold text-fg">
              {group.scope === GLOBAL_SCOPE ? 'Global' : group.scope}
              <span className="ml-2 font-normal text-fg-subtle">{group.memories.length}</span>
            </h2>
            <ul className="divide-y divide-line border border-line rounded-lg">
              {group.memories.map((m) => (
                <li key={m.id}>
                  <Link to={memoryHref(m.id)} className="flex items-start gap-4 px-4 py-3 hover:bg-surface-2 transition-colors">
                    <div className="min-w-0 flex-1">
                      <p className="font-mono text-sm text-fg truncate">{m.name}</p>
                      <p className="mt-0.5 text-sm text-fg-muted line-clamp-2">{m.description}</p>
                    </div>
                    <div className="flex-shrink-0 text-right text-xs text-fg-subtle space-y-1">
                      <p>{MEMORY_TYPE_LABELS[m.type] ?? m.type}</p>
                      <p>
                        {m.created_by === 'operator' ? 'Operator' : 'Agent'} · {formatRelativeAgo(m.updated_at)}
                      </p>
                    </div>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        ))
      )}
    </div>
  );
}

interface TellAkmatoriProps {
  scopes: string[];
  onCreated: () => void;
}

// TellAkmatori turns a free-text note into an operator memory. The server
// drafts the name, summary and type, so the operator only writes the fact.
function TellAkmatori({ scopes, onCreated }: TellAkmatoriProps) {
  const [text, setText] = useState('');
  const [scope, setScope] = useState(GLOBAL_SCOPE);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState<Memory | null>(null);

  const submit = async () => {
    const trimmed = text.trim();
    if (!trimmed || saving) return;
    setSaving(true);
    setError('');
    try {
      const created = await memoriesApi.fromText(trimmed, scope);
      setSaved(created);
      setText('');
      onCreated();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to save memory'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="border border-line rounded-lg p-4">
      <label htmlFor="tell-akmatori" className="block text-sm font-medium text-fg">
        Tell Akmatori something
      </label>
      <p className="mt-0.5 text-xs text-fg-subtle">
        A fact or an instruction for future investigations. Link other memories with [[name]] and
        uploaded files with [[file.ext]].
      </p>
      <textarea
        id="tell-akmatori"
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          setSaved(null);
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) void submit();
        }}
        rows={3}
        placeholder="e.g. Redis prod listens on 16379, not 6379."
        className="input-field mt-2 resize-y"
      />
      {error && <p className="mt-1 text-xs text-state-fail">{error}</p>}
      <div className="mt-2 flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-2 text-sm text-fg-muted">
          Scope
          <select value={scope} onChange={(e) => setScope(e.target.value)} className="input-field w-auto py-1">
            {scopes.map((s) => (
              <option key={s} value={s}>
                {s === GLOBAL_SCOPE ? 'Global' : s}
              </option>
            ))}
          </select>
        </label>
        {saved && (
          <p className="text-sm text-state-ok">
            Saved as{' '}
            <Link to={memoryHref(saved.id)} className="font-mono underline">
              {saved.name}
            </Link>
          </p>
        )}
        <button
          type="button"
          onClick={() => void submit()}
          disabled={saving || !text.trim()}
          className="btn btn-primary ml-auto"
        >
          <Send size={15} aria-hidden="true" />
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  );
}
