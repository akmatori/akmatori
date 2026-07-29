import { Plus, Trash2, ChevronDown, ChevronUp, Server, Key, Info, X, ShieldCheck, ShieldAlert } from 'lucide-react';
import type { SSHKey, SSHHostConfig } from '../../types';
import { useState } from 'react';

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
  const [showCommandPolicyHelp, setShowCommandPolicyHelp] = useState(false);

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
              <div className="flex items-center justify-between">
                <p className="text-xs text-gray-500 dark:text-gray-400 font-medium">Advanced Options</p>
                <button
                  type="button"
                  onClick={() => setShowCommandPolicyHelp(true)}
                  className="btn btn-ghost btn-sm p-1 text-blue-500 hover:text-blue-700 dark:hover:text-blue-300"
                  title="Command Policy Documentation"
                >
                  <Info className="w-4 h-4" />
                </button>
              </div>

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

              {/* Info: Command Policies are configured at tool level */}
              <div className="bg-blue-50 dark:bg-blue-900/20 rounded-lg p-3 border border-blue-200 dark:border-blue-800">
                <p className="text-xs text-blue-700 dark:text-blue-300">
                  <strong>Command Policies:</strong> Command validation (deny list, allow list, write commands) is configured at the tool level in the SSH Keys section. These settings apply to ALL hosts in this tool instance.
                </p>
              </div>
            </div>
          )}
        </div>
      ))}

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
