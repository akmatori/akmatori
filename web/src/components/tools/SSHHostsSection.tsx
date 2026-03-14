import { Plus, Trash2, ChevronDown, ChevronUp, AlertTriangle, Server, Key } from 'lucide-react';
import type { SSHKey, SSHHostConfig } from '../../types';

interface SSHHostsSectionProps {
  hosts: SSHHostConfig[];
  expandedHosts: number[];
  sshKeys: SSHKey[];
  getDefaultKey: () => SSHKey | undefined;
  onAddHost: () => void;
  onRemoveHost: (index: number) => void;
  onUpdateHost: (index: number, field: string, value: any) => void;
  onToggleHostExpand: (index: number) => void;
}

export default function SSHHostsSection({
  hosts,
  expandedHosts,
  sshKeys,
  getDefaultKey,
  onAddHost,
  onRemoveHost,
  onUpdateHost,
  onToggleHostExpand,
}: SSHHostsSectionProps) {
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
          SSH Hosts <span className="text-red-500">*</span>
        </label>
        <button
          type="button"
          onClick={onAddHost}
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
                onClick={() => onToggleHostExpand(index)}
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
                onClick={() => onRemoveHost(index)}
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
                onChange={(e) => onUpdateHost(index, 'hostname', e.target.value)}
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
                onChange={(e) => onUpdateHost(index, 'address', e.target.value)}
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
                    onChange={(e) => onUpdateHost(index, 'user', e.target.value)}
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
                    onChange={(e) => onUpdateHost(index, 'port', e.target.value ? parseInt(e.target.value) : undefined)}
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
                    onChange={(e) => onUpdateHost(index, 'key_id', e.target.value || undefined)}
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
                      onChange={(e) => onUpdateHost(index, 'jumphost_address', e.target.value)}
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">User</label>
                    <input
                      type="text"
                      className="input-field"
                      placeholder="(same as host)"
                      value={host.jumphost_user || ''}
                      onChange={(e) => onUpdateHost(index, 'jumphost_user', e.target.value)}
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">Port</label>
                    <input
                      type="number"
                      className="input-field"
                      placeholder="22"
                      value={host.jumphost_port || ''}
                      onChange={(e) => onUpdateHost(index, 'jumphost_port', e.target.value ? parseInt(e.target.value) : undefined)}
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
                  onChange={(e) => onUpdateHost(index, 'allow_write_commands', e.target.checked)}
                  className="w-4 h-4"
                />
              </div>
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
