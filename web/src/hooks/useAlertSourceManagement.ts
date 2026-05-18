import { useState, useEffect, useCallback } from 'react';
import { alertSourceTypesApi, alertSourcesApi, channelsApi } from '../api/client';
import type { AlertSourceType, AlertSourceInstance, Channel } from '../types';
import { visibleAlertSourceTypes } from '../components/alerts/alertSourceHelpers';

interface AlertSourceFormData {
  source_type_name: string;
  name: string;
  description: string;
  webhook_secret: string;
  field_mappings: Record<string, string>;
  settings: Record<string, any>;
  notification_channel_uuid: string | null;
  enabled: boolean;
}

const EMPTY_FORM: AlertSourceFormData = {
  source_type_name: '',
  name: '',
  description: '',
  webhook_secret: '',
  field_mappings: {},
  settings: {},
  notification_channel_uuid: null,
  enabled: true,
};

export function useAlertSourceManagement() {
  const [sources, setSources] = useState<AlertSourceInstance[]>([]);
  const [sourceTypes, setSourceTypes] = useState<AlertSourceType[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editingSource, setEditingSource] = useState<AlertSourceInstance | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [expandedSource, setExpandedSource] = useState<string | null>(null);
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);
  const [formData, setFormData] = useState<AlertSourceFormData>(EMPTY_FORM);

  const loadData = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const [sourcesData, typesData, channelsData] = await Promise.all([
        alertSourcesApi.list(),
        alertSourceTypesApi.list(),
        channelsApi.list().catch(() => [] as Channel[]),
      ]);
      setSources(sourcesData);
      setSourceTypes(typesData);
      setChannels(channelsData);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load data');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const channelUUIDByID = useCallback(
    (id: number | null | undefined) => {
      if (!id) return null;
      return channels.find((c) => c.id === id)?.uuid ?? null;
    },
    [channels],
  );

  const handleCreate = useCallback(() => {
    setIsCreating(true);
    const firstType = visibleAlertSourceTypes(sourceTypes)[0]?.name ?? '';
    setFormData({ ...EMPTY_FORM, source_type_name: firstType });
    setEditingSource(null);
  }, [sourceTypes]);

  const handleEdit = useCallback(
    (source: AlertSourceInstance) => {
      setEditingSource(source);
      setFormData({
        source_type_name: source.alert_source_type?.name || '',
        name: source.name,
        description: source.description,
        webhook_secret: source.webhook_secret,
        field_mappings: source.field_mappings || {},
        settings: source.settings || {},
        notification_channel_uuid: channelUUIDByID(source.notification_channel_id ?? null),
        enabled: source.enabled,
      });
      setIsCreating(false);
    },
    [channelUUIDByID],
  );

  const handleSave = useCallback(async () => {
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
          notification_channel_uuid: formData.notification_channel_uuid,
        });
      } else if (editingSource) {
        await alertSourcesApi.update(editingSource.uuid, {
          name: formData.name,
          description: formData.description,
          webhook_secret: formData.webhook_secret,
          field_mappings: formData.field_mappings,
          settings: formData.settings,
          enabled: formData.enabled,
          notification_channel_uuid: formData.notification_channel_uuid ?? '',
        });
      }

      setIsCreating(false);
      setEditingSource(null);
      setFormData(EMPTY_FORM);
      loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save alert source');
    }
  }, [formData, isCreating, editingSource, loadData]);

  const handleDelete = useCallback(
    async (uuid: string) => {
      if (!confirm('Are you sure you want to delete this alert source?')) return;

      try {
        setError('');
        await alertSourcesApi.delete(uuid);
        loadData();
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to delete alert source');
      }
    },
    [loadData],
  );

  const handleCancel = useCallback(() => {
    setIsCreating(false);
    setEditingSource(null);
    setFormData(EMPTY_FORM);
  }, []);

  const toggleExpand = useCallback((uuid: string) => {
    setExpandedSource((prev) => (prev === uuid ? null : uuid));
  }, []);

  const copyWebhookUrl = useCallback(async (uuid: string) => {
    const url = alertSourcesApi.getWebhookUrl(uuid);
    try {
      await navigator.clipboard.writeText(url);
      setCopiedUrl(uuid);
      setTimeout(() => setCopiedUrl(null), 2000);
    } catch (err) {
      console.error('Failed to copy:', err);
    }
  }, []);

  const selectedType = sourceTypes.find((t) => t.name === formData.source_type_name);

  return {
    sources,
    sourceTypes,
    channels,
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
  };
}
