import { Plus, Save, X, Trash2, Key, Star, Shield, ShieldAlert, ShieldCheck, Info } from 'lucide-react';
import type { SSHKey } from '../../types';
import { useState } from 'react';

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
  const [showCommandPolicyHelp, setShowCommandPolicyHelp] = useState(false);
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
            <button
              type="button"
              onClick={() => setShowCommandPolicyHelp(true)}
              className="btn btn-ghost btn-sm p-1 text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-200"
              title="Command Policy Documentation"
            >
              <Info className="w-4 h-4" />
            </button>
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

      {/* Command Policy Help Modal */}
      {showCommandPolicyHelp && (
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
