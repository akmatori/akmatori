import type { Memory, MemoryRef } from '../../types';

export const GLOBAL_SCOPE = 'global';

// The memory types the API accepts (services.AllMemoryTypes), in display
// order. A fact is knowledge about the environment; feedback is an operator
// instruction about how the agent must work.
export const MEMORY_TYPES = ['fact', 'feedback'] as const;

export const MEMORY_TYPE_LABELS: Record<string, string> = {
  fact: 'Fact',
  feedback: 'Feedback',
};

export interface MemoryScopeGroup {
  scope: string;
  memories: Memory[];
}

// compareScopes orders "global" first, then skill scopes alphabetically.
export function compareScopes(a: string, b: string): number {
  if (a === b) return 0;
  if (a === GLOBAL_SCOPE) return -1;
  if (b === GLOBAL_SCOPE) return 1;
  return a.localeCompare(b);
}

// groupByScope returns one group per scope: "global" first, then skill scopes
// alphabetically. Inside a group the most recently updated memory comes first.
export function groupByScope(memories: Memory[]): MemoryScopeGroup[] {
  const byScope = new Map<string, Memory[]>();
  for (const m of memories) {
    const list = byScope.get(m.scope);
    if (list) list.push(m);
    else byScope.set(m.scope, [m]);
  }
  return [...byScope.entries()]
    .sort(([a], [b]) => compareScopes(a, b))
    .map(([scope, list]) => ({
      scope,
      memories: [...list].sort((x, y) => y.updated_at.localeCompare(x.updated_at)),
    }));
}

// filterMemories keeps memories whose name, description or body contains the
// query (case-insensitive). An empty query keeps everything.
export function filterMemories(memories: Memory[], query: string): Memory[] {
  const q = query.trim().toLowerCase();
  if (!q) return memories;
  return memories.filter((m) =>
    [m.name, m.description, m.body].some((field) => field.toLowerCase().includes(q)),
  );
}

export function memoryHref(id: number): string {
  return `/settings/memory?memory=${id}`;
}

// linkifyRefs turns each [[...]] in a memory body into Markdown the UI can
// render: an existing memory becomes a link to its detail view, an existing
// context file a link to the context files page, and a missing target stays
// as text marked "(missing)". Invalid refs are left as written.
export function linkifyRefs(body: string, refs: MemoryRef[]): string {
  const byRaw = new Map(refs.map((r) => [r.raw, r]));
  return body.replace(/\[\[([^[\]\n]{1,256})\]\]/g, (whole, inner: string) => {
    const ref = byRaw.get(inner.trim());
    if (!ref || ref.kind === 'invalid') return whole;
    if (!ref.exists) return `\`[[${ref.raw}]]\` _(missing)_`;
    if (ref.kind === 'memory' && ref.memory_id) return `[${ref.raw}](${memoryHref(ref.memory_id)})`;
    if (ref.kind === 'file') return `[${ref.raw}](/context)`;
    return whole;
  });
}
