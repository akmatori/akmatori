import type { Proposal, ProposalKind, ProposalStatus } from '../../types';
import { builtinToolLabel, sortBuiltinTools } from '../cron/cronJobHelpers';

export interface KindConfig {
  label: string;
  target: string;
  isUpdate: boolean;
}

const KIND_CONFIG: Record<ProposalKind, KindConfig> = {
  runbook_new: { label: 'New runbook', target: 'Runbook', isUpdate: false },
  runbook_update: { label: 'Runbook update', target: 'Runbook', isUpdate: true },
  memory_new: { label: 'New memory', target: 'Memory', isUpdate: false },
  memory_update: { label: 'Memory update', target: 'Memory', isUpdate: true },
  cron_new: { label: 'New cron job', target: 'Cron job', isUpdate: false },
  cron_update: { label: 'Cron job update', target: 'Cron job', isUpdate: true },
  skill_prompt_update: { label: 'Skill prompt update', target: 'Skill', isUpdate: true },
};

export function kindConfig(kind: ProposalKind): KindConfig {
  return KIND_CONFIG[kind] ?? { label: kind, target: 'Target', isUpdate: false };
}

export function statusConfig(status: ProposalStatus): { label: string; badgeClass: string } {
  switch (status) {
    case 'pending':
      return { label: 'Pending', badgeClass: 'badge-warning' };
    case 'approved':
      return { label: 'Applied', badgeClass: 'badge-success' };
    case 'rejected':
      return { label: 'Rejected', badgeClass: 'badge-default' };
    case 'apply_failed':
      return { label: 'Apply failed', badgeClass: 'badge-error' };
    case 'superseded':
      return { label: 'Superseded', badgeClass: 'badge-default' };
    default:
      return { label: status, badgeClass: 'badge-default' };
  }
}

// Field labels rendered in the diff panel, in display order per kind.
const FIELD_ORDER: Record<string, string[]> = {
  runbook: ['title', 'content'],
  memory: ['scope', 'type', 'name', 'description', 'body'],
  // tool_logical_names is the legacy shape; it renders only when a row
  // still carries it (empty fields are skipped).
  cron: ['name', 'schedule', 'prompt', 'tools_enabled', 'builtin_tools', 'tool_logical_names'],
  skill_prompt: ['skill_name', 'prompt'],
};

export function contentFamily(kind: ProposalKind): string {
  if (kind.startsWith('runbook')) return 'runbook';
  if (kind.startsWith('memory')) return 'memory';
  if (kind.startsWith('cron')) return 'cron';
  return 'skill_prompt';
}

export function fieldOrder(kind: ProposalKind): string[] {
  return FIELD_ORDER[contentFamily(kind)] ?? [];
}

const FIELD_LABELS: Record<string, string> = {
  tools_enabled: 'Connector tools',
  builtin_tools: 'Built-in tools',
};

export function fieldLabel(field: string): string {
  if (FIELD_LABELS[field]) return FIELD_LABELS[field];
  return field.replace(/_/g, ' ').replace(/^./, (c) => c.toUpperCase());
}

// parseContent decodes the per-kind JSON document stored on the proposal.
// Returns an empty object for empty/invalid JSON so the diff panel degrades
// to "(empty)" cells rather than crashing.
export function parseContent(raw: string): Record<string, unknown> {
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw);
    return typeof parsed === 'object' && parsed !== null ? parsed : {};
  } catch {
    return {};
  }
}

export function fieldToText(value: unknown): string {
  if (value === undefined || value === null) return '';
  if (Array.isArray(value)) return value.join(', ');
  if (typeof value === 'string') return value;
  return JSON.stringify(value);
}

// fieldValueText is fieldToText with per-field formatting for the cron tool
// fields: tools_enabled renders as Allowed / Not allowed, and builtin_tools
// renders labels in display order ("None" for an empty list).
export function fieldValueText(field: string, value: unknown): string {
  if (value === undefined || value === null) return '';
  if (field === 'tools_enabled' && typeof value === 'boolean') {
    return value ? 'Allowed' : 'Not allowed';
  }
  if (field === 'builtin_tools' && Array.isArray(value)) {
    const names = value.filter((v): v is string => typeof v === 'string');
    const known = sortBuiltinTools(names);
    const unknown = names.filter((n) => !(known as string[]).includes(n));
    const all = [...known.map(builtinToolLabel), ...unknown];
    return all.length === 0 ? 'None' : all.join(', ');
  }
  return fieldToText(value);
}

export function sourceIncidentUUIDs(p: Proposal): string[] {
  const uuids = p.source_incident_uuids?.uuids;
  return Array.isArray(uuids) ? uuids : [];
}

export function formatAge(dateStr: string): string {
  const then = new Date(dateStr).getTime();
  if (Number.isNaN(then)) return '-';
  const diffMs = Date.now() - then;
  const minutes = Math.floor(diffMs / 60_000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}
