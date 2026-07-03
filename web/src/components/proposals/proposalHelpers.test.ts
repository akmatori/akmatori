import { describe, it, expect } from 'vitest';
import {
  kindConfig,
  statusConfig,
  contentFamily,
  fieldOrder,
  fieldLabel,
  parseContent,
  fieldToText,
  sourceIncidentUUIDs,
} from './proposalHelpers';
import type { Proposal } from '../../types';

describe('kindConfig', () => {
  it('marks update kinds as updates and new kinds as not', () => {
    expect(kindConfig('runbook_update').isUpdate).toBe(true);
    expect(kindConfig('memory_update').isUpdate).toBe(true);
    expect(kindConfig('cron_update').isUpdate).toBe(true);
    expect(kindConfig('skill_prompt_update').isUpdate).toBe(true);
    expect(kindConfig('runbook_new').isUpdate).toBe(false);
    expect(kindConfig('memory_new').isUpdate).toBe(false);
    expect(kindConfig('cron_new').isUpdate).toBe(false);
  });
});

describe('statusConfig', () => {
  it('maps each status to a label and badge class', () => {
    expect(statusConfig('pending').badgeClass).toBe('badge-warning');
    expect(statusConfig('approved').label).toBe('Applied');
    expect(statusConfig('apply_failed').badgeClass).toBe('badge-error');
    expect(statusConfig('superseded').label).toBe('Superseded');
    expect(statusConfig('rejected').badgeClass).toBe('badge-default');
  });
});

describe('contentFamily / fieldOrder', () => {
  it('groups kinds into content families', () => {
    expect(contentFamily('runbook_new')).toBe('runbook');
    expect(contentFamily('runbook_update')).toBe('runbook');
    expect(contentFamily('memory_update')).toBe('memory');
    expect(contentFamily('cron_new')).toBe('cron');
    expect(contentFamily('skill_prompt_update')).toBe('skill_prompt');
  });

  it('returns per-kind field ordering matching the backend JSON shapes', () => {
    expect(fieldOrder('runbook_new')).toEqual(['title', 'content']);
    expect(fieldOrder('memory_update')).toEqual(['scope', 'type', 'name', 'description', 'body']);
    expect(fieldOrder('cron_update')).toEqual(['name', 'schedule', 'prompt', 'tool_logical_names']);
    expect(fieldOrder('skill_prompt_update')).toEqual(['skill_name', 'prompt']);
  });
});

describe('fieldLabel', () => {
  it('humanizes snake_case field names', () => {
    expect(fieldLabel('tool_logical_names')).toBe('Tool logical names');
    expect(fieldLabel('title')).toBe('Title');
  });
});

describe('parseContent', () => {
  it('parses valid JSON objects', () => {
    expect(parseContent('{"title":"a"}')).toEqual({ title: 'a' });
  });

  it('degrades to an empty object on empty or invalid input', () => {
    expect(parseContent('')).toEqual({});
    expect(parseContent('not json')).toEqual({});
    expect(parseContent('"just a string"')).toEqual({});
    expect(parseContent('null')).toEqual({});
  });
});

describe('fieldToText', () => {
  it('renders strings, arrays, and missing values', () => {
    expect(fieldToText('hello')).toBe('hello');
    expect(fieldToText(['a', 'b'])).toBe('a, b');
    expect(fieldToText(undefined)).toBe('');
    expect(fieldToText(null)).toBe('');
    expect(fieldToText(42)).toBe('42');
  });
});

describe('sourceIncidentUUIDs', () => {
  const base = { uuid: 'p', kind: 'runbook_new', status: 'pending' } as unknown as Proposal;

  it('extracts the uuids array', () => {
    expect(sourceIncidentUUIDs({ ...base, source_incident_uuids: { uuids: ['a', 'b'] } })).toEqual(['a', 'b']);
  });

  it('returns empty for missing or malformed payloads', () => {
    expect(sourceIncidentUUIDs(base)).toEqual([]);
    expect(sourceIncidentUUIDs({ ...base, source_incident_uuids: {} })).toEqual([]);
  });
});
