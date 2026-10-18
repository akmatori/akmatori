import EmptyState from '../shared/EmptyState';
import { useCallback, useEffect, useState } from 'react';
import { Plus, Trash2, Edit2, Play, Power, PowerOff, Shield, Wrench } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import PageHeader from '../PageHeader';
import ErrorMessage from '../ErrorMessage';
import { cronJobsApi } from '../../api/client';
import type { CronJob } from '../../types';
import CronJobForm from './CronJobForm';
import {
  lastRunBadge,
  formatRelativeTime,
  type CronJobFormState,
} from './cronJobHelpers';
import { channelDisplayLabel, providerLabel } from '../channels/channelHelpers';

export default function CronJobsManager() {
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [editing, setEditing] = useState<CronJob | null>(null);
  const [runningUUID, setRunningUUID] = useState<string | null>(null);

  const reload = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const rows = await cronJobsApi.list();
      setJobs(rows);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load cron jobs');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  const startCreate = () => {
    setEditing(null);
    setIsCreating(true);
  };

  const startEdit = (row: CronJob) => {
    setIsCreating(false);
    setEditing(row);
  };

  const cancel = () => {
    setIsCreating(false);
    setEditing(null);
  };

  const save = async (form: CronJobFormState) => {
    if (isCreating) {
      await cronJobsApi.create({
        name: form.name.trim(),
        schedule: form.schedule.trim(),
        prompt: form.prompt,
        channel_uuid: form.channel_uuid ?? '',
        enabled: form.enabled,
        post_results: form.post_results,
        tool_instance_ids: form.tool_instance_ids,
      });
    } else if (editing) {
      await cronJobsApi.update(editing.uuid, {
        name: form.name.trim(),
        schedule: form.schedule.trim(),
        prompt: form.prompt,
        channel_uuid: form.channel_uuid ?? '',
        enabled: form.enabled,
        post_results: form.post_results,
        tool_instance_ids: form.tool_instance_ids,
      });
    }
    cancel();
    await reload();
  };

  const remove = async (uuid: string) => {
    if (!confirm('Delete this cron job? Already-running ticks are not interrupted.')) return;
    try {
      setError(null);
      await cronJobsApi.delete(uuid);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete cron job');
    }
  };

  const runNow = async (uuid: string) => {
    try {
      setError(null);
      setRunningUUID(uuid);
      await cronJobsApi.run(uuid);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to trigger cron job');
    } finally {
      setRunningUUID(null);
    }
  };

  if (loading) return <LoadingSpinner />;

  return (
    <div className="space-y-5">
      <PageHeader
        title="Cron jobs"
        description="Recurring schedules that run a one-shot LLM completion or a full agent investigation, and post the result to a channel."
        action={!isCreating && !editing && (
          <button type="button" className="btn btn-primary" onClick={startCreate}>
            <Plus className="w-4 h-4" aria-hidden="true" />
            New cron job
          </button>
        )}
      />

      {error && <ErrorMessage message={error} />}

      {(isCreating || editing) && (
        <CronJobForm
          isCreating={isCreating}
          initial={editing}
          onSave={save}
          onCancel={cancel}
        />
      )}

      {jobs.length === 0 ? (
        <EmptyState padding="py-12" title="No cron jobs configured" description="Create one to schedule recurring reports or investigations." />
      ) : (
        <div className="card p-0 overflow-x-auto">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-line">
                <th className="py-2 px-3 text-xs text-fg-subtle">
                  Name
                </th>
                <th className="py-2 px-3 text-xs text-fg-subtle">
                  Schedule
                </th>
                <th className="py-2 px-3 text-xs text-fg-subtle">
                  Channel
                </th>
                <th className="py-2 px-3 text-xs text-fg-subtle">
                  Tools
                </th>
                <th className="py-2 px-3 text-xs text-fg-subtle">
                  Status
                </th>
                <th className="py-2 px-3 text-xs text-fg-subtle">
                  Last run
                </th>
                <th className="py-2 px-3"></th>
              </tr>
            </thead>
            <tbody>
              {jobs.map((job) => {
                const badge = lastRunBadge(job);
                const channelText = job.channel
                  ? `${channelDisplayLabel(job.channel)} (${providerLabel(
                      job.channel.integration?.provider ?? '',
                    )})`
                  : 'Provider default';
                const nextRunText = job.next_run_at
                  ? formatRelativeTime(new Date(job.next_run_at))
                  : '—';
                const toolCount = job.tools?.length ?? 0;
                return (
                  <tr key={job.uuid} className="border-b border-line">
                    <td className="py-3 px-3">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="text-sm font-medium text-fg">
                          {job.name}
                        </span>
                        {job.is_system && (
                          <span
                            className="badge badge-primary text-xs"
                            title="System cron — managed by Akmatori; can be disabled but not deleted."
                          >
                            <Shield className="w-3 h-3" />
                            System
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="py-3 px-3">
                      <code className="text-xs text-fg">
                        {job.schedule}
                      </code>
                      <div className="text-xs text-fg-subtle mt-0.5">
                        Next: {nextRunText}
                      </div>
                    </td>
                    <td className="py-3 px-3">
                      <span className="text-sm text-fg">{channelText}</span>
                    </td>
                    <td className="py-3 px-3">
                      {toolCount === 0 ? (
                        <span className="text-xs text-fg-subtle">None</span>
                      ) : (
                        <span
                          className="badge badge-default text-xs"
                          title={(job.tools ?? []).map((t) => t.name).join(', ')}
                        >
                          <Wrench className="w-3 h-3" />
                          {toolCount} tool{toolCount > 1 ? 's' : ''}
                        </span>
                      )}
                    </td>
                    <td className="py-3 px-3">
                      <div className="flex items-center gap-2">
                        {job.enabled ? (
                          <Power className="w-3.5 h-3.5 text-green-500" aria-label="Enabled" />
                        ) : (
                          <PowerOff className="w-3.5 h-3.5 text-gray-400" aria-label="Disabled" />
                        )}
                        <span className={badge.className} title={badge.detail}>
                          {badge.label}
                        </span>
                      </div>
                    </td>
                    <td className="py-3 px-3">
                      <span className="text-xs text-fg-subtle">
                        {job.last_run_at
                          ? new Date(job.last_run_at).toLocaleString()
                          : '—'}
                      </span>
                    </td>
                    <td className="py-3 px-3 text-right">
                      <div className="flex gap-2 justify-end">
                        <button
                          className="btn btn-ghost p-2"
                          onClick={() => runNow(job.uuid)}
                          disabled={runningUUID === job.uuid}
                          title="Run now"
                        >
                          <Play className="w-4 h-4" />
                        </button>
                        <button
                          className="btn btn-ghost p-2"
                          onClick={() => startEdit(job)}
                          title="Edit"
                        >
                          <Edit2 className="w-4 h-4" />
                        </button>
                        {!job.is_system && (
                          <button
                            className="btn btn-ghost p-2 text-state-fail hover:bg-state-fail/10"
                            onClick={() => remove(job.uuid)}
                            title="Delete"
                          >
                            <Trash2 className="w-4 h-4" />
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
