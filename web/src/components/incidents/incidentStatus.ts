import { Activity, Clock, CheckCircle, AlertCircle, XCircle, GitMerge, type LucideIcon } from 'lucide-react';
import { formatCountdown, formatRelativeShort } from '../../utils/format';
import type { Incident } from '../../types';

export interface IncidentStatusConfig {
  class: string;
  icon: LucideIcon;
  label: string;
  subLabel: string | undefined;
}

// closedReasonLabel explains why an incident closed. An incident that closed
// with no visible cause reads as data loss, so a closed badge carries why.
// Rows closed before closed_reason existed have no value and show "Closed".
export function closedReasonLabel(reason?: string): string | undefined {
  switch (reason) {
    case 'auto_stale':
      return 'Auto-closed — inactive';
    case 'monitor_expired':
      return 'Monitor window ended';
    case 'manual':
      return 'Closed by operator';
    default:
      return undefined;
  }
}

// getIncidentStatusConfig maps an incident's status (plus the fields that
// qualify it) to the badge shown in the Incidents list and detail modal.
export function getIncidentStatusConfig(
  status: string,
  monitorUntil?: string,
  sourceKind?: string,
  closedReason?: string,
): IncidentStatusConfig {
  switch (status) {
    case 'completed':
      if (sourceKind === 'alert') {
        // An alert-sourced incident only stays "completed" while at least
        // one linked alert is still firing (see UpdateIncidentComplete /
        // ResolveAlertTx) — it gets promoted to "monitor" the moment every
        // alert clears. Treat it as ongoing, still-open work (also shown
        // in the Open view via the alert_active status filter token).
        return { class: 'badge-primary', icon: Activity, label: 'Ongoing', subLabel: undefined };
      }
      return { class: 'badge-success', icon: CheckCircle, label: 'Completed', subLabel: undefined };
    case 'monitor':
      return {
        class: 'badge-success',
        icon: CheckCircle,
        label: 'Monitoring',
        subLabel: monitorUntil ? formatCountdown(monitorUntil) : undefined,
      };
    case 'closed':
      return {
        class: 'badge-default',
        icon: XCircle,
        label: 'Closed',
        subLabel: closedReasonLabel(closedReason),
      };
    case 'merged':
      return { class: 'badge-default', icon: GitMerge, label: 'Merged', subLabel: undefined };
    case 'pending':
    case 'running':
      return { class: 'badge-primary', icon: Activity, label: 'Ongoing', subLabel: undefined };
    case 'diagnosed':
      return { class: 'badge-purple', icon: Activity, label: 'Ongoing', subLabel: undefined };
    case 'failed':
      return { class: 'badge-error', icon: AlertCircle, label: 'Failed', subLabel: undefined };
    default:
      return { class: 'badge-default', icon: Clock, label: 'Pending', subLabel: undefined };
  }
}

// isFinishedStatus reports whether an incident has produced a final response
// (and so shows execution metrics instead of the auto-refresh toggle).
export function isFinishedStatus(status: string): boolean {
  return status === 'completed' || status === 'monitor' || status === 'failed';
}

// A running incident with no update for this long is shown as stalled: the
// worker died or lost the run, and nobody else will notice.
export const STALL_MS = 30 * 60 * 1000;

export function isStalled(incident: Pick<Incident, 'status' | 'updated_at'>): boolean {
  return incident.status === 'running' && Date.now() - new Date(incident.updated_at).getTime() > STALL_MS;
}

export type IncidentTone = 'work' | 'need' | 'fail' | 'ok' | 'closed';

export interface IncidentState {
  tone: IncidentTone;
  label: string;
}

// Literal class names per tone so Tailwind picks them up: the left rail of a
// list row, the state text, and the state dot.
export const TONE_CLASSES: Record<IncidentTone, { rail: string; text: string; dot: string }> = {
  work: { rail: 'border-l-state-work', text: 'text-state-work', dot: 'bg-state-work' },
  need: { rail: 'border-l-state-need', text: 'text-state-need', dot: 'bg-state-need' },
  fail: { rail: 'border-l-state-fail', text: 'text-state-fail', dot: 'bg-state-fail' },
  ok: { rail: 'border-l-state-ok', text: 'text-state-ok', dot: 'bg-state-ok' },
  closed: { rail: 'border-l-state-closed', text: 'text-state-closed', dot: 'bg-state-closed' },
};

// getIncidentState is the split view's state vocabulary: what the incident
// needs from a person, in one short label with a color tone.
export function getIncidentState(
  incident: Pick<Incident, 'status' | 'monitor_until' | 'source_kind' | 'closed_reason'> & Partial<Pick<Incident, 'updated_at'>>,
): IncidentState {
  if (incident.updated_at && isStalled({ status: incident.status, updated_at: incident.updated_at })) {
    return { tone: 'fail', label: `No progress for ${formatRelativeShort(incident.updated_at)}` };
  }
  switch (incident.status) {
    case 'pending':
      return { tone: 'work', label: 'Queued' };
    case 'running':
      return { tone: 'work', label: 'Agent working' };
    case 'diagnosed':
      return { tone: 'need', label: 'Needs action' };
    case 'completed':
      return incident.source_kind === 'alert'
        ? { tone: 'need', label: 'Alert still firing' }
        : { tone: 'ok', label: 'Completed' };
    case 'monitor':
      return {
        tone: 'ok',
        label: incident.monitor_until ? `Monitoring, ${formatCountdown(incident.monitor_until)}` : 'Monitoring',
      };
    case 'failed':
      return { tone: 'fail', label: 'Failed' };
    case 'merged':
      return { tone: 'closed', label: 'Merged' };
    default:
      return { tone: 'closed', label: closedReasonLabel(incident.closed_reason) ?? 'Closed' };
  }
}
