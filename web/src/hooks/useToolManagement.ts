import { useState, useEffect, useCallback } from 'react';
import { toolsApi, toolTypesApi } from '../api/client';
import type { ToolInstance, ToolType } from '../types';

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

const MANAGED_SETTINGS_FIELDS = ['ssh_keys'];

export function useToolManagement() {
  const [tools, setTools] = useState<ToolInstance[]>([]);
  const [toolTypes, setToolTypes] = useState<ToolType[]>([]);
  const [toolSchemas, setToolSchemas] = useState<Record<string, ToolSchema>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editingTool, setEditingTool] = useState<ToolInstance | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [formData, setFormData] = useState<any>({
    tool_type_id: 0,
    name: '',
    settings: {},
    enabled: true,
  });

  const loadData = useCallback(async () => {
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
  }, []);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const handleCreate = useCallback(() => {
    setIsCreating(true);
    setFormData({
      tool_type_id: toolTypes[0]?.id || 0,
      name: '',
      settings: {},
      enabled: true,
    });
    setEditingTool(null);
  }, [toolTypes]);

  const handleEdit = useCallback((tool: ToolInstance) => {
    setEditingTool(tool);
    setFormData({
      tool_type_id: tool.tool_type_id,
      name: tool.name,
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
  }, [formData, isCreating, editingTool, loadData]);

  const handleDelete = useCallback(async (id: number) => {
    if (!confirm('Are you sure you want to delete this tool instance?')) return;

    try {
      setError('');
      await toolsApi.delete(id);
      loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete tool');
    }
  }, [loadData]);

  const handleCancel = useCallback(() => {
    setIsCreating(false);
    setEditingTool(null);
    setFormData({ tool_type_id: 0, name: '', settings: {}, enabled: true });
  }, []);

  const updateSetting = useCallback((key: string, value: any) => {
    setFormData((prev: any) => ({
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
