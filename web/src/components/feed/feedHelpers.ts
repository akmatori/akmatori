import type { EventFeedItem } from '../../types';
import type { IncidentTone } from '../incidents/incidentStatus';
import { formatClock } from '../../utils/format';

export interface EventGroup {
  key: string;
  label: string;
  items: EventFeedItem[];
}

const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

// bucketSize picks a group width that keeps the number of groups readable:
// 15 minutes for ranges up to 3 hours, 1 hour up to 2 days, else 1 day.
export function bucketSize(rangeSeconds: number): number {
  if (rangeSeconds <= 3 * 3600) return 15 * MINUTE;
  if (rangeSeconds <= 2 * 86400) return HOUR;
  return DAY;
}

function dayLabel(d: Date): string {
  return d.toLocaleDateString('en-US', { weekday: 'short', month: 'short', day: 'numeric' });
}

// groupEvents splits newest-first events into time buckets of `sizeMs`,
// aligned to the local clock. Labels read "14:00 to 14:15" (the day is added
// when it is not today) or "Sat, Oct 18" for day buckets.
export function groupEvents(items: EventFeedItem[], sizeMs: number, now = new Date()): EventGroup[] {
  const groups: EventGroup[] = [];
  for (const item of items) {
    const t = new Date(item.occurred_at);
    let start: Date;
    if (sizeMs >= DAY) {
      start = new Date(t.getFullYear(), t.getMonth(), t.getDate());
    } else {
      const midnight = new Date(t.getFullYear(), t.getMonth(), t.getDate()).getTime();
      start = new Date(midnight + Math.floor((t.getTime() - midnight) / sizeMs) * sizeMs);
    }
    const key = String(start.getTime());
    let group = groups[groups.length - 1];
    if (!group || group.key !== key) {
      let label: string;
      if (sizeMs >= DAY) {
        label = dayLabel(start);
      } else {
        const end = new Date(start.getTime() + sizeMs);
        label = `${formatClock(start.toISOString())} to ${formatClock(end.toISOString())}`;
        if (start.toDateString() !== now.toDateString()) label = `${dayLabel(start)}, ${label}`;
      }
      group = { key, label, items: [] };
      groups.push(group);
    }
    group.items.push(item);
  }
  return groups;
}

// eventTone colors a feed row by what the event means now: firing or failed
// is red, finished or resolved is green, agent work in flight is blue.
export function eventTone(status: string): IncidentTone {
  switch (status) {
    case 'firing':
    case 'failed':
      return 'fail';
    case 'resolved':
    case 'completed':
    case 'monitor':
      return 'ok';
    case 'pending':
    case 'running':
      return 'work';
    case 'diagnosed':
      return 'need';
    default:
      return 'closed';
  }
}

// eventStatusLabel is the short status word shown on a feed row.
export function eventStatusLabel(status: string): string {
  switch (status) {
    case 'firing': return 'Firing';
    case 'resolved': return 'Resolved';
    case 'completed': return 'Completed';
    case 'monitor': return 'Monitoring';
    case 'pending': return 'Queued';
    case 'running': return 'Agent working';
    case 'diagnosed': return 'Needs action';
    case 'failed': return 'Failed';
    case 'closed': return 'Closed';
    default: return status;
  }
}
