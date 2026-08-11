import { Save, X, Power, PowerOff, ChevronDown, ChevronUp, AlertTriangle, Info, ShieldCheck, ShieldAlert } from 'lucide-react';
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

const COMMAND_POLICY_KEYS = new Set(['ssh_deny_list', 'ssh_allow_list', 'ssh_allow_write_commands']);

function getSchemaProperties(schema: any) {
  const properties = schema?.properties || {};
  const basicProps: [string, any][] = [];
  const adhocProps: [string, any][] = [];
  const commandPolicyProps: [string, any][] = [];
  const otherAdvancedProps: [string, any][] = [];

  Object.entries(properties).forEach(([key, prop]: [string, any]) => {
    if (key === 'ssh_hosts' || key === 'ssh_keys' || key === 'ssh_private_key') return;

    if (prop.advanced) {
      if (key.startsWith('ssh_adhoc_')) {
        adhocProps.push([key, prop]);
      } else if (COMMAND_POLICY_KEYS.has(key)) {
        commandPolicyProps.push([key, prop]);
      } else {
        otherAdvancedProps.push([key, prop]);
      }
    } else {
      basicProps.push([key, prop]);
    }
  });

  const advancedProps = [...adhocProps, ...commandPolicyProps, ...otherAdvancedProps];

  return { basicProps, advancedProps, adhocProps, commandPolicyProps, otherAdvancedProps };
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
  const [showCommandPolicyHelp, setShowCommandPolicyHelp] = useState(false);

  // Command Policies state (tool-level, SSH only)
  const [denyList, setDenyList] = useState<string[]>([]);
  const [allowList, setAllowList] = useState<string[]>([]);
  const [allowWriteCommands, setAllowWriteCommands] = useState(false);

  // Reset local UI state when switching between tools
  useEffect(() => {
    setShowAdvanced(false);
    setExpandedHosts([]);
    // Sync Command Policies from formData.settings
    if (formData?.settings) {
      setDenyList(Array.isArray(formData.settings.ssh_deny_list) ? formData.settings.ssh_deny_list : []);
      setAllowList(Array.isArray(formData.settings.ssh_allow_list) ? formData.settings.ssh_allow_list : []);
      setAllowWriteCommands(!!formData.settings.ssh_allow_write_commands);
    }
  }, [editingToolId, isCreating, formData.tool_type_id]);

  // Sync Command Policies back to formData
  useEffect(() => {
    if (selectedType?.name === 'ssh') {
      updateSetting('ssh_deny_list', denyList);
      updateSetting('ssh_allow_list', allowList);
      updateSetting('ssh_allow_write_commands', allowWriteCommands);
    }
  }, [denyList, allowList, allowWriteCommands, selectedType?.name]);

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
                const { basicProps, advancedProps, adhocProps, commandPolicyProps, otherAdvancedProps } = getSchemaProperties(selectedSchema.settings_schema);
                const required = selectedSchema.settings_schema.required || [];
                return (
                  <>
                    {basicProps.map(([key, prop]) =>
                      renderPropertyInput(key, prop, required.includes(key))
                    )}

                    {/* Advanced toggle */}
                    {advancedProps.length > 0 && (
                      <div className="border-t border-gray-200 dark:border-gray-700 pt-4">
                        <div className="flex items-center justify-between">
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
                          {selectedType?.name === 'ssh' && (
                            <button
                              type="button"
                              onClick={() => setShowCommandPolicyHelp(true)}
                              className="btn btn-ghost btn-sm p-1 text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-200"
                              title="Command Policy Documentation"
                            >
                              <Info className="w-4 h-4" />
                            </button>
                          )}
                        </div>

                        {showAdvanced && (
                          <div className="mt-4 space-y-6 pl-4 border-l-2 border-gray-200 dark:border-gray-700">
                            {/* AD Hoc Settings */}
                            {adhocProps.length > 0 && (
                              <div className="space-y-4">
                                <h4 className="text-sm font-semibold text-gray-800 dark:text-gray-200">AD Hoc Settings</h4>
                                {adhocProps.map(([key, prop]) =>
                                  renderPropertyInput(key, prop, required.includes(key))
                                )}
                              </div>
                            )}

                            {/* Command Policies */}
                            {commandPolicyProps.length > 0 && (
                              <div className="space-y-4">
                                <h4 className="text-sm font-semibold text-gray-800 dark:text-gray-200">Command Policies</h4>
                                {commandPolicyProps.map(([key, prop]) =>
                                  renderPropertyInput(key, prop, required.includes(key))
                                )}
                              </div>
                            )}

                            {/* Remaining advanced settings */}
                            {otherAdvancedProps.length > 0 && (
                              <div className="space-y-4">
                                <h4 className="text-sm font-semibold text-gray-800 dark:text-gray-200">Other Settings</h4>
                                {otherAdvancedProps.map(([key, prop]) =>
                                  renderPropertyInput(key, prop, required.includes(key))
                                )}
                              </div>
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

      {/* Command Policy Help Modal (SSH only) */}
      {showCommandPolicyHelp && selectedType?.name === 'ssh' && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-lg shadow-xl max-h-[90vh] w-full max-w-4xl overflow-hidden flex flex-col">
            {/* Header */}
            <div className="flex items-center justify-between p-4 border-b border-gray-200 dark:border-gray-700">
              <h3 className="text-lg font-semibold text-gray-900 dark:text-white">SSH Command Policy Documentation</h3>
              <button
                type="button"
                onClick={() => setShowCommandPolicyHelp(false)}
                className="btn btn-ghost btn-sm p-1"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Scrollable Content */}
            <div className="overflow-y-auto p-6 space-y-6">
              {/* 1. Decision Pipeline */}
              <div>
                <h4 className="text-sm font-semibold text-gray-900 dark:text-white mb-3 flex items-center gap-2">
                  <ShieldCheck className="w-4 h-4" />
                  1. Decision Pipeline (Entscheidungsreihenfolge)
                </h4>
                <div className="bg-gray-50 dark:bg-gray-800 rounded-lg p-4 space-y-2 text-sm">
                  <div className="flex items-start gap-3">
                    <span className="flex-shrink-0 w-6 h-6 rounded-full bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-300 flex items-center justify-center text-xs font-bold">1</span>
                    <div>
                      <p className="font-medium text-gray-900 dark:text-white">Global Deny List</p>
                      <p className="text-gray-600 dark:text-gray-400">Commands matching deny patterns are <strong className="text-red-600">ALWAYS blocked</strong> — even with write commands enabled.</p>
                    </div>
                  </div>
                  <div className="flex items-start gap-3">
                    <span className="flex-shrink-0 w-6 h-6 rounded-full bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 flex items-center justify-center text-xs font-bold">2</span>
                    <div>
                      <p className="font-medium text-gray-900 dark:text-white">Default Read-Only List</p>
                      <p className="text-gray-600 dark:text-gray-400">If command is in the default safe list → <strong className="text-green-600">ALLOWED</strong> immediately.</p>
                    </div>
                  </div>
                  <div className="flex items-start gap-3">
                    <span className="flex-shrink-0 w-6 h-6 rounded-full bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300 flex items-center justify-center text-xs font-bold">3</span>
                    <div>
                      <p className="font-medium text-gray-900 dark:text-white">Global Allow List</p>
                      <p className="text-gray-600 dark:text-gray-400">If command matches allow patterns → <strong className="text-green-600">ALLOWED</strong> (deny list still applies).</p>
                    </div>
                  </div>
                  <div className="flex items-start gap-3">
                    <span className="flex-shrink-0 w-6 h-6 rounded-full bg-yellow-100 dark:bg-yellow-900/30 text-yellow-700 dark:text-yellow-300 flex items-center justify-center text-xs font-bold">4</span>
                    <div>
                      <p className="font-medium text-gray-900 dark:text-white">Destructive Gate</p>
                      <p className="text-gray-600 dark:text-gray-400">If "Allow Write/Destructive Commands" is enabled → <strong className="text-green-600">ALLOWED</strong>. Otherwise → <strong className="text-red-600">BLOCKED</strong>.</p>
                    </div>
                  </div>
                </div>
              </div>

              {/* 2. Pattern Syntax */}
              <div>
                <h4 className="text-sm font-semibold text-gray-900 dark:text-white mb-3 flex items-center gap-2">
                  <Info className="w-4 h-4" />
                  2. Pattern Syntax (Wildcard Patterns)
                </h4>
                <div className="bg-gray-50 dark:bg-gray-800 rounded-lg p-4 space-y-3 text-sm">
                  <p className="text-gray-700 dark:text-gray-300">
                    Uses <strong>Claude Code wildcard syntax</strong> with <code className="px-1 py-0.5 bg-gray-200 dark:bg-gray-700 rounded text-xs">*</code>:
                  </p>
                  <div className="space-y-2">
                    <div className="flex gap-2">
                      <code className="px-2 py-1 bg-white dark:bg-gray-900 rounded border border-gray-200 dark:border-gray-700 text-xs font-mono flex-shrink-0">rm *</code>
                      <span className="text-gray-600 dark:text-gray-400">→ matches <code className="text-xs">rm -rf /</code>, <code className="text-xs">rm file.txt</code> but NOT <code className="text-xs">rmdir</code></span>
                    </div>
                    <div className="flex gap-2">
                      <code className="px-2 py-1 bg-white dark:bg-gray-900 rounded border border-gray-200 dark:border-gray-700 text-xs font-mono flex-shrink-0">shutdown</code>
                      <span className="text-gray-600 dark:text-gray-400">→ matches <code className="text-xs">shutdown</code> exactly (no args)</span>
                    </div>
                    <div className="flex gap-2">
                      <code className="px-2 py-1 bg-white dark:bg-gray-900 rounded border border-gray-200 dark:border-gray-700 text-xs font-mono flex-shrink-0">shutdown *</code>
                      <span className="text-gray-600 dark:text-gray-400">→ matches <code className="text-xs">shutdown -h now</code></span>
                    </div>
                    <div className="flex gap-2">
                      <code className="px-2 py-1 bg-white dark:bg-gray-900 rounded border border-gray-200 dark:border-gray-700 text-xs font-mono flex-shrink-0">* --version</code>
                      <span className="text-gray-600 dark:text-gray-400">→ matches <code className="text-xs">docker --version</code>, <code className="text-xs">git --version</code></span>
                    </div>
                    <div className="flex gap-2">
                      <code className="px-2 py-1 bg-white dark:bg-gray-900 rounded border border-gray-200 dark:border-gray-700 text-xs font-mono flex-shrink-0">git push *</code>
                      <span className="text-gray-600 dark:text-gray-400">→ matches <code className="text-xs">git push origin main</code></span>
                    </div>
                  </div>
                  <div className="bg-yellow-50 dark:bg-yellow-900/20 rounded p-2 text-xs text-yellow-800 dark:text-yellow-300">
                    <strong>Note:</strong> Space before <code className="px-1 py-0.5 bg-yellow-100 dark:bg-yellow-900/30 rounded">*</code> matters: <code className="px-1 py-0.5 bg-yellow-100 dark:bg-yellow-900/30 rounded">ls *</code> matches <code className="text-xs">ls -la</code> but NOT <code className="text-xs">lsof</code>
                  </div>
                </div>
              </div>

              {/* 3. Default Read-Only Commands */}
              <div>
                <h4 className="text-sm font-semibold text-gray-900 dark:text-white mb-3 flex items-center gap-2">
                  <ShieldCheck className="w-4 h-4 text-green-500" />
                  3. Default Read-Only Commands (immer erlaubt)
                </h4>
                <div className="bg-gray-50 dark:bg-gray-800 rounded-lg p-4 text-sm space-y-2">
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">File Viewing</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">cat, head, tail, less, more</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Search & Find</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">grep, rg, fzf, find, locate, which, type</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Directory Listing</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">ls, pwd, tree</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">System Info</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">whoami, uname, hostname, date, id, uptime, nproc, lscpu, getconf</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Processes</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">ps, top, htop, pgrep, pstree</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Performance</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">mpstat, sar, iostat, vmstat, pidstat, nmon, iotop</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Resources</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">df, du, free, lsblk</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Network</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">netstat, ss, ip, ping, dig, traceroute</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Text Processing</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">wc, sort, uniq, cut, awk, sed, tr</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">File Info</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">stat, file, md5sum, sha256sum</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Logs</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">journalctl, dmesg</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Containers</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">docker ps/images/logs/inspect/stats, kubectl get/describe/logs</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Package Managers</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">apt list/show, yum list/info, dpkg -l, rpm -qa</p>
                    </div>
                  </div>
                  <p className="text-xs text-gray-500 dark:text-gray-400 pt-2 border-t border-gray-200 dark:border-gray-700">
                    <strong>sudo</strong> is allowed — inner command is recursively validated against same rules.
                  </p>
                </div>
              </div>

              {/* 4. Destructive/Write Commands */}
              <div>
                <h4 className="text-sm font-semibold text-gray-900 dark:text-white mb-3 flex items-center gap-2">
                  <ShieldAlert className="w-4 h-4 text-red-500" />
                  4. Destructive/Write Commands (blocked by default)
                </h4>
                <div className="bg-gray-50 dark:bg-gray-800 rounded-lg p-4 text-sm space-y-2">
                  <p className="text-gray-700 dark:text-gray-300 mb-2">
                    These commands are <strong className="text-red-600">NOT</strong> in the default read-only list and require "Allow Write/Destructive Commands" to be enabled:
                  </p>
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">File Operations</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">rm, rmdir, shred, mv, cp, chmod, chown, chgrp</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Process Control</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">kill, killall, pkill</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">System Control</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">shutdown, reboot, halt, poweroff, init</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Disk Operations</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">dd, mkfs, fdisk, parted, mount, umount</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">User Management</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">useradd, userdel, usermod, passwd, groupadd</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Package Install/Remove</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">apt install/remove, yum install/remove, pip install, npm install</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Service Control</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">systemctl start/stop/restart/enable/disable</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Network Modification</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">iptables, firewall-cmd, ufw</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Docker/Kubectl Dangerous</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">docker rm/stop/kill/run/exec, kubectl delete/apply/create/exec/edit</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Privilege Escalation</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">su, mkfifo, mknod</p>
                    </div>
                    <div>
                      <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">Fork Bomb</p>
                      <p className="text-gray-600 dark:text-gray-400 text-xs">{'():(){ :|:& };:'}</p>
                    </div>
                  </div>
                  <div className="bg-red-50 dark:bg-red-900/20 rounded p-2 text-xs text-red-800 dark:text-red-300 mt-2">
                    <strong>⚠️ Warning:</strong> Enabling "Allow Write/Destructive Commands" permits all commands NOT in the deny list. Use with caution!
                  </div>
                </div>
              </div>
            </div>

            {/* Footer */}
            <div className="p-4 border-t border-gray-200 dark:border-gray-700 flex justify-end">
              <button
                type="button"
                onClick={() => setShowCommandPolicyHelp(false)}
                className="btn btn-sm btn-primary"
              >
                Close
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
