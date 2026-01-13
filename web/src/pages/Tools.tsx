import { useEffect, useState, useCallback } from 'react';
import { Plus, Edit2, Trash2, Save, X, Wrench, Power, PowerOff, ChevronDown, ChevronUp, AlertTriangle, Server, Key, Star } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { toolsApi, toolTypesApi, sshKeysApi } from '../api/client';
import type { ToolInstance, ToolType, SSHKey } from '../types';

// SSH Host Configuration Interface
interface SSHHostConfig {
  hostname: string;
  address: string;
  user?: string;
  port?: number;
  key_id?: string;  // Override key for this host
  jumphost_address?: string;
  jumphost_user?: string;
  jumphost_port?: number;
  allow_write_commands?: boolean;
}

// Tool Schema from MCP Gateway
interface ToolSchema {
  name: string;
  description: string;
  version: string;
  settings_schema: {
    type: string;
    required?: string[];
    properties: Record<string, any>;
  };
  functions: Array<{
    name: string;
    description: string;
    parameters?: string;
    returns?: string;
  }>;
}

// Fields managed via dedicated endpoints, excluded from tool update
const MANAGED_SETTINGS_FIELDS = ['ssh_keys'];

export default function Tools() {
  const [tools, setTools] = useState<ToolInstance[]>([]);
  const [toolTypes, setToolTypes] = useState<ToolType[]>([]);
  const [toolSchemas, setToolSchemas] = useState<Record<string, ToolSchema>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editingTool, setEditingTool] = useState<ToolInstance | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [expandedHosts, setExpandedHosts] = useState<number[]>([]);
  const [formData, setFormData] = useState<any>({
    tool_type_id: 0,
    name: '',
    settings: {},
    enabled: true,
  });

  // SSH Keys state
  const [sshKeys, setSshKeys] = useState<SSHKey[]>([]);
  const [showAddKey, setShowAddKey] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [newKeyValue, setNewKeyValue] = useState('');
  const [newKeyIsDefault, setNewKeyIsDefault] = useState(false);
  const [sshKeysLoading, setSshKeysLoading] = useState(false);

  useEffect(() => {
    loadData();
  }, []);

  const loadData = async () => {
    try {
      setLoading(true);
      setError('');
      const [toolsData, typesData, schemasData] = await Promise.all([
        toolsApi.list(),
        toolTypesApi.list(),
        fetch('/mcp/tools').then(res => res.json()),
      ]);
      setTools(toolsData);
      setToolTypes(typesData);
      setToolSchemas(schemasData);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load data');
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = () => {
    setIsCreating(true);
    setShowAdvanced(false);
    setExpandedHosts([]);
    setFormData({
      tool_type_id: toolTypes[0]?.id || 0,
      name: '',
      settings: {},
      enabled: true,
    });
    setEditingTool(null);
  };

  const handleEdit = async (tool: ToolInstance) => {
    setEditingTool(tool);
    setShowAdvanced(false);
    setExpandedHosts([]);
    setShowAddKey(false);
    setNewKeyName('');
    setNewKeyValue('');
    setNewKeyIsDefault(false);
    setFormData({
      tool_type_id: tool.tool_type_id,
      name: tool.name,
      settings: tool.settings,
      enabled: tool.enabled,
    });
    setIsCreating(false);

    // Load SSH keys if this is an SSH tool
    if (tool.tool_type?.name === 'ssh') {
      await loadSSHKeys(tool.id);
    }
  };

  const handleSave = async () => {
    try {
      setError('');

      if (!formData.name.trim()) {
        setError('Name is required');
        return;
      }

      if (isCreating) {
        await toolsApi.create({
          tool_type_id: formData.tool_type_id,
          name: formData.name,
          settings: formData.settings,
        });
      } else if (editingTool) {
        // Exclude managed fields from settings update
        const cleanSettings = { ...formData.settings };
        MANAGED_SETTINGS_FIELDS.forEach(field => delete cleanSettings[field]);

        await toolsApi.update(editingTool.id, {
          name: formData.name,
          settings: cleanSettings,
          enabled: formData.enabled,
        });
      }

      setIsCreating(false);
      setEditingTool(null);
      setFormData({ tool_type_id: 0, name: '', settings: {}, enabled: true });
      loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save tool');
    }
  };

  const handleDelete = async (id: number) => {
    if (!confirm('Are you sure you want to delete this tool instance?')) return;

    try {
      setError('');
      await toolsApi.delete(id);
      loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete tool');
    }
  };

  const handleCancel = () => {
    setIsCreating(false);
    setEditingTool(null);
    setShowAdvanced(false);
    setExpandedHosts([]);
    setFormData({ tool_type_id: 0, name: '', settings: {}, enabled: true });
  };

  const updateSetting = (key: string, value: any) => {
    setFormData({
      ...formData,
      settings: {
        ...formData.settings,
        [key]: value,
      },
    });
  };

  const selectedType = toolTypes.find((t) => t.id === formData.tool_type_id);
  const selectedSchema = selectedType ? toolSchemas[selectedType.name] : null;

  // SSH Host Management Functions
  const addHost = () => {
    const newHost: SSHHostConfig = { hostname: '', address: '' };
    const currentHosts = formData.settings.ssh_hosts || [];
    updateSetting('ssh_hosts', [...currentHosts, newHost]);
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

  // SSH Keys Management Functions
  const loadSSHKeys = useCallback(async (toolId: number) => {
    try {
      setSshKeysLoading(true);
      const keys = await sshKeysApi.list(toolId);
      setSshKeys(keys);
    } catch (err) {
      console.error('Failed to load SSH keys:', err);
      setSshKeys([]);
    } finally {
      setSshKeysLoading(false);
    }
  }, []);

  const handleAddSSHKey = async () => {
    if (!editingTool) return;
    if (!newKeyName.trim()) {
      setError('Key name is required');
      return;
    }
    if (!newKeyValue.trim()) {
      setError('Private key is required');
      return;
    }

    try {
      setError('');
      await sshKeysApi.create(editingTool.id, {
        name: newKeyName,
        private_key: newKeyValue,
        is_default: newKeyIsDefault || sshKeys.length === 0,
      });
      setShowAddKey(false);
      setNewKeyName('');
      setNewKeyValue('');
      setNewKeyIsDefault(false);
      await loadSSHKeys(editingTool.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add SSH key');
    }
  };

  const handleDeleteSSHKey = async (keyId: string) => {
    if (!editingTool) return;
    if (!confirm('Are you sure you want to delete this SSH key?')) return;

    try {
      setError('');
      await sshKeysApi.delete(editingTool.id, keyId);
      await loadSSHKeys(editingTool.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete SSH key');
    }
  };

  const handleSetDefaultKey = async (keyId: string) => {
    if (!editingTool) return;

    try {
      setError('');
      await sshKeysApi.update(editingTool.id, keyId, { is_default: true });
      await loadSSHKeys(editingTool.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to set default key');
    }
  };

  const getDefaultKey = () => sshKeys.find(k => k.is_default);

  // Filter schema properties by advanced flag
  const getSchemaProperties = (schema: any) => {
    const properties = schema?.properties || {};
    const basicProps: [string, any][] = [];
    const advancedProps: [string, any][] = [];

    Object.entries(properties).forEach(([key, prop]: [string, any]) => {
      // Skip SSH-specific fields that are handled separately
      if (key === 'ssh_hosts' || key === 'ssh_keys' || key === 'ssh_private_key') return;

      if (prop.advanced) {
        advancedProps.push([key, prop]);
      } else {
        basicProps.push([key, prop]);
      }
    });

    return { basicProps, advancedProps };
  };

  // Render a single property input
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

  // Render SSH Keys Section
  const renderSSHKeysSection = () => {
    const defaultKey = getDefaultKey();

    return (
      <div className="space-y-4 mb-6">
        <div className="flex items-center justify-between">
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
            <Key className="w-4 h-4 inline mr-1" />
            SSH Keys
          </label>
          {!showAddKey && editingTool && (
            <button
              type="button"
              onClick={() => setShowAddKey(true)}
              className="btn btn-sm btn-primary"
            >
              <Plus className="w-4 h-4" /> Add Key
            </button>
          )}
        </div>

        {/* Add Key Form */}
        {showAddKey && (
          <div className="border border-blue-200 dark:border-blue-800 rounded-lg p-4 bg-blue-50 dark:bg-blue-900/20">
            <h4 className="font-medium text-gray-900 dark:text-white mb-4">Add New SSH Key</h4>
            <div className="space-y-4">
              <div>
                <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">
                  Key Name <span className="text-red-500">*</span>
                </label>
                <input
                  type="text"
                  className="input-field"
                  placeholder="e.g., production-key"
                  value={newKeyName}
                  onChange={(e) => setNewKeyName(e.target.value)}
                />
              </div>
              <div>
                <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">
                  Private Key (PEM format) <span className="text-red-500">*</span>
                </label>
                <textarea
                  className="input-field min-h-[120px] font-mono text-sm"
                  placeholder="-----BEGIN RSA PRIVATE KEY-----&#10;...&#10;-----END RSA PRIVATE KEY-----"
                  value={newKeyValue}
                  onChange={(e) => setNewKeyValue(e.target.value)}
                />
              </div>
              <div className="flex items-center gap-2">
                <input
                  type="checkbox"
                  id="newKeyIsDefault"
                  checked={newKeyIsDefault}
                  onChange={(e) => setNewKeyIsDefault(e.target.checked)}
                />
                <label htmlFor="newKeyIsDefault" className="text-sm text-gray-700 dark:text-gray-300">
                  Set as default key
                </label>
              </div>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={handleAddSSHKey}
                  className="btn btn-sm btn-primary"
                >
                  <Save className="w-4 h-4" /> Save Key
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setShowAddKey(false);
                    setNewKeyName('');
                    setNewKeyValue('');
                    setNewKeyIsDefault(false);
                  }}
                  className="btn btn-sm btn-secondary"
                >
                  <X className="w-4 h-4" /> Cancel
                </button>
              </div>
            </div>
          </div>
        )}

        {/* Keys List */}
        {sshKeysLoading ? (
          <div className="text-center py-4 text-gray-500">Loading keys...</div>
        ) : sshKeys.length === 0 && !isCreating ? (
          <div className="text-center py-6 border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
            <Key className="w-8 h-8 mx-auto text-gray-400 mb-2" />
            <p className="text-sm text-gray-500 dark:text-gray-400">No SSH keys configured</p>
            <p className="text-xs text-gray-400 dark:text-gray-500 mt-1">Click "Add Key" to add your first SSH key</p>
          </div>
        ) : isCreating ? (
          <div className="text-center py-6 border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
            <Key className="w-8 h-8 mx-auto text-gray-400 mb-2" />
            <p className="text-sm text-gray-500 dark:text-gray-400">Save the tool first to add SSH keys</p>
          </div>
        ) : (
          <div className="border border-gray-200 dark:border-gray-700 rounded-lg overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 dark:bg-gray-800">
                <tr>
                  <th className="px-4 py-2 text-left text-gray-600 dark:text-gray-300">Name</th>
                  <th className="px-4 py-2 text-left text-gray-600 dark:text-gray-300">Default</th>
                  <th className="px-4 py-2 text-right text-gray-600 dark:text-gray-300">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {sshKeys.map((key) => (
                  <tr key={key.id} className="hover:bg-gray-50 dark:hover:bg-gray-800/50">
                    <td className="px-4 py-2 text-gray-900 dark:text-white font-medium">
                      {key.name}
                    </td>
                    <td className="px-4 py-2">
                      {key.is_default ? (
                        <span className="inline-flex items-center text-yellow-600 dark:text-yellow-400">
                          <Star className="w-4 h-4 fill-current mr-1" /> Default
                        </span>
                      ) : (
                        <button
                          type="button"
                          onClick={() => handleSetDefaultKey(key.id)}
                          className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 text-xs"
                        >
                          Set as default
                        </button>
                      )}
                    </td>
                    <td className="px-4 py-2 text-right">
                      <button
                        type="button"
                        onClick={() => handleDeleteSSHKey(key.id)}
                        className="text-red-500 hover:text-red-700 p-1"
                        title="Delete key"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* Info about default key */}
        {sshKeys.length > 0 && defaultKey && (
          <p className="text-xs text-gray-500 dark:text-gray-400">
            Default key: <span className="font-medium">{defaultKey.name}</span> - used for all hosts unless overridden
          </p>
        )}
      </div>
    );
  };

  // Render SSH Hosts List
  const renderSSHHostsList = () => {
    const hosts: SSHHostConfig[] = formData.settings.ssh_hosts || [];

    return (
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
            SSH Hosts <span className="text-red-500">*</span>
          </label>
          <button
            type="button"
            onClick={addHost}
            className="btn btn-sm btn-primary"
          >
            <Plus className="w-4 h-4" /> Add Host
          </button>
        </div>

        {hosts.length === 0 && (
          <div className="text-center py-8 border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
            <Server className="w-8 h-8 mx-auto text-gray-400 mb-2" />
            <p className="text-sm text-gray-500 dark:text-gray-400">No hosts configured</p>
            <p className="text-xs text-gray-400 dark:text-gray-500 mt-1">Click "Add Host" to add your first server</p>
          </div>
        )}

        {hosts.map((host: SSHHostConfig, index: number) => (
          <div key={index} className="border border-gray-200 dark:border-gray-700 rounded-lg p-4">
            <div className="flex items-start justify-between mb-4">
              <h4 className="font-medium text-gray-900 dark:text-white">
                {host.hostname || `Host #${index + 1}`}
              </h4>
              <div className="flex items-center gap-2">
                {host.allow_write_commands && (
                  <span className="badge bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-300 text-xs">
                    <AlertTriangle className="w-3 h-3 mr-1 inline" />
                    Write Enabled
                  </span>
                )}
                {host.jumphost_address && (
                  <span className="badge bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300 text-xs">
                    <Server className="w-3 h-3 mr-1 inline" />
                    Jumphost
                  </span>
                )}
                <button
                  type="button"
                  onClick={() => toggleHostExpand(index)}
                  className="btn btn-ghost btn-sm p-1"
                >
                  {expandedHosts.includes(index) ? (
                    <ChevronUp className="w-4 h-4" />
                  ) : (
                    <ChevronDown className="w-4 h-4" />
                  )}
                </button>
                <button
                  type="button"
                  onClick={() => removeHost(index)}
                  className="btn btn-ghost btn-sm p-1 text-red-500 hover:text-red-700"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>

            {/* Required Fields (always visible) */}
            <div className="grid grid-cols-2 gap-4 mb-4">
              <div>
                <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">
                  Hostname (display name) *
                </label>
                <input
                  type="text"
                  className="input-field"
                  placeholder="web-prod-1"
                  value={host.hostname || ''}
                  onChange={(e) => updateHost(index, 'hostname', e.target.value)}
                />
              </div>
              <div>
                <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">
                  Address (IP or FQDN) *
                </label>
                <input
                  type="text"
                  className="input-field"
                  placeholder="192.168.1.10"
                  value={host.address || ''}
                  onChange={(e) => updateHost(index, 'address', e.target.value)}
                />
              </div>
            </div>

            {/* Advanced Fields (collapsible) */}
            {expandedHosts.includes(index) && (
              <div className="border-t border-gray-200 dark:border-gray-700 pt-4 mt-4 space-y-4">
                <p className="text-xs text-gray-500 dark:text-gray-400 font-medium">Advanced Options</p>

                {/* User and Port */}
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">
                      SSH User <span className="text-gray-400">(default: root)</span>
                    </label>
                    <input
                      type="text"
                      className="input-field"
                      placeholder="root"
                      value={host.user || ''}
                      onChange={(e) => updateHost(index, 'user', e.target.value)}
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">
                      SSH Port <span className="text-gray-400">(default: 22)</span>
                    </label>
                    <input
                      type="number"
                      className="input-field"
                      placeholder="22"
                      value={host.port || ''}
                      onChange={(e) => updateHost(index, 'port', e.target.value ? parseInt(e.target.value) : undefined)}
                    />
                  </div>
                </div>

                {/* SSH Key Selection */}
                {sshKeys.length > 0 && (
                  <div>
                    <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">
                      <Key className="w-3 h-3 inline mr-1" />
                      SSH Key
                    </label>
                    <select
                      className="input-field"
                      value={host.key_id || ''}
                      onChange={(e) => updateHost(index, 'key_id', e.target.value || undefined)}
                    >
                      <option value="">
                        Use Default ({getDefaultKey()?.name || 'none'})
                      </option>
                      {sshKeys.filter(k => !k.is_default).map((key) => (
                        <option key={key.id} value={key.id}>
                          {key.name}
                        </option>
                      ))}
                    </select>
                  </div>
                )}

                {/* Jumphost Configuration */}
                <div className="bg-gray-50 dark:bg-gray-900/50 rounded-lg p-3">
                  <p className="text-xs font-medium text-gray-700 dark:text-gray-300 mb-3">
                    <Server className="w-3 h-3 inline mr-1" />
                    Jumphost / Bastion (optional)
                  </p>
                  <div className="grid grid-cols-3 gap-4">
                    <div>
                      <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">Address</label>
                      <input
                        type="text"
                        className="input-field"
                        placeholder="bastion.example.com"
                        value={host.jumphost_address || ''}
                        onChange={(e) => updateHost(index, 'jumphost_address', e.target.value)}
                      />
                    </div>
                    <div>
                      <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">User</label>
                      <input
                        type="text"
                        className="input-field"
                        placeholder="(same as host)"
                        value={host.jumphost_user || ''}
                        onChange={(e) => updateHost(index, 'jumphost_user', e.target.value)}
                      />
                    </div>
                    <div>
                      <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">Port</label>
                      <input
                        type="number"
                        className="input-field"
                        placeholder="22"
                        value={host.jumphost_port || ''}
                        onChange={(e) => updateHost(index, 'jumphost_port', e.target.value ? parseInt(e.target.value) : undefined)}
                      />
                    </div>
                  </div>
                </div>

                {/* Write Commands Toggle */}
                <div className="flex items-center justify-between p-3 bg-yellow-50 dark:bg-yellow-900/20 rounded-lg border border-yellow-200 dark:border-yellow-800">
                  <div className="flex items-start gap-2">
                    <AlertTriangle className="w-4 h-4 text-yellow-600 mt-0.5" />
                    <div>
                      <p className="text-sm font-medium text-yellow-800 dark:text-yellow-200">
                        Allow Write Commands
                      </p>
                      <p className="text-xs text-yellow-600 dark:text-yellow-400">
                        Enables destructive commands (rm, mv, kill, etc.)
                      </p>
                    </div>
                  </div>
                  <input
                    type="checkbox"
                    checked={host.allow_write_commands || false}
                    onChange={(e) => updateHost(index, 'allow_write_commands', e.target.checked)}
                    className="w-4 h-4"
                  />
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    );
  };

  return (
    <div>
      <PageHeader
        title="Tools"
        description="Manage tool instances and their configurations"
        action={
          !isCreating && !editingTool && (
            <button onClick={handleCreate} className="btn btn-primary">
              <Plus className="w-4 h-4" />
              New Tool
            </button>
          )
        }
      />

      {error && <ErrorMessage message={error} />}

      {loading ? (
        <LoadingSpinner />
      ) : (
        <>
          {/* Create/Edit Form */}
          {(isCreating || editingTool) && (
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
                    disabled={!!editingTool}
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
                      {selectedType.name === 'ssh' && renderSSHKeysSection()}

                      {/* SSH Hosts - Special handling */}
                      {selectedType.name === 'ssh' && renderSSHHostsList()}

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

          {/* Tools List */}
          <div className="card">
            {tools.length === 0 ? (
              <div className="py-16 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
                <Wrench className="w-12 h-12 mx-auto text-gray-400 mb-3" />
                <p className="text-gray-500 dark:text-gray-400">No tool instances yet</p>
                <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">Create one to get started</p>
              </div>
            ) : (
              <div className="space-y-4">
                {tools.map((tool) => (
                  <div
                    key={tool.id}
                    className={`border rounded-lg transition-all ${
                      tool.enabled
                        ? 'border-gray-200 dark:border-gray-700 hover:border-gray-300 dark:hover:border-gray-600'
                        : 'border-gray-100 dark:border-gray-800 opacity-60'
                    }`}
                  >
                    {/* Tool Header */}
                    <div className="p-6">
                      <div className="flex items-start justify-between">
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-3 mb-2">
                            <h3 className="font-semibold text-gray-900 dark:text-white">
                              {tool.name}
                            </h3>
                            <span className="badge badge-primary">
                              {tool.tool_type?.name}
                            </span>
                            <span className={`badge ${tool.enabled ? 'badge-success' : 'badge-default'}`}>
                              {tool.enabled ? 'Enabled' : 'Disabled'}
                            </span>
                          </div>
                          <p className="text-gray-600 dark:text-gray-400 text-sm">
                            {tool.tool_type?.description}
                          </p>
                        </div>

                        {/* Actions */}
                        <div className="flex gap-2 ml-4 flex-shrink-0">
                          <button
                            onClick={() => handleEdit(tool)}
                            className="btn btn-ghost p-2 text-primary-600 dark:text-primary-400 hover:bg-primary-50 dark:hover:bg-primary-900/20"
                            title="Edit"
                          >
                            <Edit2 className="w-4 h-4" />
                          </button>
                          <button
                            onClick={() => handleDelete(tool.id)}
                            className="btn btn-ghost p-2 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20"
                            title="Delete"
                          >
                            <Trash2 className="w-4 h-4" />
                          </button>
                        </div>
                      </div>

                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
