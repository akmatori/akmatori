import { Plus, Save, X, Trash2, Key, Star } from 'lucide-react';
import type { SSHKey } from '../../types';

interface SSHKeysSectionProps {
  sshKeys: SSHKey[];
  sshKeysLoading: boolean;
  isCreating: boolean;
  editingToolId: number | undefined;
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
}

export default function SSHKeysSection({
  sshKeys,
  sshKeysLoading,
  isCreating,
  editingToolId,
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
}: SSHKeysSectionProps) {
  const defaultKey = getDefaultKey();

  return (
    <div className="space-y-4 mb-6">
      <div className="flex items-center justify-between">
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
          <Key className="w-4 h-4 inline mr-1" />
          SSH Keys
        </label>
        {!showAddKey && editingToolId && (
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
                onClick={onAddSSHKey}
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
                        onClick={() => onSetDefaultKey(key.id)}
                        className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 text-xs"
                      >
                        Set as default
                      </button>
                    )}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button
                      type="button"
                      onClick={() => onDeleteSSHKey(key.id)}
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
}
