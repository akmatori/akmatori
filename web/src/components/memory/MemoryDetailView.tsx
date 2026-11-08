import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { ArrowLeft, Pencil, Trash2 } from 'lucide-react';
import { memoriesApi } from '../../api/client';
import type { MemoryDetail } from '../../types';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { getErrorMessage } from '../../utils/errors';
import { formatAbsolute, formatRelativeAgo } from '../../utils/format';
import type { MemoryClaim, MemoryEvidence } from '../../types';
import EvidenceBadge from './EvidenceBadge';
import {
  CLAIM_LABELS,
  EVIDENCE_DESCRIPTIONS,
  EVIDENCE_LABELS,
  GLOBAL_SCOPE,
  MEMORY_EVIDENCES,
  MEMORY_TYPES,
  MEMORY_TYPE_LABELS,
  linkifyRefs,
  memoryEvidence,
  usageLabel,
} from './memoryHelpers';

// The API cap on evidence_note (services.MemoryEvidenceNoteMaxLen).
const EVIDENCE_NOTE_MAX = 500;

// The API caps (services.MemoryDescriptionMaxLen / MemoryBodyMaxBytes).
const DESCRIPTION_MAX = 500;
const BODY_MAX_BYTES = 8 * 1024;

// In-app links (memory refs, /context) use the router; anything else opens
// in a new tab. react-markdown already drops unsafe schemes.
const markdownComponents: Components = {
  a: ({ href, children }) =>
    href?.startsWith('/') ? (
      <Link to={href}>{children}</Link>
    ) : (
      <a href={href} target="_blank" rel="noopener noreferrer">
        {children}
      </a>
    ),
};

interface MemoryDetailViewProps {
  id: number;
}

