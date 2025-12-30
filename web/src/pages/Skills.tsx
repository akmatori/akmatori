import { useEffect, useState } from 'react';
import { Plus, Edit2, Trash2, Save, X, Bot, Wrench, Power, PowerOff, Shield, RefreshCw, Eye } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage } from '../components/ErrorMessage';
import PromptEditor from '../components/PromptEditor';
import { skillsApi, toolsApi } from '../api/client';
import type { Skill, ToolInstance } from '../types';

// Modal component for creating/editing skills
interface SkillModalProps {
  isOpen: boolean;
  skill: Skill | null;
  isCreating: boolean;
  isViewOnly: boolean;
  toolInstances: ToolInstance[];
  onClose: () => void;
  onSave: (data: SkillFormData) => Promise<void>;
}

interface SkillFormData {
  name: string;
  description: string;
  prompt: string;
  enabled: boolean;
  toolIds: number[];
}

function SkillModal({ isOpen, skill, isCreating, isViewOnly, toolInstances, onClose, onSave }: SkillModalProps) {
  const [formData, setFormData] = useState<SkillFormData>({
    name: '',
    description: '',
    prompt: '',
    enabled: true,
    toolIds: [],
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
        toolIds: skill.tools?.map(t => t.id) || [],
      });
    } else {
      setFormData({
        name: '',
        description: '',
        prompt: '',
        enabled: true,
        toolIds: [],
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

  const toggleToolSelection = (toolId: number) => {
    if (isViewOnly) return;
    setFormData(prev => ({
      ...prev,
      toolIds: prev.toolIds.includes(toolId)
        ? prev.toolIds.filter(id => id !== toolId)
        : [...prev.toolIds, toolId]
    }));
  };

  if (!isOpen) return null;

  const isSystemSkill = skill?.is_system;

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto">
      <div className="flex min-h-full items-center justify-center p-4">
        {/* Backdrop */}
        <div className="fixed inset-0 bg-black/50 transition-opacity" onClick={onClose} />

        {/* Modal */}
        <div className="relative w-full max-w-2xl bg-white dark:bg-gray-800 rounded-xl shadow-2xl transform transition-all">
          {/* Header */}
          <div className="flex items-center justify-between p-6 border-b border-gray-200 dark:border-gray-700">
            <div className="flex items-center gap-3">
              {isSystemSkill && <Shield className="w-5 h-5 text-primary-500" />}
              <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
                {isCreating ? 'Create Skill' : isViewOnly ? `View: ${skill?.name}` : `Edit: ${skill?.name}`}
              </h2>
              {isSystemSkill && (
                <span className="badge badge-primary text-xs">System</span>
              )}
            </div>
            <button
              onClick={onClose}
              className="p-2 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700"
            >
              <X className="w-5 h-5" />
            </button>
          </div>

          {/* Body */}
          <div className="p-6 space-y-5 max-h-[70vh] overflow-y-auto">
            {error && (
              <div className="p-3 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg text-red-700 dark:text-red-300 text-sm">
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
                      This is the core incident manager. Its prompt is defined in the system configuration and cannot be modified here.
                    </p>
                  </div>
                </div>
              </div>
            )}

            {/* Skill Name */}
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
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
                <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  Must be in kebab-case (lowercase with hyphens)
                </p>
              )}
            </div>

            {/* Description */}
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
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
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Prompt {!isViewOnly && <span className="text-red-500">*</span>}
              </label>
              {isViewOnly ? (
                <div className="bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 rounded-lg p-4 max-h-64 overflow-y-auto">
                  <pre className="text-sm text-gray-700 dark:text-gray-300 whitespace-pre-wrap font-mono">
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
              <div className="flex items-center gap-3 p-3 rounded-lg bg-gray-50 dark:bg-gray-900/50">
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
                  <span className="text-sm text-gray-700 dark:text-gray-300">
                    {formData.enabled ? 'Enabled' : 'Disabled'}
                  </span>
                </label>
              </div>
            )}

            {/* Tool Assignments - only for non-system, non-view-only skills */}
            {!isSystemSkill && !isViewOnly && (
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
                  Tool Connections
                </label>
                <div className="border border-gray-200 dark:border-gray-700 rounded-lg p-3 max-h-48 overflow-y-auto bg-gray-50 dark:bg-gray-900/50">
                  {toolInstances.length === 0 ? (
                    <div className="text-center py-4">
                      <Wrench className="w-6 h-6 mx-auto text-gray-400 mb-1" />
                      <p className="text-gray-500 dark:text-gray-400 text-sm">No tools available</p>
                    </div>
                  ) : (
                    <div className="space-y-2">
                      {toolInstances.filter(tool => tool.enabled).map((tool) => (
                        <div
                          key={tool.id}
                          className={`flex items-center gap-3 p-2 rounded-lg border cursor-pointer transition-all ${
                            formData.toolIds.includes(tool.id)
                              ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
                              : 'border-gray-200 dark:border-gray-700 hover:border-gray-300'
                          }`}
                          onClick={() => toggleToolSelection(tool.id)}
                        >
                          <input
                            type="checkbox"
                            checked={formData.toolIds.includes(tool.id)}
                            onChange={() => toggleToolSelection(tool.id)}
                            onClick={(e) => e.stopPropagation()}
                            className="w-4 h-4"
                          />
                          <div className="flex-1 min-w-0">
                            <div className="font-medium text-gray-900 dark:text-white text-sm truncate">
                              {tool.name}
                            </div>
                            <div className="text-xs text-gray-500 dark:text-gray-400 truncate">
                              {tool.tool_type?.description}
                            </div>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            )}
          </div>

          {/* Footer */}
          <div className="flex items-center justify-end gap-3 p-6 border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50 rounded-b-xl">
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
  const [toolInstances, setToolInstances] = useState<ToolInstance[]>([]);
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
      const [skillsData, toolsData] = await Promise.all([
        skillsApi.list(),
        toolsApi.list(),
      ]);
      setSkills(skillsData || []);
      setToolInstances(toolsData);
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
    let skillName: string;

    if (isCreating) {
      const newSkill = await skillsApi.create({
        name: data.name,
        description: data.description,
        prompt: data.prompt,
      });
      skillName = newSkill.name;
    } else if (editingSkill) {
      await skillsApi.update(editingSkill.name, {
        description: data.description,
        enabled: data.enabled,
        prompt: data.prompt,
      });
      skillName = editingSkill.name;
    } else {
      return;
    }

    // Update tools assignment (only for non-system skills)
    if (!editingSkill?.is_system) {
      await skillsApi.updateTools(skillName, data.toolIds);
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
        description="Manage your AI skills and their tool assignments"
        action={
          <div className="flex gap-2">
            <button onClick={handleSync} disabled={syncing} className="btn btn-secondary">
              <RefreshCw className={`w-4 h-4 ${syncing ? 'animate-spin' : ''}`} />
              {syncing ? 'Syncing...' : 'Sync'}
            </button>
            <button onClick={handleCreate} className="btn btn-primary">
              <Plus className="w-4 h-4" />
              New Skill
            </button>
          </div>
        }
      />

      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message={success} />}

      {loading ? (
        <LoadingSpinner />
      ) : (
        <div className="card">
          {sortedSkills.length === 0 ? (
            <div className="py-16 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
              <Bot className="w-12 h-12 mx-auto text-gray-400 mb-3" />
              <p className="text-gray-500 dark:text-gray-400">No skills yet</p>
              <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">Create one or sync from filesystem</p>
            </div>
          ) : (
            <div className="divide-y divide-gray-200 dark:divide-gray-700">
              {sortedSkills.map((skill) => (
                <div
                  key={skill.id}
                  onClick={() => handleEdit(skill)}
                  className={`flex items-center justify-between p-4 cursor-pointer transition-all hover:bg-gray-50 dark:hover:bg-gray-800/50 ${
                    !skill.enabled ? 'opacity-60' : ''
                  } ${
                    skill.is_system ? 'bg-primary-50/50 dark:bg-primary-900/10' : ''
                  }`}
                >
                  <div className="flex items-center gap-4 min-w-0 flex-1">
                    {/* Icon */}
                    <div className={`p-2 rounded-lg flex-shrink-0 ${
                      skill.is_system
                        ? 'bg-primary-100 dark:bg-primary-900/30'
                        : 'bg-gray-100 dark:bg-gray-700'
                    }`}>
                      {skill.is_system ? (
                        <Shield className="w-5 h-5 text-primary-600 dark:text-primary-400" />
                      ) : (
                        <Bot className="w-5 h-5 text-gray-600 dark:text-gray-400" />
                      )}
                    </div>

                    {/* Content */}
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2 flex-wrap">
                        <h3 className="font-semibold text-gray-900 dark:text-white font-mono">
                          {skill.name}
                        </h3>
                        {skill.is_system && (
                          <span className="badge badge-primary text-xs">System</span>
                        )}
                        <span className={`badge text-xs ${skill.enabled ? 'badge-success' : 'badge-default'}`}>
                          {skill.enabled ? 'Enabled' : 'Disabled'}
                        </span>
                        {skill.tools && skill.tools.length > 0 && (
                          <span className="badge badge-warning text-xs">
                            <Wrench className="w-3 h-3" />
                            {skill.tools.length} tool{skill.tools.length > 1 ? 's' : ''}
                          </span>
                        )}
                      </div>
                      <p className="text-sm text-gray-500 dark:text-gray-400 mt-1 truncate">
                        {skill.description || 'No description'}
                      </p>
                    </div>
                  </div>

                  {/* Actions */}
                  <div className="flex items-center gap-2 ml-4 flex-shrink-0" onClick={(e) => e.stopPropagation()}>
                    {skill.is_system ? (
                      <button
                        onClick={() => handleView(skill)}
                        className="p-2 text-gray-400 hover:text-primary-600 dark:hover:text-primary-400 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                        title="View"
                      >
                        <Eye className="w-4 h-4" />
                      </button>
                    ) : (
                      <>
                        <button
                          onClick={() => handleEdit(skill)}
                          className="p-2 text-gray-400 hover:text-primary-600 dark:hover:text-primary-400 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                          title="Edit"
                        >
                          <Edit2 className="w-4 h-4" />
                        </button>
                        <button
                          onClick={() => handleDelete(skill)}
                          className="p-2 text-gray-400 hover:text-red-600 dark:hover:text-red-400 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
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
        toolInstances={toolInstances}
        onClose={closeModal}
        onSave={handleSave}
      />
    </div>
  );
}
