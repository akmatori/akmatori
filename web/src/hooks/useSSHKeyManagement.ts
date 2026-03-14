import { useState, useCallback } from 'react';
import { sshKeysApi } from '../api/client';
import type { SSHKey } from '../types';

export function useSSHKeyManagement(setError: (error: string) => void) {
  const [sshKeys, setSshKeys] = useState<SSHKey[]>([]);
  const [showAddKey, setShowAddKey] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [newKeyValue, setNewKeyValue] = useState('');
  const [newKeyIsDefault, setNewKeyIsDefault] = useState(false);
  const [sshKeysLoading, setSshKeysLoading] = useState(false);

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

  const handleAddSSHKey = useCallback(async (toolId: number) => {
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
      await sshKeysApi.create(toolId, {
        name: newKeyName,
        private_key: newKeyValue,
        is_default: newKeyIsDefault || sshKeys.length === 0,
      });
      setShowAddKey(false);
      setNewKeyName('');
      setNewKeyValue('');
      setNewKeyIsDefault(false);
      await loadSSHKeys(toolId);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add SSH key');
    }
  }, [newKeyName, newKeyValue, newKeyIsDefault, sshKeys.length, loadSSHKeys, setError]);

  const handleDeleteSSHKey = useCallback(async (toolId: number, keyId: string) => {
    if (!confirm('Are you sure you want to delete this SSH key?')) return;

    try {
      setError('');
      await sshKeysApi.delete(toolId, keyId);
      await loadSSHKeys(toolId);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete SSH key');
    }
  }, [loadSSHKeys, setError]);

  const handleSetDefaultKey = useCallback(async (toolId: number, keyId: string) => {
    try {
      setError('');
      await sshKeysApi.update(toolId, keyId, { is_default: true });
      await loadSSHKeys(toolId);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to set default key');
    }
  }, [loadSSHKeys, setError]);

  const getDefaultKey = useCallback(() => sshKeys.find(k => k.is_default), [sshKeys]);

  const resetKeyForm = useCallback(() => {
    setShowAddKey(false);
    setNewKeyName('');
    setNewKeyValue('');
    setNewKeyIsDefault(false);
  }, []);

  return {
    sshKeys,
    showAddKey,
    setShowAddKey,
    newKeyName,
    setNewKeyName,
    newKeyValue,
    setNewKeyValue,
    newKeyIsDefault,
    setNewKeyIsDefault,
    sshKeysLoading,
    loadSSHKeys,
    handleAddSSHKey,
    handleDeleteSSHKey,
    handleSetDefaultKey,
    getDefaultKey,
    resetKeyForm,
  };
}
