import { useCallback, useEffect, useState } from 'react';
import { Plus, Save, X, Trash2, Edit2 } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { channelsApi, integrationsApi } from '../../api/client';
import type { Channel, Integration } from '../../types';
import {
  channelRoles,
  roleBadgeClass,
  roleBadgeLabel,
  providerIconText,
  providerLabel,
} from './channelHelpers';

type FormState = {
  integration_uuid: string;
  external_id: string;
  display_name: string;
  can_post: boolean;
  can_listen: boolean;
  is_default_post: boolean;
  extraction_prompt: string;
  process_bot_messages: boolean;
  process_human_messages: boolean;
  enabled: boolean;
};

const EMPTY_FORM: FormState = {
  integration_uuid: '',
  external_id: '',
  display_name: '',
  can_post: true,
  can_listen: false,
  is_default_post: false,
  extraction_prompt: '',
  process_bot_messages: true,
  process_human_messages: false,
  enabled: true,
};

export default function ChannelsManager() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [integrations, setIntegrations] = useState<Integration[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [editing, setEditing] = useState<Channel | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);

  const reload = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const [chData, intData] = await Promise.all([
        channelsApi.list(),
        integrationsApi.list(),
      ]);
      setChannels(chData);
      setIntegrations(intData);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load channels');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  const integrationByID = (id: number) =>
    integrations.find((i) => i.id === id) ?? null;

  const startCreate = () => {
    setIsCreating(true);
    setEditing(null);
    setForm({ ...EMPTY_FORM, integration_uuid: integrations[0]?.uuid ?? '' });
  };

  const startEdit = (row: Channel) => {
    setIsCreating(false);
    setEditing(row);
    const integ = integrationByID(row.integration_id);
    setForm({
      integration_uuid: integ?.uuid ?? '',
      external_id: row.external_id,
      display_name: row.display_name,
      can_post: row.can_post,
      can_listen: row.can_listen,
      is_default_post: row.is_default_post,
      extraction_prompt: row.extraction_prompt,
      process_bot_messages: row.process_bot_messages,
      process_human_messages: row.process_human_messages,
      enabled: row.enabled,
    });
  };

  const cancel = () => {
    setIsCreating(false);
    setEditing(null);
    setForm(EMPTY_FORM);
  };

  const save = async () => {
    try {
      setError(null);
      if (!form.integration_uuid) {
        setError('Integration is required');
        return;
      }
      if (!form.external_id.trim()) {
        setError('External ID is required');
        return;
      }
      if (isCreating) {
        await channelsApi.create({
          integration_uuid: form.integration_uuid,
          external_id: form.external_id.trim(),
          display_name: form.display_name.trim() || undefined,
          can_post: form.can_post,
          can_listen: form.can_listen,
          is_default_post: form.is_default_post,
          extraction_prompt: form.extraction_prompt || undefined,
          process_bot_messages: form.process_bot_messages,
          process_human_messages: form.process_human_messages,
          enabled: form.enabled,
        });
      } else if (editing) {
        await channelsApi.update(editing.uuid, {
          external_id: form.external_id.trim(),
          display_name: form.display_name.trim(),
          can_post: form.can_post,
          can_listen: form.can_listen,
          is_default_post: form.is_default_post,
          extraction_prompt: form.extraction_prompt,
          process_bot_messages: form.process_bot_messages,
          process_human_messages: form.process_human_messages,
          enabled: form.enabled,
        });
      }
      cancel();
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save channel');
    }
  };

  const remove = async (uuid: string) => {
    if (!confirm('Delete this channel?')) return;
    try {
      await channelsApi.delete(uuid);
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete channel');
    }
  };

  if (loading) return <LoadingSpinner />;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <p className="text-sm text-gray-600 dark:text-gray-400">
          Channels are addressable destinations inside an Integration. Use them to route alert notifications, listen for inbound messages, or set a default outbound destination.
        </p>
        {!isCreating && !editing && (
          <button
            className="btn btn-primary"
            onClick={startCreate}
            disabled={integrations.length === 0}
            title={integrations.length === 0 ? 'Add an Integration first' : 'Add channel'}
          >
            <Plus className="w-4 h-4" />
            New Channel
          </button>
        )}
      </div>

      {error && <ErrorMessage message={error} />}

      {(isCreating || editing) && (
        <div className="p-6 bg-gray-50 dark:bg-gray-900/50 rounded-lg border border-gray-200 dark:border-gray-700 animate-fade-in">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white mb-4">
            {isCreating ? 'Add Channel' : 'Edit Channel'}
          </h3>
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Integration <span className="text-red-500">*</span>
              </label>
              <select
                className="input-field"
                value={form.integration_uuid}
                onChange={(e) => setForm({ ...form, integration_uuid: e.target.value })}
                disabled={!!editing}
              >
                <option value="">Select integration…</option>
                {integrations.map((row) => (
                  <option key={row.uuid} value={row.uuid}>
                    {providerLabel(row.provider)} — {row.name}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                External ID <span className="text-red-500">*</span>
              </label>
              <input
                type="text"
                className="input-field"
                placeholder="C0123456789"
                value={form.external_id}
                onChange={(e) => setForm({ ...form, external_id: e.target.value })}
              />
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                For Slack: the Channel ID (not the name).
              </p>
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Display Name
              </label>
              <input
                type="text"
                className="input-field"
                placeholder="#alerts"
                value={form.display_name}
                onChange={(e) => setForm({ ...form, display_name: e.target.value })}
              />
            </div>

            <div className="flex flex-wrap gap-4 p-3 rounded bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700">
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.can_post}
                  onChange={(e) => setForm({ ...form, can_post: e.target.checked })}
                />
                <span className="text-sm text-gray-700 dark:text-gray-300">Can post</span>
              </label>
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.can_listen}
                  onChange={(e) => setForm({ ...form, can_listen: e.target.checked })}
                />
                <span className="text-sm text-gray-700 dark:text-gray-300">Can listen</span>
              </label>
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.is_default_post}
                  onChange={(e) => setForm({ ...form, is_default_post: e.target.checked })}
                  disabled={!form.can_post}
                />
                <span className="text-sm text-gray-700 dark:text-gray-300">Default post target</span>
              </label>
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.enabled}
                  onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
                />
                <span className="text-sm text-gray-700 dark:text-gray-300">Enabled</span>
              </label>
              {form.can_listen && !form.can_post && (
                <span className="w-full text-xs text-gray-500 dark:text-gray-400">
                  Silent listener: alerts from this channel are investigated and shown in the
                  incidents UI, but Akmatori never posts replies, reactions, or status back to the
                  channel.
                </span>
              )}
            </div>

            {form.can_listen && (
              <>
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                    Custom Extraction Prompt (optional)
                  </label>
                  <textarea
                    className="input-field min-h-[100px]"
                    placeholder="Override the default AI extraction prompt for alert parsing…"
                    value={form.extraction_prompt}
                    onChange={(e) => setForm({ ...form, extraction_prompt: e.target.value })}
                  />
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    Leave empty to use the default. Use %s as a placeholder for the message text.
                  </p>
                </div>
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.process_bot_messages}
                    onChange={(e) => setForm({ ...form, process_bot_messages: e.target.checked })}
                  />
                  <span className="text-sm text-gray-700 dark:text-gray-300">
                    Process bot messages as alerts
                  </span>
                </label>
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.process_human_messages}
                    onChange={(e) => setForm({ ...form, process_human_messages: e.target.checked })}
                  />
                  <span className="text-sm text-gray-700 dark:text-gray-300">
                    Process human messages as alerts
                  </span>
                </label>
                {!form.process_bot_messages && !form.process_human_messages && (
                  <p className="text-xs text-amber-600 dark:text-amber-400">
                    Neither source is selected: this channel will not create alerts. Only explicit
                    @mentions of the bot will be handled.
                  </p>
                )}
              </>
            )}

            <div className="flex gap-3 pt-2 border-t border-gray-200 dark:border-gray-700">
              <button onClick={save} className="btn btn-primary">
                <Save className="w-4 h-4" />
                Save
              </button>
              <button onClick={cancel} className="btn btn-secondary">
                <X className="w-4 h-4" />
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}

      {channels.length === 0 ? (
        <div className="py-12 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
          <p className="text-gray-500 dark:text-gray-400">No channels configured</p>
          <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">
            Add a channel to start routing messages.
          </p>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-gray-200 dark:border-gray-700">
                <th className="py-2 px-3 text-xs uppercase tracking-wide text-gray-500 dark:text-gray-400">Integration</th>
                <th className="py-2 px-3 text-xs uppercase tracking-wide text-gray-500 dark:text-gray-400">Channel</th>
                <th className="py-2 px-3 text-xs uppercase tracking-wide text-gray-500 dark:text-gray-400">Roles</th>
                <th className="py-2 px-3"></th>
              </tr>
            </thead>
            <tbody>
              {channels.map((ch) => {
                const integ = integrationByID(ch.integration_id);
                const provider = integ?.provider ?? '';
                return (
                  <tr key={ch.uuid} className="border-b border-gray-100 dark:border-gray-800">
                    <td className="py-3 px-3">
                      <div className="flex items-center gap-2">
                        <div className={`source-icon source-icon-${provider}`}>
                          {providerIconText(provider)}
                        </div>
                        <div>
                          <div className="text-sm text-gray-900 dark:text-white">
                            {integ?.name ?? 'unknown'}
                          </div>
                          <div className="text-xs text-gray-500 dark:text-gray-400">
                            {providerLabel(provider)}
                          </div>
                        </div>
                      </div>
                    </td>
                    <td className="py-3 px-3">
                      <div className="text-sm text-gray-900 dark:text-white">
                        {ch.display_name || ch.external_id}
                      </div>
                      <code className="text-xs text-gray-500 dark:text-gray-400">{ch.external_id}</code>
                    </td>
                    <td className="py-3 px-3">
                      <div className="flex flex-wrap gap-1.5">
                        {channelRoles(ch).map((role) => (
                          <span key={role} className={roleBadgeClass(role)}>
                            {roleBadgeLabel(role)}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td className="py-3 px-3 text-right">
                      <div className="flex gap-2 justify-end">
                        <button
                          className="btn btn-ghost p-2 text-primary-600 dark:text-primary-400 hover:bg-primary-50 dark:hover:bg-primary-900/20"
                          onClick={() => startEdit(ch)}
                          title="Edit"
                        >
                          <Edit2 className="w-4 h-4" />
                        </button>
                        <button
                          className="btn btn-ghost p-2 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20"
                          onClick={() => remove(ch.uuid)}
                          title="Delete"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
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
