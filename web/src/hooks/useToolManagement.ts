import { useState, useEffect, useCallback } from 'react';
import { toolsApi, toolTypesApi } from '../api/client';
import type { ToolInstance, ToolType } from '../types';
import { EMPTY_TOOL_FORM, type ToolFormData, type ToolSchema } from '../components/tools/toolFormTypes';
import { getErrorMessage } from '../utils/errors';

const MANAGED_SETTINGS_FIELDS = ['ssh_keys'];

export function useToolManagement() {
  const [tools, setTools] = useState<ToolInstance[]>([]);
  const [toolTypes, setToolTypes] = useState<ToolType[]>([]);
  const [toolSchemas, setToolSchemas] = useState<Record<string, ToolSchema>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editingTool, setEditingTool] = useState<ToolInstance | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [formData, setFormData] = useState<ToolFormData>(EMPTY_TOOL_FORM);

  const loadData = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const [toolsData, typesData, schemasData] = await Promise.all([
        toolsApi.list(),
        toolTypesApi.list(),
        fetch('/mcp/tools').then(res => res.json() as Promise<Record<string, ToolSchema>>),
      ]);
      setTools(toolsData);
      setToolTypes(typesData);
      setToolSchemas(schemasData);
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load data'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const handleCreate = useCallback(() => {
    setIsCreating(true);
    setFormData({
      ...EMPTY_TOOL_FORM,
      tool_type_id: toolTypes[0]?.id || 0,
    });
    setEditingTool(null);
  }, [toolTypes]);

  const handleEdit = useCallback((tool: ToolInstance) => {
    setEditingTool(tool);
    setFormData({
      tool_type_id: tool.tool_type_id,
      name: tool.name,
      logical_name: tool.logical_name || '',
      settings: tool.settings,
      enabled: tool.enabled,
    });
    setIsCreating(false);
  }, []);

  const handleSave = useCallback(async () => {
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
        const cleanSettings = { ...formData.settings };
        MANAGED_SETTINGS_FIELDS.forEach(field => delete cleanSettings[field]);

        // If the user renamed the tool, send empty logical_name so the backend
        // re-derives it from the new name. Otherwise preserve the existing slug.
        const nameChanged = formData.name !== editingTool.name;
        await toolsApi.update(editingTool.id, {
          name: formData.name,
          logical_name: nameChanged ? '' : (formData.logical_name || ''),
          settings: cleanSettings,
          enabled: formData.enabled,
        });
      }

      setIsCreating(false);
      setEditingTool(null);
      setFormData(EMPTY_TOOL_FORM);
      loadData();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to save tool'));
    }
  }, [formData, isCreating, editingTool, loadData]);

  const handleDelete = useCallback(async (id: number) => {
    if (!confirm('Are you sure you want to delete this tool instance?')) return;

    try {
      setError('');
      await toolsApi.delete(id);
      loadData();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to delete tool'));
    }
  }, [loadData]);

  const handleCancel = useCallback(() => {
    setIsCreating(false);
    setEditingTool(null);
    setFormData(EMPTY_TOOL_FORM);
  }, []);

  const updateSetting = useCallback((key: string, value: unknown) => {
    setFormData((prev) => ({
      ...prev,
      settings: {
        ...prev.settings,
        [key]: value,
      },
    }));
  }, []);

  const selectedType = toolTypes.find((t) => t.id === formData.tool_type_id);
  const selectedSchema = selectedType ? toolSchemas[selectedType.name] : null;

  return {
    tools,
    toolTypes,
    toolSchemas,
    loading,
    error,
    setError,
    editingTool,
    isCreating,
    formData,
    setFormData,
    selectedType,
    selectedSchema,
    handleCreate,
    handleEdit,
    handleSave,
    handleDelete,
    handleCancel,
    updateSetting,
    loadData,
  };
}
