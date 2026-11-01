import EmptyState from './shared/EmptyState';
import {
  Plus,
  Edit2,
  Trash2,
  Bell,
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
import AlertSourceForm from './alerts/AlertSourceForm';
import { useAlertSourceManagement } from '../hooks/useAlertSourceManagement';
import { alertSourcesApi } from '../api/client';
import type { PartScope } from './connectors/partScope';

const sourceTypeIcons: Record<string, string> = {
  alertmanager: 'AM',
  grafana: 'GF',
  pagerduty: 'PD',
  datadog: 'DD',
  zabbix: 'ZX',
  slack_channel: 'SL',
};

interface AlertSourcesManagerProps {
  // Limit the list and the "new" form to one connector.
  scope?: PartScope;
}

export default function AlertSourcesManager({ scope }: AlertSourcesManagerProps = {}) {
  const {
    sources,
    sourceTypes,
    loading,
    error,
    editingSource,
    isCreating,
    formData,
    setFormData,
    expandedSource,
    copiedUrl,
    selectedType,
    handleCreate,
    handleEdit,
    handleSave,
    handleDelete,
    handleCancel,
    toggleExpand,
    copyWebhookUrl,
  } = useAlertSourceManagement(scope);

  if (loading) {
    return <LoadingSpinner />;
  }

  return (
    <div className="space-y-6">
      {/* Header with Create Button */}
      <div className="flex items-center justify-between">
        <p className="text-sm text-fg-muted">
          {scope
            ? 'Webhooks that receive alerts from this system. Paste the webhook URL into its alerting settings.'
            : 'Configure webhook integrations for monitoring systems like Alertmanager, Grafana, PagerDuty, Datadog, and Zabbix.'}
        </p>
        {!isCreating && !editingSource && sourceTypes.length > 0 && (
          <button onClick={handleCreate} className="btn btn-primary flex-shrink-0">
            <Plus className="w-4 h-4" />
            New Source
          </button>
        )}
      </div>

      {error && <ErrorMessage message={error} />}

      {/* Create/Edit Form */}
      {(isCreating || editingSource) && (
        <AlertSourceForm
          isCreating={isCreating}
          formData={formData}
          setFormData={setFormData}
          sourceTypes={sourceTypes}
          selectedType={selectedType}
          editingSource={editingSource}
          onSave={handleSave}
          onCancel={handleCancel}
        />
      )}

      {/* Sources List */}
      {sources.length === 0 ? (
        <EmptyState icon={Bell} title="No alert sources configured" description="Create one to start receiving alerts" />
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
                    ? 'border-line hover:border-gray-300 dark:hover:border-gray-600'
                    : 'border-line opacity-60'
                }`}
              >
                {/* Source Header */}
                <div className="p-6">
                  <div className="flex items-start justify-between">
                    <div className="flex items-start gap-4">
                      <div className={`source-icon source-icon-${typeName}`}>
                        {sourceTypeIcons[typeName] || typeName.slice(0, 2).toUpperCase()}
                      </div>

                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-3 mb-1">
                          <h3 className="font-semibold text-fg">
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
                          <p className="text-fg-muted text-sm">
                            {source.description}
                          </p>
                        )}
                      </div>
                    </div>

                    {/* Actions */}
                    <div className="flex gap-2 ml-4 flex-shrink-0">
                      <button
                        onClick={() => handleEdit(source)}
                        className="btn btn-ghost p-2"
                        title="Edit"
                      >
                        <Edit2 className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => handleDelete(source.uuid)}
                        className="btn btn-ghost p-2 text-state-fail hover:bg-state-fail/10"
                        title="Delete"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </div>

                  {/* Webhook URL or Channel ID */}
                  {typeName === 'slack_channel' ? (
                    <div className="mt-4">
                      <label className="flex items-center gap-2 text-xs font-medium text-fg-subtle mb-2">
                        <Link2 className="w-3.5 h-3.5" />
                        Slack Channel ID
                      </label>
                      <div className="webhook-url">
                        <code className="text-fg">
                          {(source.settings?.slack_channel_id as string) || 'Not configured'}
                        </code>
                      </div>
                    </div>
                  ) : (
                    <div className="mt-4">
                      <label className="flex items-center gap-2 text-xs font-medium text-fg-subtle mb-2">
                        <Link2 className="w-3.5 h-3.5" />
                        Webhook URL
                      </label>
                      <div className="webhook-url">
                        <code className="text-fg">{webhookUrl}</code>
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
                    className="mt-4 flex items-center gap-2 text-fg-subtle hover:text-fg transition-colors"
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
                  <div className="border-t border-line bg-surface-2 p-4 space-y-4">
                    {source.field_mappings &&
                      Object.keys(source.field_mappings).length > 0 && (
                        <div>
                          <h4 className="text-xs font-medium text-fg-subtle mb-2">
                            Field Mappings
                          </h4>
                          <pre className="font-mono text-xs text-primary-600 dark:text-primary-400 overflow-x-auto p-3 bg-surface rounded border border-line">
                            {JSON.stringify(source.field_mappings, null, 2)}
                          </pre>
                        </div>
                      )}

                    {source.settings && Object.keys(source.settings).length > 0 && (
                      <div>
                        <h4 className="text-xs font-medium text-fg-subtle mb-2">
                          Settings
                        </h4>
                        <pre className="font-mono text-xs text-primary-600 dark:text-primary-400 overflow-x-auto p-3 bg-surface rounded border border-line">
                          {JSON.stringify(source.settings, null, 2)}
                        </pre>
                      </div>
                    )}

                    {source.alert_source_type?.default_field_mappings && (
                      <div>
                        <h4 className="text-xs font-medium text-fg-subtle mb-2">
                          Default Field Mappings (from type)
                        </h4>
                        <pre className="font-mono text-xs text-gray-500 dark:text-gray-500 overflow-x-auto p-3 bg-surface rounded border border-line">
                          {JSON.stringify(source.alert_source_type.default_field_mappings, null, 2)}
                        </pre>
                      </div>
                    )}

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
