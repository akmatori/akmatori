import { useCallback, useEffect, useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, Info, Pencil, Plus, Trash2 } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { SuccessMessage } from '../ErrorMessage';
import ChannelPicker from '../channels/ChannelPicker';
import {
  alertSourcesApi,
  channelsApi,
  cronJobsApi,
  formattingRulesApi,
  skillsApi,
} from '../../api/client';
import type { AlertSourceInstance, Channel, CronJob, FormattingRule, Skill } from '../../types';
import FormattingConfigFields from './FormattingConfigFields';
import {
  MATCH_SOURCE_KINDS,
  buildRulePayload,
  emptyRuleFormState,
  moveInList,
  ruleConditionSummary,
  ruleFormStateFromRule,
  type FormattingRuleFormState,
} from './formattingRulesHelpers';
import { substituteUUIDsForDisplay, validateMatchExpression } from './matchExpression';
import {
  OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES,
  SYSTEM_PROMPT_MAX_BYTES,
  systemPromptByteLength,
  validateSchemaExampleText,
} from './formattingSettingsHelpers';

interface FormattingRulesSectionProps {
  onStatusChange?: (status: 'configured' | 'disabled' | undefined) => void;
}

export default function FormattingRulesSection({ onStatusChange }: FormattingRulesSectionProps) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [rules, setRules] = useState<FormattingRule[]>([]);

  // Lookup data for condition pickers / summaries (best-effort).
  const [listenerChannels, setListenerChannels] = useState<Channel[]>([]);
  const [alertSources, setAlertSources] = useState<AlertSourceInstance[]>([]);
  const [cronJobs, setCronJobs] = useState<CronJob[]>([]);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [postChannels, setPostChannels] = useState<Channel[]>([]);

  // Editor state: null = closed, '' = creating, uuid = editing that rule.
  const [editingUUID, setEditingUUID] = useState<string | null>(null);
  const [form, setForm] = useState<FormattingRuleFormState>(emptyRuleFormState());
  const [schemaJsonError, setSchemaJsonError] = useState<string | null>(null);
  const [exprError, setExprError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Insert-condition builder state (expression mode).
  const [insField, setInsField] = useState<'source_kind' | 'trigger' | 'channel' | 'skill'>('source_kind');
  const [insOp, setInsOp] = useState<'==' | '!='>('==');
  const [insValue, setInsValue] = useState('');
  const [insConnector, setInsConnector] = useState<'&&' | '||'>('&&');

  const publishStatus = useCallback(
    (list: FormattingRule[]) => {
      onStatusChange?.(list.some((r) => r.enabled) ? 'configured' : 'disabled');
    },
    [onStatusChange],
  );

  const loadRules = useCallback(async () => {
    try {
      setLoading(true);
      const data = await formattingRulesApi.list();
      setRules(data);
      publishStatus(data);
      setError(null);
    } catch (err) {
      setError('Failed to load formatting rules');
      console.error(err);
    } finally {
      setLoading(false);
    }
  }, [publishStatus]);

  useEffect(() => {
    loadRules();
    // Lookup data is decoration for pickers/summaries — failures are silent.
    channelsApi.list({ can_listen: true }).then(setListenerChannels).catch(() => {});
    channelsApi.list({ can_post: true }).then(setPostChannels).catch(() => {});
    alertSourcesApi.list().then(setAlertSources).catch(() => {});
    cronJobsApi.list().then(setCronJobs).catch(() => {});
    skillsApi
      .list()
      .then((all) => setSkills(all.filter((s) => !s.is_system)))
      .catch(() => {});
  }, [loadRules]);

  const lookups = useMemo(() => {
    const triggers: Record<string, string> = {};
    for (const src of alertSources) triggers[src.uuid] = src.name;
    for (const ch of listenerChannels) triggers[ch.uuid] = ch.display_name || ch.external_id;
    for (const job of cronJobs) triggers[job.uuid] = job.name;
    const channels: Record<string, string> = {};
    for (const ch of postChannels) channels[ch.uuid] = ch.display_name || ch.external_id;
    for (const ch of listenerChannels) channels[ch.uuid] ??= ch.display_name || ch.external_id;
    return { triggers, channels };
  }, [alertSources, listenerChannels, cronJobs, postChannels]);

  const flashSuccess = (message: string) => {
    setSuccess(message);
    setTimeout(() => setSuccess(null), 3000);
  };

  const openCreate = () => {
    setForm(emptyRuleFormState());
    setSchemaJsonError(null);
    setExprError(null);
    setEditingUUID('');
  };

  const openEdit = (rule: FormattingRule) => {
    setForm(ruleFormStateFromRule(rule));
    setSchemaJsonError(null);
    setExprError(null);
    setEditingUUID(rule.uuid);
  };

  const closeEditor = () => {
    setEditingUUID(null);
    setSchemaJsonError(null);
    setExprError(null);
  };

  // insertCondition appends a builder-made condition to the expression,
  // joined with the selected connector when the expression is non-empty.
  const insertCondition = () => {
    if (!insValue) return;
    const condition = `${insField} ${insOp} "${insValue}"`;
    const current = form.matchExpression.trim();
    const next = current ? `${current} ${insConnector} ${condition}` : condition;
    setForm({ ...form, matchExpression: next });
    setExprError(validateMatchExpression(next));
  };

  const insertValueOptions = (): Array<{ value: string; label: string }> => {
    switch (insField) {
      case 'source_kind':
        return MATCH_SOURCE_KINDS.filter((k) => k.value !== '').map((k) => ({
          value: k.value,
          label: k.label,
        }));
      case 'trigger':
        return [
          ...alertSources.map((s) => ({ value: s.uuid, label: s.name })),
          ...listenerChannels.map((c) => ({ value: c.uuid, label: c.display_name || c.external_id })),
          ...cronJobs.map((j) => ({ value: j.uuid, label: j.name })),
        ];
      case 'channel':
        return postChannels.map((c) => ({ value: c.uuid, label: c.display_name || c.external_id }));
      case 'skill':
        return skills.map((s) => ({ value: s.name, label: s.name }));
    }
  };

  const handleSave = async () => {
    if (!form.name.trim()) {
      setError('Rule name is required');
      return;
    }
    if (systemPromptByteLength(form.systemPrompt) > SYSTEM_PROMPT_MAX_BYTES) {
      setError(`System prompt must be ${SYSTEM_PROMPT_MAX_BYTES} bytes or fewer`);
      return;
    }
    if (new TextEncoder().encode(form.outputSchemaExample).length > OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES) {
      setError(`Output shape must be ${OUTPUT_SCHEMA_EXAMPLE_MAX_BYTES} bytes or fewer`);
      return;
    }
    const schemaError = validateSchemaExampleText(form.outputSchemaExample);
    if (schemaError) {
      setSchemaJsonError(schemaError);
      setError(`Output shape: ${schemaError}`);
      return;
    }
    if (form.matchMode === 'expression') {
      const exprValidation = validateMatchExpression(form.matchExpression);
      if (exprValidation) {
        setExprError(exprValidation);
        setError(`Match expression: ${exprValidation}`);
        return;
      }
    }
    try {
      setSaving(true);
      setError(null);
      const payload = buildRulePayload(form);
      if (editingUUID === '') {
        await formattingRulesApi.create({ ...payload, name: payload.name ?? form.name.trim() });
        flashSuccess('Formatting rule created');
      } else if (editingUUID) {
        await formattingRulesApi.update(editingUUID, payload);
        flashSuccess('Formatting rule saved');
      }
      closeEditor();
      await loadRules();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save formatting rule');
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (rule: FormattingRule) => {
    if (!window.confirm(`Delete formatting rule "${rule.name}"?`)) return;
    try {
      await formattingRulesApi.delete(rule.uuid);
      if (editingUUID === rule.uuid) closeEditor();
      flashSuccess('Formatting rule deleted');
      await loadRules();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete formatting rule');
    }
  };

  const handleToggle = async (rule: FormattingRule) => {
    try {
      await formattingRulesApi.update(rule.uuid, { enabled: !rule.enabled });
      await loadRules();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to update formatting rule');
    }
  };

  const handleMove = async (index: number, delta: number) => {
    const next = moveInList(rules, index, delta);
    if (next === rules) return;
    setRules(next); // optimistic
    try {
      const ordered = await formattingRulesApi.reorder(next.map((r) => r.uuid));
      setRules(ordered);
      publishStatus(ordered);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to reorder formatting rules');
      await loadRules();
    }
  };

  if (loading) {
    return <LoadingSpinner />;
  }

  const editorOpen = editingUUID !== null;

  return (
    <div className="space-y-5">
      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message={success} />}

      <div className="rounded-lg bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 p-3 text-xs text-blue-900 dark:text-blue-200 flex gap-2">
        <Info className="w-4 h-4 flex-shrink-0 mt-0.5" />
        <span>
          Rules are evaluated top to bottom; the first enabled rule whose conditions all match the
          incident's flow reformats the agent's final response with an extra LLM pass. When no rule
          matches, the raw response is delivered unchanged. The raw reasoning is always preserved in
          {' '}
          <code>incident.full_log</code>.
        </span>
      </div>

      {rules.length === 0 && !editorOpen && (
        <p className="text-sm text-fg-subtle">
          No formatting rules yet — responses are delivered as the agent wrote them.
        </p>
      )}

      <ul className="space-y-2">
        {rules.map((rule, index) => (
          <li
            key={rule.uuid}
            className="rounded-lg border border-line p-3 flex items-center gap-3"
          >
            <div className="flex flex-col gap-0.5">
              <button
                type="button"
                onClick={() => handleMove(index, -1)}
                disabled={index === 0}
                aria-label="Move rule up"
                className="text-gray-400 hover:text-fg disabled:opacity-30 disabled:cursor-not-allowed"
              >
                <ArrowUp className="w-3.5 h-3.5" />
              </button>
              <button
                type="button"
                onClick={() => handleMove(index, 1)}
                disabled={index === rules.length - 1}
                aria-label="Move rule down"
                className="text-gray-400 hover:text-fg disabled:opacity-30 disabled:cursor-not-allowed"
              >
                <ArrowDown className="w-3.5 h-3.5" />
              </button>
            </div>

            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-fg truncate">
                {rule.name}
              </p>
              <div className="mt-1 flex flex-wrap gap-1.5">
                {ruleConditionSummary(rule, lookups).map((chip) => (
                  <span
                    key={chip}
                    className="inline-flex items-center rounded-full bg-surface-2 px-2 py-0.5 text-xs text-fg-muted"
                  >
                    {chip}
                  </span>
                ))}
              </div>
            </div>

            <button
              type="button"
              role="switch"
              aria-checked={rule.enabled}
              aria-label={`Toggle rule ${rule.name}`}
              onClick={() => handleToggle(rule)}
              className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors ${
                rule.enabled ? 'bg-blue-600' : 'bg-gray-300 dark:bg-gray-600'
              }`}
            >
              <span
                className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform ${
                  rule.enabled ? 'translate-x-4.5' : 'translate-x-1'
                }`}
                style={{ transform: rule.enabled ? 'translateX(18px)' : 'translateX(4px)' }}
              />
            </button>

            <button
              type="button"
              onClick={() => openEdit(rule)}
              aria-label={`Edit rule ${rule.name}`}
              className="text-gray-400 hover:text-blue-600 dark:hover:text-blue-400"
            >
              <Pencil className="w-4 h-4" />
            </button>
            <button
              type="button"
              onClick={() => handleDelete(rule)}
              aria-label={`Delete rule ${rule.name}`}
              className="text-gray-400 hover:text-state-fail"
            >
              <Trash2 className="w-4 h-4" />
            </button>
          </li>
        ))}
      </ul>

      {!editorOpen && (
        <button type="button" onClick={openCreate} className="btn btn-secondary">
          <Plus className="w-4 h-4" />
          Add rule
        </button>
      )}

      {editorOpen && (
        <div className="rounded-lg border border-line p-4 space-y-5">
          <h4 className="text-sm font-semibold text-fg">
            {editingUUID === '' ? 'New formatting rule' : 'Edit formatting rule'}
          </h4>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-fg mb-1.5">
                Name
              </label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="e.g. Payments team alerts"
                className="input-field"
              />
            </div>
            <div className="flex items-end pb-1">
              <label className="flex items-center gap-2 text-sm text-fg">
                <input
                  type="checkbox"
                  checked={form.enabled}
                  onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
                  className="rounded border-gray-300"
                />
                Enabled
              </label>
            </div>
          </div>

          <div className="flex items-center justify-between">
            <div className="space-y-1">
              <p className="text-sm font-medium text-fg">Match conditions</p>
              <p className="text-xs text-fg-subtle">
                {form.matchMode === 'simple'
                  ? 'All selected conditions must match ("Any" = wildcard). Leave everything on "Any" for a catch-all rule.'
                  : 'Boolean expression over source_kind / trigger / channel / skill. Combine conditions with && (and), || (or), ! (not) and parentheses.'}
              </p>
            </div>
            <div className="flex rounded-md border border-line overflow-hidden text-xs flex-shrink-0">
              {(['simple', 'expression'] as const).map((mode) => (
                <button
                  key={mode}
                  type="button"
                  onClick={() => setForm({ ...form, matchMode: mode })}
                  className={`px-3 py-1.5 ${
                    form.matchMode === mode
                      ? 'bg-blue-600 text-white'
                      : 'bg-surface text-fg-muted hover:bg-surface-2'
                  }`}
                >
                  {mode === 'simple' ? 'Simple' : 'Expression'}
                </button>
              ))}
            </div>
          </div>

          {form.matchMode === 'expression' && (
            <div className="space-y-3">
              <div className="flex flex-wrap items-end gap-2 rounded-lg bg-gray-50 dark:bg-gray-800/60 border border-line p-3">
                <div>
                  <label className="block text-xs text-fg-subtle mb-1">Field</label>
                  <select
                    value={insField}
                    onChange={(e) => {
                      setInsField(e.target.value as typeof insField);
                      setInsValue('');
                    }}
                    className="input-field text-xs py-1.5"
                  >
                    <option value="source_kind">Source kind</option>
                    <option value="trigger">Trigger</option>
                    <option value="channel">Channel</option>
                    <option value="skill">Skill</option>
                  </select>
                </div>
                <div>
                  <label className="block text-xs text-fg-subtle mb-1">Operator</label>
                  <select
                    value={insOp}
                    onChange={(e) => setInsOp(e.target.value as typeof insOp)}
                    className="input-field text-xs py-1.5"
                  >
                    <option value="==">is (==)</option>
                    <option value="!=">is not (!=)</option>
                  </select>
                </div>
                <div className="min-w-[10rem]">
                  <label className="block text-xs text-fg-subtle mb-1">Value</label>
                  <select
                    value={insValue}
                    onChange={(e) => setInsValue(e.target.value)}
                    className="input-field text-xs py-1.5"
                  >
                    <option value="">Choose…</option>
                    {insertValueOptions().map((opt) => (
                      <option key={opt.value} value={opt.value}>
                        {opt.label}
                      </option>
                    ))}
                  </select>
                </div>
                {form.matchExpression.trim() !== '' && (
                  <div>
                    <label className="block text-xs text-fg-subtle mb-1">Join with</label>
                    <select
                      value={insConnector}
                      onChange={(e) => setInsConnector(e.target.value as typeof insConnector)}
                      className="input-field text-xs py-1.5"
                    >
                      <option value="&&">AND (&&)</option>
                      <option value="||">OR (||)</option>
                    </select>
                  </div>
                )}
                <button
                  type="button"
                  onClick={insertCondition}
                  disabled={!insValue}
                  className="btn btn-secondary text-xs py-1.5"
                >
                  <Plus className="w-3.5 h-3.5" />
                  Insert
                </button>
              </div>

              <textarea
                value={form.matchExpression}
                onChange={(e) => {
                  setForm({ ...form, matchExpression: e.target.value });
                  if (exprError) setExprError(null);
                }}
                onBlur={() => setExprError(validateMatchExpression(form.matchExpression))}
                rows={3}
                placeholder={'source_kind == "alert" && (channel == "<channel-uuid>" || skill == "<skill-name>")'}
                className={`input-field font-mono text-xs ${exprError ? 'border-red-500 dark:border-red-500' : ''}`}
              />
              {exprError ? (
                <p className="text-xs text-state-fail">{exprError}</p>
              ) : form.matchExpression.trim() ? (
                <p className="text-xs text-fg-subtle">
                  Reads as:{' '}
                  <span className="font-mono">
                    {substituteUUIDsForDisplay(form.matchExpression, {
                      ...lookups.triggers,
                      ...lookups.channels,
                    })}
                  </span>
                </p>
              ) : (
                <p className="text-xs text-fg-subtle">
                  Empty expression matches everything. Use the pickers above to insert conditions —
                  UUIDs are filled in for you.
                </p>
              )}
            </div>
          )}

          {form.matchMode === 'simple' && (
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-fg mb-1.5">
                Source kind
              </label>
              <select
                value={form.matchSourceKind}
                onChange={(e) => setForm({ ...form, matchSourceKind: e.target.value })}
                className="input-field"
              >
                {MATCH_SOURCE_KINDS.map((kind) => (
                  <option key={kind.value} value={kind.value}>
                    {kind.label}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label className="block text-sm font-medium text-fg mb-1.5">
                Trigger
              </label>
              <select
                value={form.matchSourceUUID}
                onChange={(e) => setForm({ ...form, matchSourceUUID: e.target.value })}
                className="input-field"
              >
                <option value="">Any trigger</option>
                {alertSources.length > 0 && (
                  <optgroup label="Alert sources">
                    {alertSources.map((src) => (
                      <option key={src.uuid} value={src.uuid}>
                        {src.name}
                      </option>
                    ))}
                  </optgroup>
                )}
                {listenerChannels.length > 0 && (
                  <optgroup label="Listener channels">
                    {listenerChannels.map((ch) => (
                      <option key={ch.uuid} value={ch.uuid}>
                        {ch.display_name || ch.external_id}
                      </option>
                    ))}
                  </optgroup>
                )}
                {cronJobs.length > 0 && (
                  <optgroup label="Cron jobs">
                    {cronJobs.map((job) => (
                      <option key={job.uuid} value={job.uuid}>
                        {job.name}
                      </option>
                    ))}
                  </optgroup>
                )}
              </select>
            </div>

            <div>
              <ChannelPicker
                label="Destination channel"
                value={form.matchChannelUUID || null}
                onChange={(uuid) => setForm({ ...form, matchChannelUUID: uuid ?? '' })}
                allowEmpty
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-fg mb-1.5">
                Last used skill
              </label>
              <select
                value={form.matchLastSkill}
                onChange={(e) => setForm({ ...form, matchLastSkill: e.target.value })}
                className="input-field"
              >
                <option value="">Any skill</option>
                {skills.map((skill) => (
                  <option key={skill.name} value={skill.name}>
                    {skill.name}
                  </option>
                ))}
              </select>
              <p className="mt-1 text-xs text-fg-subtle">
                Matches the last skill the agent consulted during the investigation.
              </p>
            </div>
          </div>
          )}

          <div className="space-y-1 pt-2 border-t border-line">
            <p className="text-sm font-medium text-fg">Output format</p>
          </div>

          <FormattingConfigFields
            values={{
              systemPrompt: form.systemPrompt,
              outputSchemaExample: form.outputSchemaExample,
              maxTokens: form.maxTokens,
              temperature: form.temperature,
            }}
            onChange={(patch) => setForm({ ...form, ...patch })}
            schemaJsonError={schemaJsonError}
            onSchemaJsonError={setSchemaJsonError}
          />

          <div className="flex items-center justify-end gap-2 pt-3 border-t border-line">
            <button type="button" onClick={closeEditor} className="btn btn-secondary">
              Cancel
            </button>
            <button
              type="button"
              onClick={handleSave}
              disabled={saving || schemaJsonError !== null || (form.matchMode === 'expression' && exprError !== null)}
              className="btn btn-primary"
            >
              {saving ? 'Saving...' : editingUUID === '' ? 'Create rule' : 'Save rule'}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
