import { Save, X, Power, PowerOff, ChevronDown, ChevronUp, AlertTriangle } from 'lucide-react';
import type { ToolType, SSHKey } from '../../types';
import SSHKeysSection from './SSHKeysSection';
import SSHHostsSection from './SSHHostsSection';
import { useState, useEffect } from 'react';

interface ToolSchema {
  name: string;
  settings_schema: {
    type: string;
    required?: string[];
    properties: Record<string, any>;
  };
}

interface ToolFormSectionProps {
  isCreating: boolean;
  formData: any;
  setFormData: (data: any) => void;
  updateSetting: (key: string, value: any) => void;
  toolTypes: ToolType[];
  selectedType: ToolType | undefined;
  selectedSchema: ToolSchema | null;
  editingToolId: number | undefined;
  sshKeys: SSHKey[];
  sshKeysLoading: boolean;
  showAddKey: boolean;
  setShowAddKey: (show: boolean) => void;
  newKeyName: string;
  setNewKeyName: (name: string) => void;
  newKeyValue: string;
  setNewKeyValue: (value: string) => void;
  newKeyIsDefault: boolean;
  setNewKeyIsDefault: (isDefault: boolean) => void;
  onAddSSHKey: () => void;
  onDeleteSSHKey: (keyId: string) => void;
  onSetDefaultKey: (keyId: string) => void;
  getDefaultKey: () => SSHKey | undefined;
  onSave: () => void;
  onCancel: () => void;
}

function getSchemaProperties(schema: any) {
  const properties = schema?.properties || {};
  const basicProps: [string, any][] = [];
  const advancedProps: [string, any][] = [];

  Object.entries(properties).forEach(([key, prop]: [string, any]) => {
    if (key === 'ssh_hosts' || key === 'ssh_keys' || key === 'ssh_private_key') return;

    if (prop.advanced) {
      advancedProps.push([key, prop]);
    } else {
      basicProps.push([key, prop]);
    }
  });

  return { basicProps, advancedProps };
}

