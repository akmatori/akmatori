import { Plus, Save, X, Trash2, Key, Star, Shield, ShieldAlert, ShieldCheck } from 'lucide-react';
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
  // Command Policies (tool-level)
  denyList: string[];
  setDenyList: (patterns: string[]) => void;
  allowList: string[];
  setAllowList: (patterns: string[]) => void;
  allowWriteCommands: boolean;
  setAllowWriteCommands: (val: boolean) => void;
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
  denyList,
  setDenyList,
  allowList,
  setAllowList,
  allowWriteCommands,
  setAllowWriteCommands,
}: SSHKeysSectionProps) {
  const defaultKey = getDefaultKey();

  // Helper: add pattern to list
  const addPattern = (list: string[], setList: (p: string[]) => void, pattern: string) => {
    const trimmed = pattern.trim();
    if (trimmed && !list.includes(trimmed)) {
      setList([...list, trimmed]);
    }
  };

  // Helper: remove pattern from list
  const removePattern = (list: string[], setList: (p: string[]) => void, idx: number) => {
    const next = [...list];
    next.splice(idx, 1);
    setList(next);
  };

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

      {/* Command Policies Section */}
      {editingToolId && (
        <div className="border border-gray-200 dark:border-gray-700 rounded-lg p-4 space-y-4 bg-gray-50 dark:bg-gray-900/50">
          <div className="flex items-center gap-2">
            <Shield className="w-4 h-4 text-gray-600 dark:text-gray-400" />
            <h4 className="font-medium text-gray-900 dark:text-white">Command Policies</h4>
          </div>
          <p className="text-xs text-gray-500 dark:text-gray-400">
            These policies apply to ALL hosts in this SSH tool instance. Commands are validated through a 4-stage pipeline:
            deny list → read-only list → allow list → destructive gate.
          </p>

          {/* Allow Write Commands Toggle */}
          <div className="flex items-center justify-between">
            <div>
              <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
                <ShieldCheck className="w-3 h-3 inline mr-1" />
                Allow Write/Destructive Commands
              </label>
              <p className="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                Permits commands not in the default read-only list. Commands in the deny list are still blocked.
              </p>
            </div>
            <label className="relative inline-flex items-center cursor-pointer">
              <input
                type="checkbox"
                checked={allowWriteCommands}
                onChange={(e) => setAllowWriteCommands(e.target.checked)}
                className="sr-only peer"
              />
              <div className="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-4 peer-focus:ring-yellow-300 dark:peer-focus:ring-yellow-800 rounded-full peer dark:bg-gray-700 peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:border-gray-600 peer-checked:bg-yellow-500"></div>
            </label>
          </div>

          {/* Deny List */}
          <div>
            <div className="flex items-center gap-2 mb-1">
              <ShieldAlert className="w-3 h-3 text-red-500" />
              <label className="text-xs font-medium text-gray-700 dark:text-gray-300">
                Deny List (always blocked)
              </label>
            </div>
            <p className="text-xs text-gray-500 dark:text-gray-400 mb-2">
              Claude Code wildcard syntax: <code className="px-1 py-0.5 bg-gray-100 dark:bg-gray-800 rounded">rm *</code> blocks "rm -rf /", <code className="px-1 py-0.5 bg-gray-100 dark:bg-gray-800 rounded">shutdown</code> blocks exact "shutdown"
            </p>
            <div className="flex flex-wrap gap-1 mb-2 min-h-[32px] p-2 border border-gray-200 dark:border-gray-700 rounded bg-white dark:bg-gray-800">
              {denyList.map((pattern, idx) => (
                <span key={idx} className="inline-flex items-center gap-1 px-2 py-0.5 bg-red-100 dark:bg-red-900/30 text-red-800 dark:text-red-300 rounded text-xs">
                  {pattern}
                  <button
                    type="button"
                    onClick={() => removePattern(denyList, setDenyList, idx)}
                    className="hover:text-red-900 dark:hover:text-red-100"
                  >
                    ×
                  </button>
                </span>
              ))}
            </div>
            <div className="flex gap-2">
              <input
                type="text"
                className="input-field flex-1"
                placeholder="e.g., rm *, shutdown, kill *"
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ',') {
                    e.preventDefault();
                    const val = (e.target as HTMLInputElement).value.trim().replace(/,/g, '');
                    addPattern(denyList, setDenyList, val);
                    (e.target as HTMLInputElement).value = '';
                  }
                }}
              />
            </div>
          </div>

          {/* Allow List */}
          <div>
            <div className="flex items-center gap-2 mb-1">
              <ShieldCheck className="w-3 h-3 text-green-500" />
              <label className="text-xs font-medium text-gray-700 dark:text-gray-300">
                Allow List (explicitly permitted)
              </label>
            </div>
            <p className="text-xs text-gray-500 dark:text-gray-400 mb-2">
              Commands matching these patterns are allowed even if not in the default read-only list. Deny list still applies.
            </p>
            <div className="flex flex-wrap gap-1 mb-2 min-h-[32px] p-2 border border-gray-200 dark:border-gray-700 rounded bg-white dark:bg-gray-800">
              {allowList.map((pattern, idx) => (
                <span key={idx} className="inline-flex items-center gap-1 px-2 py-0.5 bg-green-100 dark:bg-green-900/30 text-green-800 dark:text-green-300 rounded text-xs">
                  {pattern}
                  <button
                    type="button"
                    onClick={() => removePattern(allowList, setAllowList, idx)}
                    className="hover:text-green-900 dark:hover:text-green-100"
                  >
                    ×
                  </button>
                </span>
              ))}
            </div>
            <div className="flex gap-2">
              <input
                type="text"
                className="input-field flex-1"
                placeholder="e.g., curl *, wget *, scp *"
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ',') {
                    e.preventDefault();
                    const val = (e.target as HTMLInputElement).value.trim().replace(/,/g, '');
                    addPattern(allowList, setAllowList, val);
                    (e.target as HTMLInputElement).value = '';
                  }
                }}
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
