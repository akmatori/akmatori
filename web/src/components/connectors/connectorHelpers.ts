import type {
  ConnectorCapability,
  ConnectorCatalogEntry,
  ConnectorKind,
  ConnectorState,
  ConnectorSummary,
} from '../../types/connectors';
import type { FormattingRule } from '../../types';

export const CAPABILITY_LABELS: Record<ConnectorCapability, string> = {
  tools: 'Tools',
  alerts: 'Alerts',
  messaging: 'Messaging',
};

export const KIND_LABELS: Record<ConnectorKind, string> = {
  builtin: 'Built-in',
  custom_mcp: 'Custom MCP',
  custom_http: 'Custom HTTP',
};

export const CATEGORY_LABELS: Record<string, string> = {
  monitoring: 'Monitoring',
  incident: 'Incident management',
  infrastructure: 'Infrastructure',
  data: 'Databases',
  messaging: 'Messaging',
  custom: 'Custom',
  other: 'Other',
};

export const CATEGORY_ORDER = ['monitoring', 'incident', 'infrastructure', 'data', 'messaging', 'custom', 'other'];

export interface StatusView {
  label: string;
  tone: 'success' | 'warning' | 'danger' | 'muted';
}

export function statusView(state: ConnectorState): StatusView {
  switch (state) {
    case 'ok':
      return { label: 'Configured', tone: 'success' };
    case 'connected':
      return { label: 'Connected', tone: 'success' };
    case 'disabled':
      return { label: 'Disabled', tone: 'muted' };
    case 'needs_setup':
      return { label: 'Needs setup', tone: 'warning' };
    case 'error':
      return { label: 'Error', tone: 'danger' };
    default:
      return { label: 'Unknown', tone: 'muted' };
  }
}

export function statusBadgeClass(tone: StatusView['tone']): string {
  switch (tone) {
    case 'success':
      return 'badge badge-success';
    case 'warning':
      return 'badge badge-warning';
    case 'danger':
      return 'badge badge-error';
    default:
      return 'badge badge-default';
  }
}

// monogram returns 1–2 letters for a connector tile: "Prometheus
// Alertmanager" -> "PA", "kubernetes" -> "KU".
export function monogram(name: string): string {
  const words = name.trim().split(/[\s_\-.]+/).filter(Boolean);
  if (words.length === 0) return '?';
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
  return (words[0][0] + words[1][0]).toUpperCase();
}

function matches(haystack: string[], query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return haystack.some((h) => h.toLowerCase().includes(q));
}

export function filterConnectors(list: ConnectorSummary[], query: string): ConnectorSummary[] {
  return list.filter((c) => matches([c.name, c.catalog_name, c.catalog_id], query));
}

export interface CatalogGroup {
  category: string;
  label: string;
  entries: ConnectorCatalogEntry[];
}

// groupCatalog filters the catalog by query and groups it by category in
// CATEGORY_ORDER (unknown categories last, alphabetically).
export function groupCatalog(entries: ConnectorCatalogEntry[], query: string): CatalogGroup[] {
  const byCat = new Map<string, ConnectorCatalogEntry[]>();
  for (const e of entries) {
    if (!matches([e.name, e.description, e.id], query)) continue;
    const list = byCat.get(e.category) ?? [];
    list.push(e);
    byCat.set(e.category, list);
  }
  const cats = [...byCat.keys()].sort((a, b) => {
    const ia = CATEGORY_ORDER.indexOf(a);
    const ib = CATEGORY_ORDER.indexOf(b);
    if (ia === -1 && ib === -1) return a.localeCompare(b);
    if (ia === -1) return 1;
    if (ib === -1) return -1;
    return ia - ib;
  });
  return cats.map((c) => ({ category: c, label: CATEGORY_LABELS[c] ?? c, entries: byCat.get(c) ?? [] }));
}

// addPathFor is where "add" from the catalog goes.
export function addPathFor(entry: ConnectorCatalogEntry): string {
  if (entry.kind === 'custom_mcp') return '/connectors?add=mcp';
  return `/connectors/new/${encodeURIComponent(entry.id)}`;
}

// rulesForTarget returns the formatting rules whose simple match fields
// name this channel (destination) or alert source (trigger), plus every
// expression rule that mentions its UUID, in evaluation order. Wildcard
// rules that match everything are returned separately: they apply too,
// unless a more specific rule above them wins.
export function rulesForTarget(
  rules: FormattingRule[],
  target: { channelUUID?: string; sourceUUID?: string },
): { specific: FormattingRule[]; catchAll: FormattingRule[] } {
  const ordered = [...rules].sort((a, b) => a.position - b.position || a.id - b.id);
  const specific: FormattingRule[] = [];
  const catchAll: FormattingRule[] = [];
  for (const r of ordered) {
    if (!r.enabled) continue;
    const expr = r.match_expression?.trim() ?? '';
    if (expr) {
      if ((target.channelUUID && expr.includes(target.channelUUID)) || (target.sourceUUID && expr.includes(target.sourceUUID))) {
        specific.push(r);
      }
      continue;
    }
    const hasAny = r.match_source_kind || r.match_source_uuid || r.match_channel_uuid || r.match_last_skill;
    if (!hasAny) {
      catchAll.push(r);
      continue;
    }
    if (target.channelUUID && r.match_channel_uuid === target.channelUUID) specific.push(r);
    else if (target.sourceUUID && r.match_source_uuid === target.sourceUUID) specific.push(r);
  }
  return { specific, catchAll };
}

export function newRuleLink(target: { channelUUID?: string; sourceUUID?: string; name: string }): string {
  const p = new URLSearchParams({ new: '1', name: target.name });
  if (target.channelUUID) p.set('channel', target.channelUUID);
  if (target.sourceUUID) {
    p.set('source', target.sourceUUID);
    p.set('kind', 'alert');
  }
  return `/settings/formatting?${p.toString()}`;
}

// slugNamespace mirrors the server's DefaultMCPNamespace: "GitHub Issues"
// -> "ext.github_issues". Used for the preview in the add dialog.
export function slugNamespace(name: string): string {
  const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '');
  return `ext.${slug || 'mcp'}`;
}

// isValidRemoteURL accepts absolute http(s) URLs without credentials.
export function isValidRemoteURL(raw: string): boolean {
  try {
    const u = new URL(raw.trim());
    return (u.protocol === 'https:' || u.protocol === 'http:') && !!u.hostname && !u.username && !u.password;
  } catch {
    return false;
  }
}
