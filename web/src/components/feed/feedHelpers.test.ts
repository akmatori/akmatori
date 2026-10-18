import { describe, it, expect } from 'vitest';
import type { EventFeedItem } from '../../types';
import { bucketSize, eventTone, groupEvents } from './feedHelpers';

const at = (iso: string): EventFeedItem => ({
  event_type: 'alert', event_uuid: iso, title: 't', occurred_at: iso, status: 'firing', incident_uuid: '', correlated: false,
});

describe('bucketSize', () => {
  it('scales with the range', () => {
    expect(bucketSize(3 * 3600)).toBe(15 * 60 * 1000);
    expect(bucketSize(24 * 3600)).toBe(60 * 60 * 1000);
    expect(bucketSize(7 * 86400)).toBe(24 * 60 * 60 * 1000);
  });
});

describe('groupEvents', () => {
  it('groups newest-first events into local 15-minute buckets', () => {
    const now = new Date(2026, 9, 18, 14, 10);
    const items = [
      at(new Date(2026, 9, 18, 14, 4).toISOString()),
      at(new Date(2026, 9, 18, 14, 0, 30).toISOString()),
      at(new Date(2026, 9, 18, 13, 59).toISOString()),
    ];
    const groups = groupEvents(items, 15 * 60 * 1000, now);
    expect(groups.map((g) => [g.label, g.items.length])).toEqual([
      ['14:00 to 14:15', 2],
      ['13:45 to 14:00', 1],
    ]);
  });

  it('adds the day for buckets that are not today', () => {
    const now = new Date(2026, 9, 18, 9, 0);
    const groups = groupEvents([at(new Date(2026, 9, 17, 23, 50).toISOString())], 60 * 60 * 1000, now);
    expect(groups[0].label).toBe('Sat, Oct 17, 23:00 to 00:00');
  });
});

describe('eventTone', () => {
  it('maps statuses', () => {
    expect(eventTone('firing')).toBe('fail');
    expect(eventTone('resolved')).toBe('ok');
    expect(eventTone('running')).toBe('work');
    expect(eventTone('closed')).toBe('closed');
  });
});
