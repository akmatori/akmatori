import { useMemo, useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { Save, X, Power, PowerOff } from 'lucide-react';
import ChannelPicker from '../channels/ChannelPicker';
import type { CronBuiltinTool, CronJob } from '../../types';
import {
  SCHEDULE_PRESETS,
  ADVANCED_SCHEDULE_VALUE,
  matchesPreset,
  validateCronExpression,
  nextRun,
  formatRelativeTime,
  EMPTY_CRON_FORM,
  formStateFromJob,
  CRON_BUILTIN_TOOLS,
  sortBuiltinTools,
  type CronJobFormState,
} from './cronJobHelpers';

interface CronJobFormProps {
  isCreating: boolean;
  initial?: CronJob | null;
  onSave: (form: CronJobFormState) => Promise<void> | void;
  onCancel: () => void;
}

export default function CronJobForm({ isCreating, initial, onSave, onCancel }: CronJobFormProps) {
  const [form, setForm] = useState<CronJobFormState>(() =>
    initial ? formStateFromJob(initial) : EMPTY_CRON_FORM,
  );
  const [scheduleMode, setScheduleMode] = useState<string>(() =>
    initial ? matchesPreset(initial.schedule) : '*/15 * * * *',
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (initial) {
      setForm(formStateFromJob(initial));
      setScheduleMode(matchesPreset(initial.schedule));
    }
  }, [initial]);

  const validation = useMemo(() => validateCronExpression(form.schedule), [form.schedule]);
  const nextRunPreview = useMemo(() => {
    if (!validation.valid) return null;
    return nextRun(form.schedule);
  }, [form.schedule, validation.valid]);

  const onSchedulePresetChange = (value: string) => {
    setScheduleMode(value);
    if (value !== ADVANCED_SCHEDULE_VALUE) {
      setForm((f) => ({ ...f, schedule: value }));
    }
  };

  const toggleBuiltin = (name: CronBuiltinTool) => {
    setForm((f) => ({
      ...f,
      builtin_tools: f.builtin_tools.includes(name)
        ? f.builtin_tools.filter((x) => x !== name)
        : sortBuiltinTools([...f.builtin_tools, name]),
    }));
  };

  const submit = async () => {
    setError(null);
    if (!form.name.trim()) {
      setError('Name is required');
      return;
    }
    if (!form.prompt.trim()) {
      setError('Prompt is required');
      return;
    }
    if (!validation.valid) {
      setError(validation.message ?? 'Schedule is invalid');
      return;
    }
    try {
      setSubmitting(true);
      await onSave(form);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save cron job');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="p-6 bg-surface-2 rounded-lg border border-line animate-fade-in">
      <h3 className="text-lg font-semibold text-fg mb-6">
        {isCreating ? 'New cron job' : 'Edit cron job'}
      </h3>

      <div className="space-y-6">
        <div>
          <label className="block text-sm font-medium text-fg mb-1">
            Name <span className="text-red-500">*</span>
          </label>
          <input
            type="text"
            className="input-field"
            placeholder="e.g., Morning incident digest"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-fg mb-1">
            Schedule <span className="text-red-500">*</span>
          </label>
          <select
            className="input-field mb-2"
            value={scheduleMode}
            onChange={(e) => onSchedulePresetChange(e.target.value)}
          >
            {SCHEDULE_PRESETS.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label} — {p.value}
              </option>
            ))}
            <option value={ADVANCED_SCHEDULE_VALUE}>Advanced (raw cron expression)</option>
          </select>
          {scheduleMode === ADVANCED_SCHEDULE_VALUE && (
            <input
              type="text"
              className="input-field font-mono"
              placeholder="*/5 * * * *"
              value={form.schedule}
              onChange={(e) => setForm({ ...form, schedule: e.target.value })}
              aria-label="Cron expression"
            />
          )}
          {!validation.valid && (
            <p className="mt-1 text-xs text-state-fail">{validation.message}</p>
          )}
          {validation.valid && nextRunPreview && (
            <p className="mt-1 text-xs text-fg-subtle">
              Next run {formatRelativeTime(nextRunPreview)} ({nextRunPreview.toLocaleString()})
            </p>
          )}
        </div>

        <div>
          <label className="block text-sm font-medium text-fg mb-1">
            Prompt <span className="text-red-500">*</span>
          </label>
          <textarea
            className="input-field min-h-[120px] font-mono"
            placeholder="What should the cron-agent do?  e.g. 'List incidents opened in the last 24 hours and summarise root causes'"
            value={form.prompt}
            onChange={(e) => setForm({ ...form, prompt: e.target.value })}
          />
          <p className="mt-1 text-xs text-fg-subtle">
            The prompt is handed to the cron-agent skill as its initial task. The agent can use
            the tools you allow below.
          </p>
        </div>

        <div className="flex items-center gap-3 p-4 rounded-lg bg-surface border border-line">
          <input
            type="checkbox"
            role="switch"
            id="cron-tools-enabled"
            aria-label="Allow connector tools"
            checked={form.tools_enabled}
            onChange={(e) => setForm({ ...form, tools_enabled: e.target.checked })}
          />
          <label htmlFor="cron-tools-enabled" className="cursor-pointer">
            <span className="text-sm text-fg">Allow connector tools</span>
            <span className="block text-xs text-fg-subtle">
              {form.tools_enabled ? (
                <>
                  The run may call every tool from your enabled{' '}
                  <Link to="/connectors" className="text-primary-400 hover:underline">
                    connectors
                  </Link>
                  .
                </>
              ) : (
                'The run may not call connector tools.'
              )}
            </span>
          </label>
        </div>

        <fieldset>
          <legend className="block text-sm font-medium text-fg mb-2">Built-in tools</legend>
          <div className="space-y-2 border border-line rounded-lg p-3 bg-surface">
            {CRON_BUILTIN_TOOLS.map((tool) => {
              const checked = form.builtin_tools.includes(tool.value);
              return (
                <label
                  key={tool.value}
                  className={`flex items-center gap-3 p-2 rounded-lg border cursor-pointer transition-all ${
                    checked
                      ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
                      : 'border-line hover:border-gray-300'
                  }`}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => toggleBuiltin(tool.value)}
                    className="w-4 h-4"
                    aria-label={tool.label}
                  />
                  <div className="flex-1 min-w-0">
                    <div className="font-medium text-fg text-sm">{tool.label}</div>
                    <div className="text-xs text-fg-subtle">{tool.description}</div>
                  </div>
                </label>
              );
            })}
          </div>
        </fieldset>

        <div className="flex items-center gap-3 p-4 rounded-lg bg-surface border border-line">
          <input
            type="checkbox"
            id="cron-post-results"
            checked={form.post_results}
            onChange={(e) => setForm({ ...form, post_results: e.target.checked })}
          />
          <label htmlFor="cron-post-results" className="cursor-pointer">
            <span className="text-sm text-fg">
              {form.post_results ? 'Post results to a channel' : "Don't post results anywhere"}
            </span>
            <span className="block text-xs text-fg-subtle">
              {form.post_results
                ? 'The final summary is posted to the selected channel (or the workspace default).'
                : 'The run still executes and its outcome is recorded on the incident — nothing is sent to Slack.'}
            </span>
          </label>
        </div>

        {form.post_results && (
          <ChannelPicker
            label="Post results to channel"
            value={form.channel_uuid}
            onChange={(uuid) => setForm({ ...form, channel_uuid: uuid })}
          />
        )}

        <div className="flex items-center gap-3 p-4 rounded-lg bg-surface border border-line">
          <input
            type="checkbox"
            id="cron-enabled"
            checked={form.enabled}
            onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
          />
          <label htmlFor="cron-enabled" className="flex items-center gap-2 cursor-pointer">
            {form.enabled ? (
              <Power className="w-4 h-4 text-green-500" />
            ) : (
              <PowerOff className="w-4 h-4 text-gray-400" />
            )}
            <span className="text-sm text-fg">
              {form.enabled ? 'Enabled' : 'Disabled (will not fire)'}
            </span>
          </label>
        </div>

        {error && (
          <div className="text-sm text-state-fail bg-state-fail/10 border border-state-fail/40 p-3 rounded">
            {error}
          </div>
        )}

        <div className="flex gap-3 pt-4 border-t border-line">
          <button
            onClick={submit}
            className="btn btn-primary"
            disabled={submitting || !validation.valid}
          >
            <Save className="w-4 h-4" />
            {submitting ? 'Saving…' : 'Save'}
          </button>
          <button onClick={onCancel} className="btn btn-secondary" disabled={submitting}>
            <X className="w-4 h-4" />
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
