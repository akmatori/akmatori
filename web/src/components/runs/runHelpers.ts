import type { IncidentTone } from '../incidents/incidentStatus';

// runStatusLabel names a run or request status for the UI.
export function runStatusLabel(status: string): string {
  switch (status) {
    case 'running':
      return 'Agent working';
    case 'completed':
      return 'Completed';
    case 'failed':
      return 'Failed';
    case 'superseded':
      return 'Replaced by a newer run';
    default:
      return status || 'Unknown';
  }
}

// runStatusTone maps a run or request status onto the incident tones.
export function runStatusTone(status: string): IncidentTone {
  switch (status) {
    case 'running':
      return 'work';
    case 'failed':
      return 'fail';
    case 'completed':
      return 'ok';
    default:
      return 'closed';
  }
}

// requestPath is the page of request work.
export function requestPath(uuid: string): string {
  return `/requests/${uuid}`;
}

// taskResultPath is where the UI goes after POST /api/incidents: the
// incident a reported problem opened or joined, or the request work a request
// started. A task stored as a change or info event has no page (null).
export function taskResultPath(res: { uuid: string; status: string; event_kind?: string }): string | null {
  if (!res.uuid || res.status === 'recorded') return null;
  if (res.event_kind === 'alert' || res.status === 'linked' || res.status === 'pending') {
    return `/incidents/${res.uuid}`;
  }
  return requestPath(res.uuid);
}
