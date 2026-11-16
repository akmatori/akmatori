import type { FormattingRule, FormattingRuleUpdate } from '../../types';
import {
  DEFAULT_FORMATTING_PROMPT_PLACEHOLDER,
  DEFAULT_OUTPUT_SCHEMA_EXAMPLE,
  dehydrateField,
  hydrateField,
} from './formattingSettingsHelpers';
import { substituteUUIDsForDisplay } from './matchExpression';

// Source kinds selectable as a match condition. "proposal" is intentionally
// absent: proposal chat replies never pass through the formatter, so a
// proposal-scoped rule would be inert.
export const MATCH_SOURCE_KINDS: Array<{ value: string; label: string }> = [
  { value: '', label: 'Any source kind' },
  { value: 'alert', label: 'Alert' },
  { value: 'cron', label: 'Cron job' },
  { value: 'slack_mention', label: 'Slack mention' },
  { value: 'slack_listener', label: 'Slack channel change run' },
  { value: 'manual', label: 'Manual / API' },
];

export type MatchMode = 'simple' | 'expression';

export interface FormattingRuleFormState {
  name: string;
  enabled: boolean;
  matchMode: MatchMode;
  matchSourceKind: string;
  matchSourceUUID: string;
  matchChannelUUID: string;
  matchLastSkill: string;
  matchExpression: string;
  systemPrompt: string;
  outputSchemaExample: string;
  maxTokens: number;
  temperature: number;
}

export function emptyRuleFormState(): FormattingRuleFormState {
  return {
    name: '',
    enabled: true,
    matchMode: 'simple',
    matchSourceKind: '',
    matchSourceUUID: '',
    matchChannelUUID: '',
    matchLastSkill: '',
    matchExpression: '',
    systemPrompt: DEFAULT_FORMATTING_PROMPT_PLACEHOLDER,
    outputSchemaExample: DEFAULT_OUTPUT_SCHEMA_EXAMPLE,
    maxTokens: 1500,
    temperature: 0.2,
  };
}

// ruleFormStateFromRule hydrates stored blanks to the editable defaults so
// the editor opens pre-filled with what will actually run.
export function ruleFormStateFromRule(rule: FormattingRule): FormattingRuleFormState {
  const expression = rule.match_expression ?? '';
  return {
    name: rule.name,
    enabled: rule.enabled,
    matchMode: expression.trim() ? 'expression' : 'simple',
    matchSourceKind: rule.match_source_kind ?? '',
    matchSourceUUID: rule.match_source_uuid ?? '',
    matchChannelUUID: rule.match_channel_uuid ?? '',
    matchLastSkill: rule.match_last_skill ?? '',
    matchExpression: expression,
    systemPrompt: hydrateField(rule.system_prompt ?? '', DEFAULT_FORMATTING_PROMPT_PLACEHOLDER),
    outputSchemaExample: hydrateField(
      rule.output_schema_example ?? '',
      DEFAULT_OUTPUT_SCHEMA_EXAMPLE,
    ),
    maxTokens: rule.max_tokens,
    temperature: rule.temperature,
  };
}

// buildRulePayload maps the editor state to the create/update wire shape,
// dehydrating unchanged defaults back to '' so the backend fallbacks stay
// authoritative. The inactive match side is sent as empty strings — the
// backend enforces expression XOR simple fields, so switching modes must
// clear the other side in the same request.
export function buildRulePayload(state: FormattingRuleFormState): FormattingRuleUpdate {
  const expressionMode = state.matchMode === 'expression';
  return {
    name: state.name.trim(),
    enabled: state.enabled,
    match_source_kind: expressionMode ? '' : state.matchSourceKind.trim(),
    match_source_uuid: expressionMode ? '' : state.matchSourceUUID.trim(),
    match_channel_uuid: expressionMode ? '' : state.matchChannelUUID.trim(),
    match_last_skill: expressionMode ? '' : state.matchLastSkill.trim(),
    match_expression: expressionMode ? state.matchExpression.trim() : '',
    system_prompt: dehydrateField(state.systemPrompt, DEFAULT_FORMATTING_PROMPT_PLACEHOLDER),
    output_schema_example: dehydrateField(state.outputSchemaExample, DEFAULT_OUTPUT_SCHEMA_EXAMPLE),
    max_tokens: state.maxTokens,
    temperature: state.temperature,
  };
}

export interface RuleConditionLookups {
  // uuid -> display label for trigger entities (alert sources, listener
  // channels, cron jobs) and destination channels.
  triggers?: Record<string, string>;
  channels?: Record<string, string>;
}

// ruleConditionSummary renders the AND-ed match conditions as short chips.
// A rule with no conditions is a catch-all.
export function ruleConditionSummary(
  rule: Pick<
    FormattingRule,
    | 'match_source_kind'
    | 'match_source_uuid'
    | 'match_channel_uuid'
    | 'match_last_skill'
    | 'match_expression'
  >,
  lookups?: RuleConditionLookups,
): string[] {
  const expression = (rule.match_expression ?? '').trim();
  if (expression) {
    const names = { ...(lookups?.triggers ?? {}), ...(lookups?.channels ?? {}) };
    return [`expr: ${substituteUUIDsForDisplay(expression, names)}`];
  }
  const chips: string[] = [];
  if (rule.match_source_kind) {
    const kind = MATCH_SOURCE_KINDS.find((k) => k.value === rule.match_source_kind);
    chips.push(`kind: ${kind?.label ?? rule.match_source_kind}`);
  }
  if (rule.match_source_uuid) {
    chips.push(`trigger: ${lookups?.triggers?.[rule.match_source_uuid] ?? shortUUID(rule.match_source_uuid)}`);
  }
  if (rule.match_channel_uuid) {
    chips.push(`channel: ${lookups?.channels?.[rule.match_channel_uuid] ?? shortUUID(rule.match_channel_uuid)}`);
  }
  if (rule.match_last_skill) {
    chips.push(`skill: ${rule.match_last_skill}`);
  }
  if (chips.length === 0) {
    chips.push('matches everything');
  }
  return chips;
}

function shortUUID(uuid: string): string {
  return uuid.length > 8 ? `${uuid.slice(0, 8)}…` : uuid;
}

// moveInList returns a copy of the list with the item at `index` moved by
// `delta` positions (clamped). Used by the up/down reorder arrows.
export function moveInList<T>(list: T[], index: number, delta: number): T[] {
  const target = index + delta;
  if (target < 0 || target >= list.length) return list;
  const next = [...list];
  const [item] = next.splice(index, 1);
  next.splice(target, 0, item);
  return next;
}
