import { useEffect, useState } from 'react';
import {
  Plus,
  Edit2,
  Trash2,
  Save,
  X,
  Bell,
  Power,
  PowerOff,
  ChevronDown,
  ChevronUp,
  Copy,
  Check,
  Link2,
  Settings,
  AlertTriangle,
} from 'lucide-react';
import LoadingSpinner from './LoadingSpinner';
import ErrorMessage from './ErrorMessage';
import { alertSourceTypesApi, alertSourcesApi } from '../api/client';
import type { AlertSourceType, AlertSourceInstance } from '../types';

// Source type icon mapping
const sourceTypeIcons: Record<string, string> = {
  alertmanager: 'AM',
  grafana: 'GF',
  pagerduty: 'PD',
  datadog: 'DD',
  zabbix: 'ZX',
  slack_channel: 'SL',
};

export default function AlertSourcesManager() {
  const [sources, setSources] = useState<AlertSourceInstance[]>([]);
  const [sourceTypes, setSourceTypes] = useState<AlertSourceType[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editingSource, setEditingSource] = useState<AlertSourceInstance | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [expandedSource, setExpandedSource] = useState<string | null>(null);
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);
  const [formData, setFormData] = useState({
    source_type_name: '',
    name: '',
    description: '',
    webhook_secret: '',
    field_mappings: {} as Record<string, string>,
    settings: {} as Record<string, any>,
    enabled: true,
  });

  useEffect(() => {
    loadData();
  }, []);

  const loadData = async () => {
    try {
      setLoading(true);
      setError('');
      const [sourcesData, typesData] = await Promise.all([
        alertSourcesApi.list(),
        alertSourceTypesApi.list(),
      ]);
      setSources(sourcesData);
      setSourceTypes(typesData);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load data');
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = () => {
    setIsCreating(true);
    setFormData({
      source_type_name: sourceTypes[0]?.name || '',
      name: '',
      description: '',
      webhook_secret: '',
      field_mappings: {},
      settings: {},
      enabled: true,
    });
    setEditingSource(null);
  };

  const handleEdit = (source: AlertSourceInstance) => {
    setEditingSource(source);
    setFormData({
      source_type_name: source.alert_source_type?.name || '',
      name: source.name,
      description: source.description,
      webhook_secret: source.webhook_secret,
      field_mappings: source.field_mappings || {},
      settings: source.settings || {},
      enabled: source.enabled,
    });
    setIsCreating(false);
  };

  const handleSave = async () => {
    try {
      setError('');

      if (!formData.name.trim()) {
        setError('Name is required');
        return;
      }

      if (isCreating) {
        await alertSourcesApi.create({
          source_type_name: formData.source_type_name,
          name: formData.name,
          description: formData.description,
          webhook_secret: formData.webhook_secret,
          field_mappings: formData.field_mappings,
          settings: formData.settings,
        });
      } else if (editingSource) {
        await alertSourcesApi.update(editingSource.uuid, {
          name: formData.name,
          description: formData.description,
          webhook_secret: formData.webhook_secret,
          field_mappings: formData.field_mappings,
          settings: formData.settings,
          enabled: formData.enabled,
        });
      }

      setIsCreating(false);
      setEditingSource(null);
      setFormData({
        source_type_name: '',
        name: '',
        description: '',
        webhook_secret: '',
        field_mappings: {},
        settings: {},
        enabled: true,
      });
      loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save alert source');
    }
  };

  const handleDelete = async (uuid: string) => {
    if (!confirm('Are you sure you want to delete this alert source?')) return;

    try {
      setError('');
      await alertSourcesApi.delete(uuid);
      loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete alert source');
    }
  };

  const handleCancel = () => {
    setIsCreating(false);
    setEditingSource(null);
    setFormData({
      source_type_name: '',
      name: '',
      description: '',
      webhook_secret: '',
      field_mappings: {},
      settings: {},
      enabled: true,
    });
  };

  const toggleExpand = (uuid: string) => {
    setExpandedSource(expandedSource === uuid ? null : uuid);
  };

  const copyWebhookUrl = async (uuid: string) => {
    const url = alertSourcesApi.getWebhookUrl(uuid);
    try {
      await navigator.clipboard.writeText(url);
      setCopiedUrl(uuid);
      setTimeout(() => setCopiedUrl(null), 2000);
    } catch (err) {
      console.error('Failed to copy:', err);
    }
  };

  const selectedType = sourceTypes.find((t) => t.name === formData.source_type_name);

  if (loading) {
    return <LoadingSpinner />;
  }

  return (
    <div className="space-y-6">
      {/* Header with Create Button */}
      <div className="flex items-center justify-between">
        <p className="text-sm text-gray-600 dark:text-gray-400">
          Configure webhook integrations for monitoring systems like Alertmanager, Grafana, PagerDuty, Datadog, and Zabbix.
        </p>
        {!isCreating && !editingSource && (
          <button onClick={handleCreate} className="btn btn-primary flex-shrink-0">
            <Plus className="w-4 h-4" />
            New Source
          </button>
        )}
      </div>

      {error && <ErrorMessage message={error} />}

      {/* Create/Edit Form */}
      {(isCreating || editingSource) && (
        <div className="p-6 bg-gray-50 dark:bg-gray-900/50 rounded-lg border border-gray-200 dark:border-gray-700 animate-fade-in">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white mb-6">
            {isCreating ? 'Create Alert Source' : 'Edit Alert Source'}
          </h3>

          <div className="space-y-6">
            {/* Source Type */}
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Source Type <span className="text-red-500">*</span>
              </label>
              <select
                className="input-field"
                value={formData.source_type_name}
                onChange={(e) =>
                  setFormData({ ...formData, source_type_name: e.target.value })
                }
                disabled={!!editingSource}
              >
                {sourceTypes.map((type) => (
                  <option key={type.id} value={type.name}>
                    {type.display_name} - {type.description}
                  </option>
                ))}
              </select>
            </div>

            {/* Instance Name */}
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Instance Name <span className="text-red-500">*</span>
              </label>
              <input
                type="text"
                className="input-field"
                placeholder="e.g., Production Alertmanager"
                value={formData.name}
                onChange={(e) => setFormData({ ...formData, name: e.target.value })}
              />
            </div>

            {/* Description */}
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Description
              </label>
              <input
                type="text"
                className="input-field"
                placeholder="Optional description"
                value={formData.description}
                onChange={(e) => setFormData({ ...formData, description: e.target.value })}
              />
            </div>

            {/* Slack Channel specific fields */}
            {formData.source_type_name === 'slack_channel' ? (
              <>
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                    Slack Channel ID <span className="text-red-500">*</span>
                  </label>
                  <input
                    type="text"
                    className="input-field"
                    placeholder="C0123456789"
                    value={(formData.settings?.slack_channel_id as string) || ''}
                    onChange={(e) =>
                      setFormData({
                        ...formData,
                        settings: { ...formData.settings, slack_channel_id: e.target.value },
                      })
                    }
                  />
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    Enter the Channel ID (not name). Find it in Slack channel details → About → Channel ID.
                  </p>
                </div>

                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                    Custom Extraction Prompt (optional)
                  </label>
                  <textarea
                    className="input-field min-h-[100px]"
                    placeholder="Override the default AI extraction prompt for alert parsing..."
                    value={(formData.settings?.extraction_prompt as string) || ''}
                    onChange={(e) =>
                      setFormData({
                        ...formData,
                        settings: { ...formData.settings, extraction_prompt: e.target.value },
                      })
                    }
                  />
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    Leave empty to use the default prompt. Use %s as a placeholder for the message text.
                  </p>
                </div>

                <div className="p-3 bg-blue-50 dark:bg-blue-900/20 rounded text-sm">
                  <p className="text-blue-700 dark:text-blue-300">
                    <strong>Note:</strong> Messages posted to this channel will be treated as alerts.
                    AI will extract alert details and trigger investigations. Thread replies are ignored.
                  </p>
                </div>
              </>
            ) : (
              /* Webhook Secret - for non-Slack types */
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                  Webhook Secret
                </label>
                <input
                  type="password"
                  className="input-field"
                  placeholder="Optional secret for webhook validation"
                  value={formData.webhook_secret}
                  onChange={(e) => setFormData({ ...formData, webhook_secret: e.target.value })}
                />
                {selectedType && (
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    Header: <code>{selectedType.webhook_secret_header}</code>
                  </p>
                )}
              </div>
            )}

            {/* Enabled Toggle */}
            <div className="flex items-center gap-3 p-4 rounded-lg bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700">
              <input
                type="checkbox"
                id="enabled"
                checked={formData.enabled}
                onChange={(e) => setFormData({ ...formData, enabled: e.target.checked })}
              />
              <label htmlFor="enabled" className="flex items-center gap-2 cursor-pointer">
                {formData.enabled ? (
                  <Power className="w-4 h-4 text-green-500" />
                ) : (
                  <PowerOff className="w-4 h-4 text-gray-400" />
                )}
                <span className="text-sm text-gray-700 dark:text-gray-300">
                  {formData.enabled ? 'Enabled' : 'Disabled'}
                </span>
              </label>
            </div>

            {/* Form Actions */}
            <div className="flex gap-3 pt-4 border-t border-gray-200 dark:border-gray-700">
              <button onClick={handleSave} className="btn btn-primary">
                <Save className="w-4 h-4" />
                Save
              </button>
              <button onClick={handleCancel} className="btn btn-secondary">
                <X className="w-4 h-4" />
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Sources List */}
      {sources.length === 0 ? (
        <div className="py-16 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
          <Bell className="w-12 h-12 mx-auto text-gray-400 mb-3" />
          <p className="text-gray-500 dark:text-gray-400">No alert sources configured</p>
          <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">
            Create one to start receiving alerts
          </p>
        </div>
      ) : (
        <div className="space-y-4">
          {sources.map((source) => {
            const webhookUrl = alertSourcesApi.getWebhookUrl(source.uuid);
            const typeName = source.alert_source_type?.name || 'unknown';

            return (
              <div
                key={source.uuid}
                className={`border rounded-lg transition-all ${
                  source.enabled
                    ? 'border-gray-200 dark:border-gray-700 hover:border-gray-300 dark:hover:border-gray-600'
                    : 'border-gray-100 dark:border-gray-800 opacity-60'
                }`}
              >
                {/* Source Header */}
                <div className="p-6">
                  <div className="flex items-start justify-between">
                    <div className="flex items-start gap-4">
                      {/* Type Icon */}
                      <div className={`source-icon source-icon-${typeName}`}>
                        {sourceTypeIcons[typeName] || typeName.slice(0, 2).toUpperCase()}
                      </div>

                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-3 mb-1">
                          <h3 className="font-semibold text-gray-900 dark:text-white">
                            {source.name}
                          </h3>
                          <span className="badge badge-primary">
                            {source.alert_source_type?.display_name || typeName}
                          </span>
                          <span
                            className={`badge ${source.enabled ? 'badge-success' : 'badge-default'}`}
                          >
                            {source.enabled ? 'Enabled' : 'Disabled'}
                          </span>
                        </div>
                        {source.description && (
                          <p className="text-gray-600 dark:text-gray-400 text-sm">
                            {source.description}
                          </p>
                        )}
                      </div>
                    </div>

                    {/* Actions */}
                    <div className="flex gap-2 ml-4 flex-shrink-0">
                      <button
                        onClick={() => handleEdit(source)}
                        className="btn btn-ghost p-2 text-primary-600 dark:text-primary-400 hover:bg-primary-50 dark:hover:bg-primary-900/20"
                        title="Edit"
                      >
                        <Edit2 className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => handleDelete(source.uuid)}
                        className="btn btn-ghost p-2 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20"
                        title="Delete"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </div>

                  {/* Webhook URL or Channel ID */}
                  {typeName === 'slack_channel' ? (
                    <div className="mt-4">
                      <label className="flex items-center gap-2 text-xs font-medium text-gray-500 dark:text-gray-400 mb-2">
                        <Link2 className="w-3.5 h-3.5" />
                        Slack Channel ID
                      </label>
                      <div className="webhook-url">
                        <code className="text-gray-700 dark:text-gray-300">
                          {(source.settings?.slack_channel_id as string) || 'Not configured'}
                        </code>
                      </div>
                    </div>
                  ) : (
                    <div className="mt-4">
                      <label className="flex items-center gap-2 text-xs font-medium text-gray-500 dark:text-gray-400 mb-2">
                        <Link2 className="w-3.5 h-3.5" />
                        Webhook URL
                      </label>
                      <div className="webhook-url">
                        <code className="text-gray-700 dark:text-gray-300">{webhookUrl}</code>
                        <button
                          onClick={() => copyWebhookUrl(source.uuid)}
                          className={`copy-btn ${copiedUrl === source.uuid ? 'copied' : ''}`}
                          title="Copy to clipboard"
                        >
                          {copiedUrl === source.uuid ? (
                            <Check className="w-4 h-4" />
                          ) : (
                            <Copy className="w-4 h-4" />
                          )}
                        </button>
                      </div>
                    </div>
                  )}

                  {/* Expand Toggle */}
                  <button
                    onClick={() => toggleExpand(source.uuid)}
                    className="mt-4 flex items-center gap-2 text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 transition-colors"
                  >
                    <Settings className="w-4 h-4" />
                    <span className="text-sm">View Configuration</span>
                    {expandedSource === source.uuid ? (
                      <ChevronUp className="w-4 h-4" />
                    ) : (
                      <ChevronDown className="w-4 h-4" />
                    )}
                  </button>
                </div>

                {/* Expanded Configuration */}
                {expandedSource === source.uuid && (
                  <div className="border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50 p-4 space-y-4">
                    {/* Field Mappings */}
                    {source.field_mappings &&
                      Object.keys(source.field_mappings).length > 0 && (
                        <div>
                          <h4 className="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wide mb-2">
                            Field Mappings
                          </h4>
                          <pre className="font-mono text-xs text-primary-600 dark:text-primary-400 overflow-x-auto p-3 bg-white dark:bg-gray-800 rounded border border-gray-200 dark:border-gray-700">
                            {JSON.stringify(source.field_mappings, null, 2)}
                          </pre>
                        </div>
                      )}

                    {/* Settings */}
                    {source.settings && Object.keys(source.settings).length > 0 && (
                      <div>
                        <h4 className="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wide mb-2">
                          Settings
                        </h4>
                        <pre className="font-mono text-xs text-primary-600 dark:text-primary-400 overflow-x-auto p-3 bg-white dark:bg-gray-800 rounded border border-gray-200 dark:border-gray-700">
                          {JSON.stringify(source.settings, null, 2)}
                        </pre>
                      </div>
                    )}

                    {/* Default Mappings from Type */}
                    {source.alert_source_type?.default_field_mappings && (
                      <div>
                        <h4 className="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wide mb-2">
                          Default Field Mappings (from type)
                        </h4>
                        <pre className="font-mono text-xs text-gray-500 dark:text-gray-500 overflow-x-auto p-3 bg-white dark:bg-gray-800 rounded border border-gray-200 dark:border-gray-700">
                          {JSON.stringify(source.alert_source_type.default_field_mappings, null, 2)}
                        </pre>
                      </div>
                    )}

                    {/* Info */}
                    <div className="flex items-start gap-2 p-3 bg-blue-50 dark:bg-blue-900/20 rounded text-sm">
                      <AlertTriangle className="w-4 h-4 text-blue-500 flex-shrink-0 mt-0.5" />
                      <div className="text-blue-700 dark:text-blue-300">
                        <p className="font-medium">Webhook Header</p>
                        <p className="text-blue-600 dark:text-blue-400">
                          Send secret in header:{' '}
                          <code className="bg-blue-100 dark:bg-blue-900/50 px-1 rounded">
                            {source.alert_source_type?.webhook_secret_header}
                          </code>
                        </p>
                      </div>
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
