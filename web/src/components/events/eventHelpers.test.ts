import { describe, it, expect } from 'vitest';
import type { EventFeedItem } from '../../types';
import { bucketSize, classificationSummary, eventTone, eventWork, groupEvents } from './eventHelpers';

const at = (iso: string): EventFeedItem => ({
  event_uuid: iso, kind: 'alert', source_kind: 'webhook', classification: 'llm',
  title: 't', occurred_at: iso, status: 'firing', correlated: false,
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

describe('classificationSummary', () => {
  const base = at('2026-10-27T10:00:00Z');
  it('describes each classification path', () => {
    expect(classificationSummary({ ...base, kind: 'change', classification: 'llm', classification_confidence: 0.92 }))
      .toBe('Classified as Change by the LLM (92%)');
    expect(classificationSummary({ ...base, kind: 'alert', classification: 'fallback' }))
      .toBe('Alert by fallback: classification was not available or not confident');
    expect(classificationSummary({ ...base, kind: 'request', classification: 'source' }))
      .toBe('Request by source: no classification needed');
    expect(classificationSummary({ ...base, kind: 'info', classification: 'reused' }))
      .toBe('Info: same item as an event classified in the last 24 hours, no new LLM call');
    expect(classificationSummary({ ...base, classification: 'backfill' }))
      .toBe('Alert: recorded before event classification existed');
  });
});

describe('eventWork', () => {
  it('links a request event to its request work, else to its incident', () => {
    expect(eventWork({ scope_uuid: 's1', incident_uuid: undefined })).toEqual({
      kind: 'request', uuid: 's1', path: '/requests/s1', key: 'request:s1',
    });
    expect(eventWork({ incident_uuid: 'i1' })?.path).toBe('/incidents/i1');
    expect(eventWork({})).toBeNull();
  });
});