// MemoryDetailView shows one memory with its links resolved, and edits or
// deletes it. Name and scope stay fixed: other memories link to them.
export default function MemoryDetailView({ id }: MemoryDetailViewProps) {
  const navigate = useNavigate();
  const [memory, setMemory] = useState<MemoryDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [busy, setBusy] = useState(false);
  const [description, setDescription] = useState('');
  const [type, setType] = useState('');
  const [body, setBody] = useState('');
  const [evidence, setEvidence] = useState<MemoryEvidence>('observed');
  const [claim, setClaim] = useState<MemoryClaim>('factual');
  const [evidenceNote, setEvidenceNote] = useState('');

  const load = useCallback(async () => {
    try {
      setMemory(await memoriesApi.get(id));
      setError(null);
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load memory'));
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    void load();
  }, [load]);

  const startEdit = (m: MemoryDetail) => {
    setDescription(m.description);
    setType(m.type);
    setBody(m.body);
    setEvidence(memoryEvidence(m));
    setClaim(m.claim || 'factual');
    setEvidenceNote(m.evidence_note ?? '');
    setEditing(true);
  };

  const bodyBytes = new TextEncoder().encode(body).length;
  const noteBytes = new TextEncoder().encode(evidenceNote).length;
  const canSave =
    description.trim() !== '' && description.length <= DESCRIPTION_MAX && bodyBytes <= BODY_MAX_BYTES && noteBytes <= EVIDENCE_NOTE_MAX;

  const save = async () => {
    if (!canSave) return;
    setBusy(true);
    try {
      // body and evidence_note are always sent, so an emptied field clears it.
      await memoriesApi.update(id, {
        description: description.trim(),
        type,
        body,
        evidence,
        claim,
        evidence_note: evidenceNote.trim(),
      });
      setEditing(false);
      await load();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to save memory'));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      await memoriesApi.delete(id);
      navigate('/settings/memory');
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to delete memory'));
      setBusy(false);
    }
  };

  if (loading) return <LoadingSpinner />;

  return (
    <div className="space-y-6">
      <Link to="/settings/memory" className="inline-flex items-center gap-1.5 text-sm text-fg-muted hover:text-fg">
        <ArrowLeft size={15} aria-hidden="true" />
        All memories
      </Link>

      {error && <ErrorMessage message={error} />}

      {memory && (
        <>
          <div className="flex flex-wrap items-start gap-3">
            <div className="min-w-0 flex-1">
              <h2 className="font-mono text-lg text-fg break-all">{memory.name}</h2>
              <p className="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-1 text-sm text-fg-subtle">
                <span>
                  {memory.scope === GLOBAL_SCOPE ? 'Global' : `Skill: ${memory.scope}`} ·{' '}
                  {MEMORY_TYPE_LABELS[memory.type] ?? memory.type} ·{' '}
                  {memory.created_by === 'operator' ? 'Written by an operator' : 'Learned by the agent'} ·
                </span>
                <EvidenceBadge memory={memory} />
                {memory.claim && <span>{CLAIM_LABELS[memory.claim] ?? memory.claim} claim</span>}
              </p>
            </div>
            {!editing && !confirmDelete && (
              <div className="flex gap-2">
                <button type="button" onClick={() => startEdit(memory)} className="btn btn-secondary">
                  <Pencil size={15} aria-hidden="true" />
                  Edit
                </button>
                <button type="button" onClick={() => setConfirmDelete(true)} className="btn btn-secondary">
                  <Trash2 size={15} aria-hidden="true" />
                  Delete
                </button>
              </div>
            )}
            {confirmDelete && (
              <div className="flex items-center gap-2">
                <span className="text-sm text-fg">Delete this memory?</span>
                <button type="button" onClick={() => void remove()} disabled={busy} className="btn btn-danger">
                  Delete
                </button>
                <button type="button" onClick={() => setConfirmDelete(false)} disabled={busy} className="btn btn-secondary">
                  Cancel
                </button>
              </div>
            )}
          </div>

          <dl className="grid grid-cols-1 sm:grid-cols-2 gap-4 text-sm">
            <div>
              <dt className="text-fg-subtle">Last updated</dt>
              <dd className="mt-0.5 text-fg">{formatAbsolute(memory.updated_at)}</dd>
            </div>
            <div>
              <dt className="text-fg-subtle">Use by runs</dt>
              <dd className="mt-0.5 text-fg">{usageLabel(memory, formatRelativeAgo)}</dd>
            </div>
            {memory.evidence_note && (
              <div className="sm:col-span-2">
                <dt className="text-fg-subtle">Evidence</dt>
                <dd className="mt-0.5 text-fg whitespace-pre-wrap break-words">{memory.evidence_note}</dd>
              </div>
            )}
            {memory.incident_uuid && (
              <div>
                <dt className="text-fg-subtle">Source incident</dt>
                <dd className="mt-0.5">
                  <Link to={`/incidents/${memory.incident_uuid}`} className="font-mono text-primary-500 hover:underline">
                    {memory.incident_uuid.slice(0, 8)}
                  </Link>
                </dd>
              </div>
            )}
          </dl>

          {editing ? (
            <div className="space-y-4">
              <div>
                <label htmlFor="memory-description" className="block text-sm font-medium text-fg mb-1.5">
                  Summary
                </label>
                <input
                  id="memory-description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  maxLength={DESCRIPTION_MAX}
                  className="input-field"
                />
              </div>
              <div>
                <label htmlFor="memory-type" className="block text-sm font-medium text-fg mb-1.5">
                  Type
                </label>
                <select id="memory-type" value={type} onChange={(e) => setType(e.target.value)} className="input-field w-auto">
                  {MEMORY_TYPES.map((t) => (
                    <option key={t} value={t}>
                      {MEMORY_TYPE_LABELS[t]}
                    </option>
                  ))}
                </select>
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <div>
                  <label htmlFor="memory-evidence" className="block text-sm font-medium text-fg mb-1.5">
                    Evidence
                  </label>
                  <select
                    id="memory-evidence"
                    value={evidence}
                    onChange={(e) => setEvidence(e.target.value as MemoryEvidence)}
                    className="input-field w-auto"
                  >
                    {MEMORY_EVIDENCES.map((e) => (
                      <option key={e} value={e} title={EVIDENCE_DESCRIPTIONS[e]}>
                        {EVIDENCE_LABELS[e]}
                      </option>
                    ))}
                  </select>
                  <p className="mt-1 text-xs text-fg-subtle">{EVIDENCE_DESCRIPTIONS[evidence]}</p>
                </div>
                <div>
                  <label htmlFor="memory-claim" className="block text-sm font-medium text-fg mb-1.5">
                    Claim
                  </label>
                  <select
                    id="memory-claim"
                    value={claim}
                    onChange={(e) => setClaim(e.target.value as MemoryClaim)}
                    className="input-field w-auto"
                  >
                    <option value="factual">{CLAIM_LABELS.factual}: what is</option>
                    <option value="causal">{CLAIM_LABELS.causal}: X caused Y</option>
                  </select>
                </div>
              </div>
              <div>
                <label htmlFor="memory-evidence-note" className="block text-sm font-medium text-fg mb-1.5">
                  Evidence note
                </label>
                <input
                  id="memory-evidence-note"
                  value={evidenceNote}
                  onChange={(e) => setEvidenceNote(e.target.value)}
                  placeholder="What supports it: tool output, timestamps of cause and effect, the source incident"
                  className="input-field"
                />
                <p className={`mt-1 text-xs ${noteBytes > EVIDENCE_NOTE_MAX ? 'text-state-fail' : 'text-fg-subtle'}`}>
                  {noteBytes} / {EVIDENCE_NOTE_MAX} bytes.
                </p>
              </div>
              <div>
                <label htmlFor="memory-body" className="block text-sm font-medium text-fg mb-1.5">
                  Details
                </label>
                <textarea
                  id="memory-body"
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  rows={12}
                  className="input-field font-mono text-sm resize-y"
                />
                <p className={`mt-1 text-xs ${bodyBytes > BODY_MAX_BYTES ? 'text-state-fail' : 'text-fg-subtle'}`}>
                  Markdown. Link with [[name]], [[scope/name]] or [[file.ext]]. {bodyBytes} / {BODY_MAX_BYTES} bytes.
                </p>
              </div>
              <div className="flex gap-2">
                <button type="button" onClick={() => void save()} disabled={busy || !canSave} className="btn btn-primary">
                  {busy ? 'Saving…' : 'Save'}
                </button>
                <button type="button" onClick={() => setEditing(false)} disabled={busy} className="btn btn-secondary">
                  Cancel
                </button>
              </div>
            </div>
          ) : (
            <>
              <section>
                <h3 className="text-sm text-fg-subtle">Summary</h3>
                <p className="mt-1 text-fg">{memory.description}</p>
              </section>
              {memory.body.trim() && (
                <section>
                  <h3 className="text-sm text-fg-subtle">Details</h3>
                  <div className="md mt-1">
                    <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
                      {linkifyRefs(memory.body, memory.references)}
                    </ReactMarkdown>
                  </div>
                </section>
              )}
            </>
          )}
        </>
      )}
    </div>
  );
}
