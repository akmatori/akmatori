import { Save, X, Power, PowerOff } from 'lucide-react';
import type { AlertSourceType } from '../../types';
import ChannelPicker from '../channels/ChannelPicker';
import { visibleAlertSourceTypes, isWebhookSourceType } from './alertSourceHelpers';

interface AlertSourceFormProps {
  isCreating: boolean;
  formData: {
    source_type_name: string;
    name: string;
    description: string;
    webhook_secret: string;
    field_mappings: Record<string, string>;
    settings: Record<string, any>;
    notification_channel_uuid: string | null;
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
  const pickerTypes = visibleAlertSourceTypes(sourceTypes);

  return (
    <div className="p-6 bg-gray-50 dark:bg-gray-900/50 rounded-lg border border-gray-200 dark:border-gray-700 animate-fade-in">
      <h3 className="text-lg font-semibold text-gray-900 dark:text-white mb-6">
        {isCreating ? 'Create Alert Source' : 'Edit Alert Source'}
      </h3>

      <div className="space-y-6">
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
            {pickerTypes.map((type) => (
              <option key={type.id} value={type.name}>
                {type.display_name} - {type.description}
              </option>
            ))}
          </select>
        </div>

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

        {isWebhookSourceType(formData.source_type_name) && (
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

        <ChannelPicker
          label="Notification Channel"
          value={formData.notification_channel_uuid}
          onChange={(uuid) => setFormData({ ...formData, notification_channel_uuid: uuid })}
        />

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
