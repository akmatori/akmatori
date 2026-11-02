import EmptyState from '../components/shared/EmptyState';
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Plus, Edit2, Trash2, Save, X, Bot, Wrench, Power, PowerOff, Shield, RefreshCw, Eye } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage } from '../components/ErrorMessage';
import PromptEditor from '../components/PromptEditor';
import { skillsApi } from '../api/client';
import type { Skill } from '../types';

// CONNECTED_TOOLS_SKILL is the system skill the backend generates from the
// enabled connectors. It is always read-only.
const CONNECTED_TOOLS_SKILL = 'connected-tools';
const CONNECTED_TOOLS_NOTE = 'Generated from your enabled connectors. Lists every tool the agent can call.';

function isConnectedTools(skill: Skill | null | undefined): boolean {
  return skill?.name === CONNECTED_TOOLS_SKILL;
}

// Modal component for creating/editing skills
interface SkillModalProps {
  isOpen: boolean;
  skill: Skill | null;
  isCreating: boolean;
  isViewOnly: boolean;
  onClose: () => void;
  onSave: (data: SkillFormData) => Promise<void>;
}

interface SkillFormData {
  name: string;
  description: string;
  prompt: string;
  enabled: boolean;
}

function SkillModal({ isOpen, skill, isCreating, isViewOnly, onClose, onSave }: SkillModalProps) {
  const [formData, setFormData] = useState<SkillFormData>({
    name: '',
    description: '',
    prompt: '',
    enabled: true,
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (skill) {
      setFormData({
        name: skill.name,
        description: skill.description || '',
        prompt: skill.prompt || '',
        enabled: skill.enabled,
      });
    } else {
      setFormData({
        name: '',
        description: '',
        prompt: '',
        enabled: true,
      });
    }
    setError('');
  }, [skill, isOpen]);

  const handleSave = async () => {
    if (isViewOnly) return;

    try {
      setError('');
      setSaving(true);

      if (!formData.name.trim() || !formData.description.trim() || !formData.prompt.trim()) {
        setError('Name, description, and prompt are required');
        return;
      }

      if (isCreating && (!/^[a-z][a-z0-9-]*[a-z0-9]$/.test(formData.name) && !/^[a-z]$/.test(formData.name))) {
        setError('Name must be in kebab-case (lowercase letters, numbers, and hyphens; must start with a letter)');
        return;
      }

      await onSave(formData);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save skill');
    } finally {
      setSaving(false);
    }
  };

  if (!isOpen) return null;

  const isSystemSkill = skill?.is_system;

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto">
      <div className="flex min-h-full items-center justify-center p-4">
        {/* Backdrop */}
        <div className="fixed inset-0 bg-black/50 transition-opacity" onClick={onClose} />

        {/* Modal */}
        <div className="relative w-full max-w-2xl bg-surface rounded-xl shadow-2xl transform transition-all">
          {/* Header */}
          <div className="flex items-center justify-between p-6 border-b border-line">
            <div className="flex items-center gap-3">
              {isSystemSkill && <Shield className="w-5 h-5 text-primary-500" />}
              <h2 className="text-lg font-semibold text-fg">
                {isCreating ? 'Create skill' : isViewOnly ? `View: ${skill?.name}` : `Edit: ${skill?.name}`}
              </h2>
              {isSystemSkill && (
                <span className="badge badge-primary text-xs">System</span>
              )}
            </div>
            <button
              onClick={onClose}
              className="p-2 text-gray-400 hover:text-fg rounded-lg hover:bg-surface-2"
            >
              <X className="w-5 h-5" />
            </button>
          </div>

          {/* Body */}
          <div className="p-6 space-y-5 max-h-[70vh] overflow-y-auto">
            {error && (
              <div className="p-3 bg-state-fail/10 border border-state-fail/40 rounded-lg text-state-fail text-sm">
                {error}
              </div>
            )}

            {/* System skill notice */}
            {isViewOnly && isSystemSkill && (
              <div className="p-3 rounded-lg bg-primary-50 dark:bg-primary-900/20 border border-primary-200 dark:border-primary-800">
                <div className="flex items-start gap-2">
                  <Shield className="w-4 h-4 text-primary-600 dark:text-primary-400 mt-0.5 flex-shrink-0" />
                  <div className="text-sm text-primary-800 dark:text-primary-200">
                    <p className="font-medium">System Skill (Read-Only)</p>
                    <p className="mt-1 text-primary-600 dark:text-primary-300">
                      {isConnectedTools(skill) ? (
                        <>
                          {CONNECTED_TOOLS_NOTE} Manage them on the{' '}
                          <Link to="/connectors" className="underline" onClick={onClose}>
                            Connectors
                          </Link>{' '}
                          page.
                        </>
                      ) : (
                        'This is the core incident manager. Its prompt is defined in the system configuration and cannot be modified here.'
                      )}
                    </p>
                  </div>
                </div>
              </div>
            )}

            {/* Skill Name */}
            <div>
              <label className="block text-sm font-medium text-fg mb-1">
                Skill Name {!isViewOnly && <span className="text-red-500">*</span>}
              </label>
              <input
                type="text"
                className="input-field"
                placeholder="e.g., zabbix-analyst"
                value={formData.name}
                onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                disabled={!isCreating || isViewOnly}
              />
              {isCreating && !isViewOnly && (
                <p className="mt-1 text-xs text-fg-subtle">
                  Must be in kebab-case (lowercase with hyphens)
                </p>
              )}
            </div>

            {/* Description */}
            <div>
              <label className="block text-sm font-medium text-fg mb-1">
                Description {!isViewOnly && <span className="text-red-500">*</span>}
              </label>
              <textarea
                className="input-field"
                rows={2}
                placeholder="Short description of what this skill does"
                value={formData.description}
                onChange={(e) => setFormData({ ...formData, description: e.target.value })}
                disabled={isViewOnly}
              />
            </div>

            {/* Prompt */}
            <div>
              <label className="block text-sm font-medium text-fg mb-1">
                Prompt {!isViewOnly && <span className="text-red-500">*</span>}
              </label>
              {isViewOnly ? (
                <div className="bg-surface-2 border border-line rounded-lg p-4 max-h-64 overflow-y-auto">
                  <pre className="text-sm text-fg whitespace-pre-wrap font-mono">
                    {formData.prompt}
                  </pre>
                </div>
              ) : (
                <PromptEditor
                  value={formData.prompt}
                  onChange={(value) => setFormData({ ...formData, prompt: value })}
                  placeholder="Define the skill's role, expertise, and behavior..."
                  rows={8}
                />
              )}
            </div>

            {/* Enabled Toggle - not for view only */}
            {!isViewOnly && (
              <div className="flex items-center gap-3 p-3 rounded-lg bg-surface-2">
                <input
                  type="checkbox"
                  id="enabled"
                  checked={formData.enabled}
                  onChange={(e) => setFormData({ ...formData, enabled: e.target.checked })}
                  className="w-4 h-4"
                />
                <label htmlFor="enabled" className="flex items-center gap-2 cursor-pointer">
                  {formData.enabled ? (
                    <Power className="w-4 h-4 text-green-500" />
                  ) : (
                    <PowerOff className="w-4 h-4 text-gray-400" />
                  )}
                  <span className="text-sm text-fg">
                    {formData.enabled ? 'Enabled' : 'Disabled'}
                  </span>
                </label>
              </div>
            )}

            {/* Tools hint - tools are not chosen per skill */}
            {!isSystemSkill && (
              <p className="flex items-start gap-2 text-xs text-fg-subtle">
                <Wrench className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
                <span>
                  Tools come from{' '}
                  <Link to="/connectors" className="text-primary-400 hover:underline" onClick={onClose}>
                    Connectors
                  </Link>{' '}
                  and are available to every skill.
                </span>
              </p>
            )}
          </div>

          {/* Footer */}
          <div className="flex items-center justify-end gap-3 p-6 border-t border-line bg-surface-2 rounded-b-xl">
            <button onClick={onClose} disabled={saving} className="btn btn-secondary">
              {isViewOnly ? 'Close' : 'Cancel'}
            </button>
            {!isViewOnly && (
              <button onClick={handleSave} disabled={saving} className="btn btn-primary">
                <Save className="w-4 h-4" />
                {saving ? 'Saving...' : 'Save'}
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

export default function Skills() {
  const [skills, setSkills] = useState<Skill[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const [syncing, setSyncing] = useState(false);

  // Modal state
  const [modalOpen, setModalOpen] = useState(false);
  const [editingSkill, setEditingSkill] = useState<Skill | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [isViewOnly, setIsViewOnly] = useState(false);

  useEffect(() => {
    loadData();
  }, []);

  const loadData = async () => {
    try {
      setLoading(true);
      setError('');
      const skillsData = await skillsApi.list();
      setSkills(skillsData || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load data');
    } finally {
      setLoading(false);
    }
  };

  const handleSync = async () => {
    try {
      setSyncing(true);
      setError('');
      await skillsApi.sync();
      setSuccess('Skills synced from filesystem');
      setTimeout(() => setSuccess(''), 3000);
      await loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to sync skills');
    } finally {
      setSyncing(false);
    }
  };

  const handleCreate = () => {
    setEditingSkill(null);
    setIsCreating(true);
    setIsViewOnly(false);
    setModalOpen(true);
  };

  const handleView = (skill: Skill) => {
    setEditingSkill(skill);
    setIsCreating(false);
    setIsViewOnly(true);
    setModalOpen(true);
  };

  const handleEdit = (skill: Skill) => {
    setEditingSkill(skill);
    setIsCreating(false);
    setIsViewOnly(skill.is_system); // System skills are view-only
    setModalOpen(true);
  };

  const handleSave = async (data: SkillFormData) => {
    if (isCreating) {
      await skillsApi.create({
        name: data.name,
        description: data.description,
        prompt: data.prompt,
      });
    } else if (editingSkill) {
      await skillsApi.update(editingSkill.name, {
        description: data.description,
        enabled: data.enabled,
        prompt: data.prompt,
      });
    } else {
      return;
    }

    setSuccess('Skill saved successfully');
    setTimeout(() => setSuccess(''), 3000);
    await loadData();
  };

  const handleDelete = async (skill: Skill) => {
    if (skill.is_system) return;
    if (!confirm(`Delete skill "${skill.name}"? This cannot be undone.`)) return;

    try {
      setError('');
      await skillsApi.delete(skill.name);
      setSuccess('Skill deleted');
      setTimeout(() => setSuccess(''), 3000);
      await loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete skill');
    }
  };

  const closeModal = () => {
    setModalOpen(false);
    setEditingSkill(null);
    setIsCreating(false);
    setIsViewOnly(false);
  };

  // Sort skills: system skills first, then alphabetically
  const sortedSkills = [...skills].sort((a, b) => {
    if (a.is_system && !b.is_system) return -1;
    if (!a.is_system && b.is_system) return 1;
    return a.name.localeCompare(b.name);
  });

  return (
    <div>
      <PageHeader
        title="Skills"
        description="Manage your AI skills. Tools come from Connectors and are available to every skill."
        action={
          <div className="flex gap-2">
            <button onClick={handleSync} disabled={syncing} className="btn btn-secondary">
              <RefreshCw className={`w-4 h-4 ${syncing ? 'animate-spin' : ''}`} />
              {syncing ? 'Syncing...' : 'Sync'}
            </button>
            <button onClick={handleCreate} className="btn btn-primary">
              <Plus className="w-4 h-4" />
              New skill
            </button>
          </div>
        }
      />

      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message={success} />}

      {loading ? (
        <LoadingSpinner />
      ) : (
        <div className="card p-0 overflow-hidden">
          {sortedSkills.length === 0 ? (
            <EmptyState icon={Bot} title="No skills yet" description="Create one or sync from filesystem" />
          ) : (
            <div className="divide-y divide-line">
              {sortedSkills.map((skill) => (
                <div
                  key={skill.id}
                  onClick={() => handleEdit(skill)}
                  className={`flex items-center justify-between p-4 cursor-pointer transition-all hover:bg-surface-2 ${
                    !skill.enabled ? 'opacity-60' : ''
                  } ${
                    ''
                  }`}
                >
                  <div className="flex items-center gap-4 min-w-0 flex-1">
                    {/* Icon */}
                    <div className={`p-2 rounded-lg flex-shrink-0 ${
                      skill.is_system
                        ? 'bg-primary-100 dark:bg-primary-900/30'
                        : 'bg-surface-2'
                    }`}>
                      {skill.is_system ? (
                        <Shield className="w-5 h-5 text-primary-600 dark:text-primary-400" />
                      ) : (
                        <Bot className="w-5 h-5 text-fg-muted" />
                      )}
                    </div>

                    {/* Content */}
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2 flex-wrap">
                        <h3 className="font-medium text-fg font-mono">
                          {skill.name}
                        </h3>
                        {skill.is_system && (
                          <span className="badge badge-primary text-xs">System</span>
                        )}
                        <span className={`badge text-xs ${skill.enabled ? 'badge-success' : 'badge-default'}`}>
                          {skill.enabled ? 'Enabled' : 'Disabled'}
                        </span>
                        {isConnectedTools(skill) && (
                          <span className="badge badge-default text-xs" title={CONNECTED_TOOLS_NOTE}>
                            <Wrench className="w-3 h-3" />
                            Generated
                          </span>
                        )}
                      </div>
                      <p className="text-sm text-fg-subtle mt-1 truncate">
                        {isConnectedTools(skill)
                          ? CONNECTED_TOOLS_NOTE
                          : skill.description || 'No description'}
                      </p>
                    </div>
                  </div>

                  {/* Actions */}
                  <div className="flex items-center gap-2 ml-4 flex-shrink-0" onClick={(e) => e.stopPropagation()}>
                    {skill.is_system ? (
                      <button
                        onClick={() => handleView(skill)}
                        className="p-2 text-gray-400 hover:text-primary-600 dark:hover:text-primary-400 rounded-lg hover:bg-surface-2 transition-colors"
                        title="View"
                      >
                        <Eye className="w-4 h-4" />
                      </button>
                    ) : (
                      <>
                        <button
                          onClick={() => handleEdit(skill)}
                          className="p-2 text-gray-400 hover:text-primary-600 dark:hover:text-primary-400 rounded-lg hover:bg-surface-2 transition-colors"
                          title="Edit"
                        >
                          <Edit2 className="w-4 h-4" />
                        </button>
                        <button
                          onClick={() => handleDelete(skill)}
                          className="p-2 text-gray-400 hover:text-state-fail rounded-lg hover:bg-surface-2 transition-colors"
                          title="Delete"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Skill Modal */}
      <SkillModal
        isOpen={modalOpen}
        skill={editingSkill}
        isCreating={isCreating}
        isViewOnly={isViewOnly}
        onClose={closeModal}
        onSave={handleSave}
      />
    </div>
  );
}
