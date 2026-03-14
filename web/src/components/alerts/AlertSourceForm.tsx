import { Save, X, Power, PowerOff } from 'lucide-react';
import type { AlertSourceType } from '../../types';

interface AlertSourceFormProps {
  isCreating: boolean;
  formData: {
    source_type_name: string;
    name: string;
    description: string;
    webhook_secret: string;
    field_mappings: Record<string, string>;
    settings: Record<string, any>;
    enabled: boolean;
  };
  setFormData: (data: any) => void;
  sourceTypes: AlertSourceType[];
  selectedType: AlertSourceType | undefined;
  editingSource: any;
  onSave: () => void;
  onCancel: () => void;
}

export default function AlertSourceForm({
  isCreating,
  formData,
  setFormData,
  sourceTypes,
  selectedType,
  editingSource,
  onSave,
  onCancel,
}: AlertSourceFormProps) {
  return (
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
                Enter the Channel ID (not name). Find it in Slack channel details &rarr; About &rarr; Channel ID.
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

            <div className="flex items-center gap-2">
              <input
                type="checkbox"
                id="process-human-messages"
                checked={(formData.settings?.process_human_messages as boolean) || false}
                onChange={(e) =>
                  setFormData({
                    ...formData,
                    settings: { ...formData.settings, process_human_messages: e.target.checked },
                  })
                }
              />
              <label htmlFor="process-human-messages" className="text-sm text-gray-700 dark:text-gray-300">
                Process human messages as alerts
              </label>
            </div>
            <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
              When enabled, messages from human users (not just bots/integrations) will also trigger alert
              extraction and investigations.
            </p>

            <div className="p-3 bg-blue-50 dark:bg-blue-900/20 rounded text-sm">
              <p className="text-blue-700 dark:text-blue-300">
                <strong>Note:</strong>{' '}
                {(formData.settings?.process_human_messages as boolean)
                  ? 'All messages (from bots and humans) posted to this channel will be treated as alerts.'
                  : 'Only bot/integration messages posted to this channel will be treated as alerts.'}
                {' '}AI will extract alert details and trigger investigations. Thread replies are ignored.
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
          <button onClick={onSave} className="btn btn-primary">
            <Save className="w-4 h-4" />
            Save
          </button>
          <button onClick={onCancel} className="btn btn-secondary">
            <X className="w-4 h-4" />
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
