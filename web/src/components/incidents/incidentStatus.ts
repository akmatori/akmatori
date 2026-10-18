import { Activity, Clock, CheckCircle, AlertCircle, XCircle, GitMerge, type LucideIcon } from 'lucide-react';
import { formatCountdown } from '../../utils/format';

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
