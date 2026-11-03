import type { Incident, MemoryExtractionStatus } from '../../types';

export interface ExtractionState {
  label: string;
  // Tone of the status dot: work (in progress), ok, closed (nothing to
  // show), fail.
  tone: 'work' | 'ok' | 'closed' | 'fail';
  // One line under the label; the error or note when there is one.
  detail?: string;
}

// isExtractionInProgress reports whether the post-incident memory pass is
// queued or running (the page polls while it is).
export function isExtractionInProgress(incident: Pick<Incident, 'memory_extraction'>): boolean {
  return incident.memory_extraction === 'pending' || incident.memory_extraction === 'running';
}

// showsMemorySection: the Memory section belongs to a closed incident (the
// pass runs at close) and to any incident the pass has touched.
export function showsMemorySection(incident: Pick<Incident, 'status' | 'memory_extraction'>): boolean {
  return incident.status === 'closed' || !!incident.memory_extraction;
}

// canRerunExtraction: the rerun endpoint takes a closed incident whose pass
// is not pending or running.
export function canRerunExtraction(incident: Pick<Incident, 'status' | 'memory_extraction'>): boolean {
  return incident.status === 'closed' && !isExtractionInProgress(incident);
}

const LABELS: Record<Exclude<MemoryExtractionStatus, ''>, { label: string; tone: ExtractionState['tone'] }> = {
  pending: { label: 'Memory pass queued', tone: 'work' },
  running: { label: 'Memory pass running', tone: 'work' },
  succeeded: { label: 'Memories recorded', tone: 'ok' },
  no_output: { label: 'Nothing durable to record', tone: 'closed' },
  skipped: { label: 'Memory pass skipped', tone: 'closed' },
  failed: { label: 'Memory pass failed', tone: 'fail' },
};

// extractionState turns the incident's extraction fields into a label, a
// tone and a detail line.
export function extractionState(
  incident: Pick<
    Incident,
    'status' | 'memory_extraction' | 'memory_extraction_error' | 'memory_extraction_note' | 'memory_extraction_attempts' | 'memory_extraction_retry_at'
  >,
): ExtractionState {
  const status = incident.memory_extraction;
  if (!status) {
    return {
      label: incident.status === 'closed' ? 'Memory pass not run' : 'Memory pass runs when the incident closes',
      tone: 'closed',
      detail: incident.status === 'closed' ? 'This incident closed before the pass existed. Run it to record its findings.' : undefined,
    };
  }
  const base = LABELS[status];
  const details: string[] = [];
  if (status === 'pending' && (incident.memory_extraction_attempts ?? 0) > 0 && incident.memory_extraction_error) {
    details.push(`Attempt ${incident.memory_extraction_attempts} failed: ${incident.memory_extraction_error}. Retrying.`);
  } else if (incident.memory_extraction_error) {
    details.push(incident.memory_extraction_error);
  }
  if (incident.memory_extraction_note) details.push(incident.memory_extraction_note);
  return { ...base, detail: details.length > 0 ? details.join(' · ') : undefined };
}
