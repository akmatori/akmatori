import type { Memory } from '../../types';
import { CLAIM_LABELS, EVIDENCE_BADGE_CLASSES, EVIDENCE_DESCRIPTIONS, EVIDENCE_LABELS, memoryEvidence } from './memoryHelpers';

interface EvidenceBadgeProps {
  memory: Pick<Memory, 'evidence' | 'claim' | 'evidence_note'>;
}

// EvidenceBadge shows how far a memory can be trusted. The tooltip carries
// the claim kind and the evidence note.
export default function EvidenceBadge({ memory }: EvidenceBadgeProps) {
  const evidence = memoryEvidence(memory);
  const parts = [EVIDENCE_DESCRIPTIONS[evidence]];
  if (memory.claim) parts.push(`Claim: ${CLAIM_LABELS[memory.claim] ?? memory.claim}.`);
  if (memory.evidence_note) parts.push(memory.evidence_note);
  return (
    <span
      title={parts.join(' ')}
      className={`inline-flex items-center rounded px-1.5 py-0.5 text-xs font-medium ${EVIDENCE_BADGE_CLASSES[evidence]}`}
    >
      {EVIDENCE_LABELS[evidence]}
    </span>
  );
}
