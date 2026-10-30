import { describe, expect, it } from 'vitest';
import type { Memory, MemoryRef } from '../../types';
import { filterMemories, groupByScope, linkifyRefs } from './memoryHelpers';

function mem(id: number, scope: string, name: string, updated: string, extra: Partial<Memory> = {}): Memory {
  return {
    id,
    scope,
    type: 'host',
    name,
    description: `${name} description`,
    body: '',
    created_at: updated,
    updated_at: updated,
    ...extra,
  };
}

describe('groupByScope', () => {
  it('puts global first, then scopes alphabetically, newest memory first', () => {
    const groups = groupByScope([
      mem(1, 'redis', 'r-old', '2026-10-01T00:00:00Z'),
      mem(2, 'global', 'g', '2026-10-02T00:00:00Z'),
      mem(3, 'postgres', 'p', '2026-10-03T00:00:00Z'),
      mem(4, 'redis', 'r-new', '2026-10-04T00:00:00Z'),
    ]);
    expect(groups.map((g) => g.scope)).toEqual(['global', 'postgres', 'redis']);
    expect(groups[2].memories.map((m) => m.name)).toEqual(['r-new', 'r-old']);
  });

  it('returns no groups for no memories', () => {
    expect(groupByScope([])).toEqual([]);
  });
});

describe('filterMemories', () => {
  const list = [
    mem(1, 'global', 'redis-port', '2026-10-01T00:00:00Z', { body: 'listens on 16379' }),
    mem(2, 'global', 'dns', '2026-10-01T00:00:00Z', { description: 'Resolvers on EDGE hosts' }),
  ];

  it('matches name, description and body case-insensitively', () => {
    expect(filterMemories(list, 'REDIS').map((m) => m.id)).toEqual([1]);
    expect(filterMemories(list, 'edge').map((m) => m.id)).toEqual([2]);
    expect(filterMemories(list, '16379').map((m) => m.id)).toEqual([1]);
  });

  it('keeps everything for an empty query', () => {
    expect(filterMemories(list, '  ')).toBe(list);
  });
});

describe('linkifyRefs', () => {
  const refs: MemoryRef[] = [
    { raw: 'dns-resolvers', kind: 'memory', scope: 'global', name: 'dns-resolvers', memory_id: 7, exists: true },
    { raw: 'topology.pdf', kind: 'file', name: 'topology.pdf', exists: true },
    { raw: 'gone', kind: 'memory', scope: 'global', name: 'gone', exists: false },
    { raw: '../etc/passwd', kind: 'invalid', exists: false },
  ];

  it('links existing targets and marks missing ones', () => {
    const body = 'See [[dns-resolvers]], [[ topology.pdf ]], [[gone]] and [[../etc/passwd]].';
    expect(linkifyRefs(body, refs)).toBe(
      'See [dns-resolvers](/settings/memory?memory=7), [topology.pdf](/context), `[[gone]]` _(missing)_ and [[../etc/passwd]].',
    );
  });

  it('leaves a body without refs unchanged', () => {
    expect(linkifyRefs('plain text', [])).toBe('plain text');
  });
});