export default function ToolFormSection({
  isCreating,
  formData,
  setFormData,
  updateSetting,
  toolTypes,
  selectedType,
  selectedSchema,
  editingToolId,
  sshKeys,
  sshKeysLoading,
  showAddKey,
  setShowAddKey,
  newKeyName,
  setNewKeyName,
  newKeyValue,
  setNewKeyValue,
  newKeyIsDefault,
  setNewKeyIsDefault,
  onAddSSHKey,
  onDeleteSSHKey,
  onSetDefaultKey,
  getDefaultKey,
  onSave,
  onCancel,
}: ToolFormSectionProps) {
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [expandedHosts, setExpandedHosts] = useState<number[]>([]);

  // Reset local UI state when switching between tools
  useEffect(() => {
    setShowAdvanced(false);
    setExpandedHosts([]);
  }, [editingToolId, isCreating, formData.tool_type_id]);

  const addHost = () => {
    const currentHosts = formData.settings.ssh_hosts || [];
    updateSetting('ssh_hosts', [...currentHosts, { hostname: '', address: '' }]);
  };

  const removeHost = (index: number) => {
    const currentHosts = formData.settings.ssh_hosts || [];
    updateSetting('ssh_hosts', currentHosts.filter((_: any, i: number) => i !== index));
    setExpandedHosts(expandedHosts.filter(i => i !== index).map(i => i > index ? i - 1 : i));
  };

  const updateHost = (index: number, field: string, value: any) => {
    const currentHosts = [...(formData.settings.ssh_hosts || [])];
    currentHosts[index] = { ...currentHosts[index], [field]: value };
    updateSetting('ssh_hosts', currentHosts);
  };

  const toggleHostExpand = (index: number) => {
    if (expandedHosts.includes(index)) {
      setExpandedHosts(expandedHosts.filter(i => i !== index));
    } else {
      setExpandedHosts([...expandedHosts, index]);
    }
  };

  const renderPropertyInput = (key: string, prop: any, isRequired: boolean) => {
    const inputType = prop.secret ? 'password' : prop.type === 'integer' ? 'number' : prop.type === 'boolean' ? 'checkbox' : 'text';

    if (prop.type === 'boolean') {
      return (
        <div key={key} className="flex items-center gap-3">
          <input
            type="checkbox"
            id={key}
            checked={formData.settings[key] || false}
            onChange={(e) => updateSetting(key, e.target.checked)}
          />
          <label htmlFor={key} className="text-sm text-gray-700 dark:text-gray-300">
            {prop.description || key}
            {prop.warning && (
              <span className="ml-2 text-yellow-600 dark:text-yellow-400 text-xs">
                <AlertTriangle className="w-3 h-3 inline mr-1" />
                {prop.warning}
              </span>
            )}
          </label>
        </div>
      );
    }

    if (prop.format === 'textarea') {
      return (
        <div key={key}>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
            {prop.description || key}
            {isRequired && <span className="text-red-500 ml-1">*</span>}
          </label>
          <textarea
            className="input-field min-h-[100px] font-mono text-sm"
            placeholder={prop.example || ''}
            value={formData.settings[key] || ''}
            onChange={(e) => updateSetting(key, e.target.value)}
          />
        </div>
      );
    }

    if (prop.enum) {
      return (
        <div key={key}>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
            {prop.description || key}
            {isRequired && <span className="text-red-500 ml-1">*</span>}
          </label>
          <select
            className="input-field"
            value={formData.settings[key] || prop.default || ''}
            onChange={(e) => updateSetting(key, e.target.value)}
          >
            {prop.enum.map((opt: string) => (
              <option key={opt} value={opt}>{opt}</option>
            ))}
          </select>
        </div>
      );
    }

    return (
      <div key={key}>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
          {prop.description || key}
          {isRequired && <span className="text-red-500 ml-1">*</span>}
          {prop.default !== undefined && (
            <span className="ml-2 text-gray-400 text-xs">(default: {String(prop.default)})</span>
          )}
        </label>
        <input
          type={inputType}
          className="input-field"
          placeholder={prop.example || ''}
          value={formData.settings[key] ?? ''}
          onChange={(e) => updateSetting(key, inputType === 'number' ? (e.target.value ? Number(e.target.value) : undefined) : e.target.value)}
        />
      </div>
    );
  };

  return (
    <div className="card mb-8 animate-fade-in">
      <h3 className="text-lg font-semibold text-gray-900 dark:text-white mb-6">
        {isCreating ? 'Create Tool Instance' : 'Edit Tool Instance'}
      </h3>

      <div className="space-y-6">
        {/* Tool Type */}
        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
            Tool Type <span className="text-red-500">*</span>
          </label>
          <select
            className="input-field"
            value={formData.tool_type_id}
            onChange={(e) =>
              setFormData({ ...formData, tool_type_id: Number(e.target.value), settings: {} })
            }
            disabled={!isCreating}
          >
            {toolTypes.map((type) => (
              <option key={type.id} value={type.id}>
                {type.name} - {type.description}
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
            placeholder="e.g., Production Zabbix"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
          />
        </div>

        {/* Settings based on schema */}
        {selectedType && selectedSchema && (
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
              Settings
            </label>
            <div className="border border-gray-200 dark:border-gray-700 rounded-lg p-4 space-y-4 bg-gray-50 dark:bg-gray-900/50">
              {/* SSH Keys - Special handling */}
              {selectedType.name === 'ssh' && (
                <SSHKeysSection
                  sshKeys={sshKeys}
                  sshKeysLoading={sshKeysLoading}
                  isCreating={isCreating}
                  editingToolId={editingToolId}
                  showAddKey={showAddKey}
                  setShowAddKey={setShowAddKey}
                  newKeyName={newKeyName}
                  setNewKeyName={setNewKeyName}
                  newKeyValue={newKeyValue}
                  setNewKeyValue={setNewKeyValue}
                  newKeyIsDefault={newKeyIsDefault}
                  setNewKeyIsDefault={setNewKeyIsDefault}
                  onAddSSHKey={onAddSSHKey}
                  onDeleteSSHKey={onDeleteSSHKey}
                  onSetDefaultKey={onSetDefaultKey}
                  getDefaultKey={getDefaultKey}
                />
              )}

              {/* SSH Hosts - Special handling */}
              {selectedType.name === 'ssh' && (
                <SSHHostsSection
                  hosts={formData.settings.ssh_hosts || []}
                  expandedHosts={expandedHosts}
                  sshKeys={sshKeys}
                  getDefaultKey={getDefaultKey}
                  onAddHost={addHost}
                  onRemoveHost={removeHost}
                  onUpdateHost={updateHost}
                  onToggleHostExpand={toggleHostExpand}
                />
              )}

              {/* Basic (non-advanced) properties */}
              {(() => {
                const { basicProps, advancedProps } = getSchemaProperties(selectedSchema.settings_schema);
                return (
                  <>
                    {basicProps.map(([key, prop]) =>
                      renderPropertyInput(key, prop, selectedSchema.settings_schema.required?.includes(key) || false)
                    )}

                    {/* Advanced toggle */}
                    {advancedProps.length > 0 && (
                      <div className="border-t border-gray-200 dark:border-gray-700 pt-4">
                        <button
                          type="button"
                          onClick={() => setShowAdvanced(!showAdvanced)}
                          className="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-400 hover:text-gray-800 dark:hover:text-gray-200"
                        >
                          {showAdvanced ? (
                            <ChevronUp className="w-4 h-4" />
                          ) : (
                            <ChevronDown className="w-4 h-4" />
                          )}
                          {showAdvanced ? 'Hide' : 'Show'} Advanced Settings ({advancedProps.length})
                        </button>

                        {showAdvanced && (
                          <div className="mt-4 space-y-4 pl-4 border-l-2 border-gray-200 dark:border-gray-700">
                            {advancedProps.map(([key, prop]) =>
                              renderPropertyInput(key, prop, selectedSchema.settings_schema.required?.includes(key) || false)
                            )}
                          </div>
                        )}
                      </div>
                    )}
                  </>
                );
              })()}
            </div>
          </div>
        )}

        {/* Enabled Toggle */}
        <div className="flex items-center gap-3 p-4 rounded-lg bg-gray-50 dark:bg-gray-900/50">
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
