import { useCallback, useEffect, useState } from 'react';
import { Plus, Save, X, Trash2, Edit2, Power, PowerOff } from 'lucide-react';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { integrationsApi } from '../../api/client';
import type { Integration, MessagingProvider } from '../../types';
import {
  PROVIDER_CONFIGS,
  getProviderConfig,
  extractCredentialsForCreate,
  areCredentialsValidForCreate,
} from './integrationsHelpers';

type FormState = {
  provider: MessagingProvider;
  name: string;
  enabled: boolean;
  credentials: Record<string, string>;
};

const EMPTY_FORM = (provider: MessagingProvider = 'slack'): FormState => ({
  provider,
  name: '',
  enabled: true,
  credentials: {},
});

export default function IntegrationsManager() {
  const [integrations, setIntegrations] = useState<Integration[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Integration | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [form, setForm] = useState<FormState>(EMPTY_FORM());

  const reload = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const rows = await integrationsApi.list();
      setIntegrations(rows);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load integrations');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  const startCreate = (provider: MessagingProvider) => {
    setEditing(null);
    setIsCreating(true);
    setForm(EMPTY_FORM(provider));
  };

  const startEdit = (row: Integration) => {
    setEditing(row);
    setIsCreating(false);
    setForm({ provider: row.provider, name: row.name, enabled: row.enabled, credentials: {} });
  };

  const cancel = () => {
    setEditing(null);
    setIsCreating(false);
    setForm(EMPTY_FORM());
  };

  const save = async () => {
    try {
      setError(null);
      const config = getProviderConfig(form.provider);
      if (!config) {
        setError(`Unknown provider: ${form.provider}`);
        return;
      }
      if (!form.name.trim()) {
        setError('Name is required');
        return;
      }

      if (isCreating) {
        if (!areCredentialsValidForCreate(config, form.credentials)) {
          setError(`All ${config.label} credentials are required`);
          return;
        }
        await integrationsApi.create({
          provider: form.provider,
          name: form.name.trim(),
          credentials: extractCredentialsForCreate(config, form.credentials),
          enabled: form.enabled,
        });
      } else if (editing) {
        const creds = extractCredentialsForCreate(config, form.credentials);
        await integrationsApi.update(editing.uuid, {
          name: form.name.trim(),
          enabled: form.enabled,
          // Only send credentials when at least one new value was typed.
          credentials: Object.keys(creds).length > 0 ? creds : undefined,
        });
      }
      cancel();
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save integration');
    }
  };

  const remove = async (uuid: string) => {
    if (!confirm('Delete this integration? Channels that reference it will also be removed.')) {
      return;
    }
    try {
      setError(null);
      await integrationsApi.delete(uuid);
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete integration');
    }
  };

  if (loading) return <LoadingSpinner />;

  const activeConfig = getProviderConfig(form.provider);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <p className="text-sm text-gray-600 dark:text-gray-400">
          Configure connections to messaging providers. Each integration can be referenced by one or more Channels.
        </p>
        <div className="flex gap-2">
          {PROVIDER_CONFIGS.map((cfg) => (
            <button
              key={cfg.provider}
              className="btn btn-primary"
              disabled={!cfg.available || isCreating || !!editing}
              title={cfg.available ? `Add ${cfg.label} integration` : `${cfg.label}: coming soon`}
              onClick={() => startCreate(cfg.provider)}
            >
              <Plus className="w-4 h-4" />
              Add {cfg.label}
              {!cfg.available ? ' (coming soon)' : ''}
            </button>
          ))}
        </div>
      </div>

      {error && <ErrorMessage message={error} />}

      {(isCreating || editing) && activeConfig && (
        <div className="p-6 bg-gray-50 dark:bg-gray-900/50 rounded-lg border border-gray-200 dark:border-gray-700 animate-fade-in">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white mb-4">
            {isCreating ? `Add ${activeConfig.label} Integration` : `Edit ${activeConfig.label} Integration`}
          </h3>
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Name <span className="text-red-500">*</span>
              </label>
              <input
                type="text"
                className="input-field"
                placeholder={`Primary ${activeConfig.label}`}
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </div>

            {activeConfig.credentialFields.map((field) => (
              <div key={field.name}>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                  {field.label}
                  {isCreating && <span className="text-red-500"> *</span>}
                </label>
                <input
                  type={field.secret ? 'password' : 'text'}
                  className="input-field"
                  placeholder={field.placeholder ?? ''}
                  value={form.credentials[field.name] ?? ''}
                  onChange={(e) =>
                    setForm({
                      ...form,
                      credentials: { ...form.credentials, [field.name]: e.target.value },
                    })
                  }
                />
                {field.hint && (
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{field.hint}</p>
                )}
                {!isCreating && (
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    Leave blank to keep existing value.
                  </p>
                )}
              </div>
            ))}

            <div className="flex items-center gap-3 p-3 rounded-lg bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700">
              <input
                type="checkbox"
                id="integration-enabled"
                checked={form.enabled}
                onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
              />
              <label htmlFor="integration-enabled" className="flex items-center gap-2 cursor-pointer">
                {form.enabled ? (
                  <Power className="w-4 h-4 text-green-500" />
                ) : (
                  <PowerOff className="w-4 h-4 text-gray-400" />
                )}
                <span className="text-sm text-gray-700 dark:text-gray-300">
                  {form.enabled ? 'Enabled' : 'Disabled'}
                </span>
              </label>
            </div>

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

      {integrations.length === 0 ? (
        <div className="py-12 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
          <p className="text-gray-500 dark:text-gray-400">No integrations configured</p>
          <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">
            Add a Slack integration to get started.
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {integrations.map((row) => {
            const cfg = getProviderConfig(row.provider);
            return (
              <div
                key={row.uuid}
                className="border border-gray-200 dark:border-gray-700 rounded-lg p-4 flex items-center justify-between"
              >
                <div className="flex items-center gap-3">
                  <div className={`source-icon source-icon-${row.provider}`}>
                    {cfg?.iconText ?? row.provider.slice(0, 2).toUpperCase()}
                  </div>
                  <div>
                    <div className="flex items-center gap-2">
                      <h4 className="font-semibold text-gray-900 dark:text-white">{row.name}</h4>
                      <span className="badge badge-primary">{cfg?.label ?? row.provider}</span>
                      <span className={`badge ${row.enabled ? 'badge-success' : 'badge-default'}`}>
                        {row.enabled ? 'Enabled' : 'Disabled'}
                      </span>
                    </div>
                    {cfg && (
                      <p className="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                        {cfg.description}
                      </p>
                    )}
                  </div>
                </div>
                <div className="flex gap-2">
                  <button
                    className="btn btn-ghost p-2 text-primary-600 dark:text-primary-400 hover:bg-primary-50 dark:hover:bg-primary-900/20"
                    onClick={() => startEdit(row)}
                    title="Edit"
                  >
                    <Edit2 className="w-4 h-4" />
                  </button>
                  <button
                    className="btn btn-ghost p-2 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20"
                    onClick={() => remove(row.uuid)}
                    title="Delete"
                  >
                    <Trash2 className="w-4 h-4" />
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
