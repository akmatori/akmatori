import { useEffect, useState } from 'react';
import { Plus, Edit2, Trash2, Save, X, Wrench, Power, PowerOff, ChevronDown, ChevronUp, Settings } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { toolsApi, toolTypesApi } from '../api/client';
import type { ToolInstance, ToolType } from '../types';

export default function Tools() {
  const [tools, setTools] = useState<ToolInstance[]>([]);
  const [toolTypes, setToolTypes] = useState<ToolType[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editingTool, setEditingTool] = useState<ToolInstance | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [expandedSettings, setExpandedSettings] = useState<number | null>(null);
  const [formData, setFormData] = useState<any>({
    tool_type_id: 0,
    name: '',
    settings: {},
    enabled: true,
  });

  useEffect(() => {
    loadData();
  }, []);

  const loadData = async () => {
    try {
      setLoading(true);
      setError('');
      const [toolsData, typesData] = await Promise.all([toolsApi.list(), toolTypesApi.list()]);
      setTools(toolsData);
      setToolTypes(typesData);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load data');
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = () => {
    setIsCreating(true);
    setFormData({
      tool_type_id: toolTypes[0]?.id || 0,
      name: '',
      settings: {},
      enabled: true,
    });
    setEditingTool(null);
  };

  const handleEdit = (tool: ToolInstance) => {
    setEditingTool(tool);
    setFormData({
      tool_type_id: tool.tool_type_id,
      name: tool.name,
      settings: tool.settings,
      enabled: tool.enabled,
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
        await toolsApi.create({
          tool_type_id: formData.tool_type_id,
          name: formData.name,
          settings: formData.settings,
        });
      } else if (editingTool) {
        await toolsApi.update(editingTool.id, {
          name: formData.name,
          settings: formData.settings,
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

  const toggleSettings = (toolId: number) => {
    setExpandedSettings(expandedSettings === toolId ? null : toolId);
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
                      setFormData({ ...formData, tool_type_id: Number(e.target.value) })
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
                {selectedType && (
                  <div>
                    <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
                      Settings
                    </label>
                    <div className="border border-gray-200 dark:border-gray-700 rounded-lg p-4 space-y-4 bg-gray-50 dark:bg-gray-900/50">
                      {Object.entries(selectedType.schema.properties || {}).map(([key, prop]: [string, any]) => (
                        <div key={key}>
                          <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                            {prop.description || key}
                            {selectedType.schema.required?.includes(key) && (
                              <span className="text-red-500 ml-1">*</span>
                            )}
                          </label>
                          <input
                            type={prop.secret ? 'password' : 'text'}
                            className="input-field"
                            placeholder={prop.example || ''}
                            value={formData.settings[key] || ''}
                            onChange={(e) => updateSetting(key, e.target.value)}
                          />
                        </div>
                      ))}
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

                      {/* Settings Toggle */}
                      <button
                        onClick={() => toggleSettings(tool.id)}
                        className="mt-4 flex items-center gap-2 text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 transition-colors"
                      >
                        <Settings className="w-4 h-4" />
                        <span className="text-sm">View Settings</span>
                        {expandedSettings === tool.id ? (
                          <ChevronUp className="w-4 h-4" />
                        ) : (
                          <ChevronDown className="w-4 h-4" />
                        )}
                      </button>
                    </div>

                    {/* Expanded Settings */}
                    {expandedSettings === tool.id && (
                      <div className="border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50 p-4">
                        <pre className="font-mono text-xs text-primary-600 dark:text-primary-400 overflow-x-auto">
                          {JSON.stringify(tool.settings, null, 2)}
                        </pre>
                      </div>
                    )}
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
