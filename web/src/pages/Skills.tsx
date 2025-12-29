import { useEffect, useState } from 'react';
import { Plus, Edit2, Trash2, Save, X, Bot, Wrench, Power, PowerOff, FileCode, ChevronDown, ChevronRight, Eye, Loader2, RefreshCw } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage } from '../components/ErrorMessage';
import PromptEditor from '../components/PromptEditor';
import ScriptViewerModal from '../components/ScriptViewerModal';
import { skillsApi, toolsApi, scriptsApi } from '../api/client';
import type { Skill, ToolInstance, ScriptInfo } from '../types';

export default function Skills() {
  const [skills, setSkills] = useState<Skill[]>([]);
  const [toolInstances, setToolInstances] = useState<ToolInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const [editingSkill, setEditingSkill] = useState<Skill | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [formData, setFormData] = useState({ name: '', description: '', category: '', prompt: '', enabled: true });
  const [selectedToolIds, setSelectedToolIds] = useState<number[]>([]);
  const [syncing, setSyncing] = useState(false);

  // Scripts state (keyed by skill name instead of ID)
  const [expandedScripts, setExpandedScripts] = useState<Set<string>>(new Set());
  const [skillScripts, setSkillScripts] = useState<Record<string, string[]>>({});
  const [scriptsLoading, setScriptsLoading] = useState<Set<string>>(new Set());
  const [scriptModalOpen, setScriptModalOpen] = useState(false);
  const [selectedScript, setSelectedScript] = useState<ScriptInfo | null>(null);
  const [scriptLoading, setScriptLoading] = useState(false);
  const [currentSkillName, setCurrentSkillName] = useState<string | null>(null);

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
    setIsCreating(true);
    setFormData({ name: '', description: '', category: '', prompt: '', enabled: true });
    setSelectedToolIds([]);
    setEditingSkill(null);
  };

  const handleEdit = (skill: Skill) => {
    setEditingSkill(skill);
    setFormData({
      name: skill.name,
      description: skill.description || '',
      category: skill.category || '',
      prompt: skill.prompt,
      enabled: skill.enabled
    });
    setSelectedToolIds(skill.tools?.map(t => t.id) || []);
    setIsCreating(false);
  };

  const handleSave = async () => {
    try {
      setError('');

      if (!formData.name.trim() || !formData.description.trim() || !formData.prompt.trim()) {
        setError('Name, description, and prompt are required');
        return;
      }

      // Validate kebab-case format
      if (!/^[a-z][a-z0-9-]*[a-z0-9]$/.test(formData.name) && !/^[a-z]$/.test(formData.name)) {
        setError('Name must be in kebab-case (lowercase letters, numbers, and hyphens; must start with a letter)');
        return;
      }

      let skillName: string;
      if (isCreating) {
        const newSkill = await skillsApi.create({
          name: formData.name,
          description: formData.description,
          category: formData.category,
          prompt: formData.prompt,
        });
        skillName = newSkill.name;
      } else if (editingSkill) {
        await skillsApi.update(editingSkill.name, {
          description: formData.description,
          category: formData.category,
          enabled: formData.enabled,
          prompt: formData.prompt,
        });
        skillName = editingSkill.name;
      } else {
        return;
      }

      // Update tools assignment (triggers symlinks + tools.md generation)
      await skillsApi.updateTools(skillName, selectedToolIds);

      setIsCreating(false);
      setEditingSkill(null);
      setFormData({ name: '', description: '', category: '', prompt: '', enabled: true });
      setSelectedToolIds([]);
      setSuccess('Skill saved successfully');
      setTimeout(() => setSuccess(''), 3000);
      loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save skill');
    }
  };

  const handleDelete = async (name: string) => {
    if (!confirm('Are you sure you want to delete this skill?')) return;

    try {
      setError('');
      await skillsApi.delete(name);
      loadData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete skill');
    }
  };

  const handleCancel = () => {
    setIsCreating(false);
    setEditingSkill(null);
    setFormData({ name: '', description: '', category: '', prompt: '', enabled: true });
    setSelectedToolIds([]);
  };

  const toggleToolSelection = (toolId: number) => {
    setSelectedToolIds(prev =>
      prev.includes(toolId)
        ? prev.filter(id => id !== toolId)
        : [...prev, toolId]
    );
  };

  // Scripts handlers (use skill name instead of ID)
  const toggleScriptsExpanded = async (skillName: string) => {
    const newExpanded = new Set(expandedScripts);
    if (newExpanded.has(skillName)) {
      newExpanded.delete(skillName);
    } else {
      newExpanded.add(skillName);
      // Load scripts if not already loaded
      if (!skillScripts[skillName]) {
        await loadSkillScripts(skillName);
      }
    }
    setExpandedScripts(newExpanded);
  };

  const loadSkillScripts = async (skillName: string) => {
    try {
      setScriptsLoading(prev => new Set(prev).add(skillName));
      const response = await scriptsApi.list(skillName);
      setSkillScripts(prev => ({ ...prev, [skillName]: response.scripts || [] }));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load scripts');
    } finally {
      setScriptsLoading(prev => {
        const next = new Set(prev);
        next.delete(skillName);
        return next;
      });
    }
  };

  const handleViewScript = async (skillName: string, filename: string) => {
    try {
      setCurrentSkillName(skillName);
      setScriptModalOpen(true);
      setScriptLoading(true);
      setSelectedScript(null);
      const scriptInfo = await scriptsApi.get(skillName, filename);
      setSelectedScript(scriptInfo);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load script');
      setScriptModalOpen(false);
    } finally {
      setScriptLoading(false);
    }
  };

  const handleSaveScript = async (content: string) => {
    if (!currentSkillName || !selectedScript) return;
    await scriptsApi.update(currentSkillName, selectedScript.filename, content);
    // Reload the script to get updated info
    const updatedScript = await scriptsApi.get(currentSkillName, selectedScript.filename);
    setSelectedScript(updatedScript);
  };

  const handleDeleteScript = async (skillName: string, filename: string) => {
    if (!confirm(`Delete script "${filename}"? This cannot be undone.`)) return;

    try {
      setError('');
      await scriptsApi.delete(skillName, filename);
      // Refresh scripts list
      await loadSkillScripts(skillName);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete script');
    }
  };

  const handleClearAllScripts = async (skillName: string, scriptCount: number) => {
    if (!confirm(`Delete all ${scriptCount} scripts from "${skillName}"? This cannot be undone.`)) return;

    try {
      setError('');
      await scriptsApi.deleteAll(skillName);
      // Refresh scripts list
      await loadSkillScripts(skillName);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to clear scripts');
    }
  };

  return (
    <div>
      <PageHeader
        title="Skills"
        description="Manage your AI skills and their tool assignments"
        action={
          !isCreating && !editingSkill && (
            <div className="flex gap-2">
              <button onClick={handleSync} disabled={syncing} className="btn btn-secondary">
                <RefreshCw className={`w-4 h-4 ${syncing ? 'animate-spin' : ''}`} />
                {syncing ? 'Syncing...' : 'Sync from Filesystem'}
              </button>
              <button onClick={handleCreate} className="btn btn-primary">
                <Plus className="w-4 h-4" />
                New Skill
              </button>
            </div>
          )
        }
      />

      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message={success} />}

      {loading ? (
        <LoadingSpinner />
      ) : (
        <>
          {/* Create/Edit Form */}
          {(isCreating || editingSkill) && (
            <div className="card mb-8 animate-fade-in">
              <h3 className="text-lg font-semibold text-gray-900 dark:text-white mb-6">
                {isCreating ? 'Create Skill' : 'Edit Skill'}
              </h3>

              <div className="space-y-6">
                {/* Skill Name */}
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                    Skill Name <span className="text-red-500">*</span>
                  </label>
                  <input
                    type="text"
                    className="input-field"
                    placeholder="e.g., zabbix-analyst"
                    value={formData.name}
                    onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                    disabled={!!editingSkill}
                  />
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    Must be in kebab-case (lowercase with hyphens, e.g., "my-skill")
                  </p>
                </div>

                {/* Description */}
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                    Description <span className="text-red-500">*</span>
                  </label>
                  <textarea
                    className="input-field"
                    rows={3}
                    placeholder="Short description of what this skill does (shown in AGENTS.md)"
                    value={formData.description}
                    onChange={(e) => setFormData({ ...formData, description: e.target.value })}
                  />
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    This will be shown to the incident manager in the available agents table.
                  </p>
                </div>

                {/* Category */}
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                    Category
                  </label>
                  <input
                    type="text"
                    className="input-field"
                    placeholder="e.g., monitoring, database, kubernetes"
                    value={formData.category}
                    onChange={(e) => setFormData({ ...formData, category: e.target.value })}
                  />
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    Optional category for organizing skills
                  </p>
                </div>

                {/* Prompt */}
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                    Prompt (SKILL.md body) <span className="text-red-500">*</span>
                  </label>
                  <PromptEditor
                    value={formData.prompt}
                    onChange={(value) => setFormData({ ...formData, prompt: value })}
                    placeholder="Define the skill's role, expertise, and behavior..."
                    rows={10}
                  />
                </div>

                {/* Enabled Toggle */}
                <div className="flex items-center gap-3 p-4 rounded-lg bg-gray-50 dark:bg-gray-900/50">
                  <input
                    type="checkbox"
                    id="enabled"
                    checked={formData.enabled}
                    onChange={(e) => setFormData({ ...formData, enabled: e.target.checked })}
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

                {/* Tool Assignments */}
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
                    Available Tools (symlinked to scripts/)
                  </label>
                  <div className="border border-gray-200 dark:border-gray-700 rounded-lg p-4 max-h-64 overflow-y-auto bg-gray-50 dark:bg-gray-900/50">
                    {toolInstances.length === 0 ? (
                      <div className="text-center py-8">
                        <Wrench className="w-8 h-8 mx-auto text-gray-400 mb-2" />
                        <p className="text-gray-500 dark:text-gray-400 text-sm">No tool instances available.</p>
                        <p className="text-gray-400 dark:text-gray-500 text-xs mt-1">Create some in the Tools page first.</p>
                      </div>
                    ) : (
                      <div className="space-y-2">
                        {toolInstances.filter(tool => tool.enabled).map((tool) => (
                          <div
                            key={tool.id}
                            className={`flex items-start gap-3 p-3 rounded-lg border transition-all cursor-pointer ${
                              selectedToolIds.includes(tool.id)
                                ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
                                : 'border-gray-200 dark:border-gray-700 hover:border-gray-300 dark:hover:border-gray-600'
                            }`}
                            onClick={() => toggleToolSelection(tool.id)}
                          >
                            <input
                              type="checkbox"
                              checked={selectedToolIds.includes(tool.id)}
                              onChange={() => toggleToolSelection(tool.id)}
                              onClick={(e) => e.stopPropagation()}
                            />
                            <div className="flex-1">
                              <div className="font-medium text-gray-900 dark:text-white text-sm">
                                {tool.name}
                              </div>
                              <div className="text-xs text-gray-500 dark:text-gray-400 mt-1">
                                Type: {tool.tool_type?.name} - {tool.tool_type?.description}
                              </div>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    Selected tools are symlinked to the skill's scripts/ folder and documented in references/tools.md
                  </p>
                </div>

                {/* Form Actions */}
                <div className="flex gap-3 pt-4 border-t border-gray-200 dark:border-gray-700">
                  <button onClick={handleSave} className="btn btn-primary">
                    <Save className="w-4 h-4" />
                    Save
                  </button>
                  <button onClick={handleCancel} className="btn btn-secondary">
                    <X className="w-4 h-4" />
                    Cancel
                  </button>
                </div>
              </div>
            </div>
          )}

          {/* Skills List */}
          <div className="card">
            {skills.length === 0 ? (
              <div className="py-16 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
                <Bot className="w-12 h-12 mx-auto text-gray-400 mb-3" />
                <p className="text-gray-500 dark:text-gray-400">No skills yet</p>
                <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">Create one or sync from filesystem</p>
              </div>
            ) : (
              <div className="space-y-4">
                {skills.map((skill) => (
                  <div
                    key={skill.id}
                    className={`border rounded-lg p-6 transition-all ${
                      skill.enabled
                        ? 'border-gray-200 dark:border-gray-700 hover:border-gray-300 dark:hover:border-gray-600'
                        : 'border-gray-100 dark:border-gray-800 opacity-60'
                    }`}
                  >
                    <div className="flex items-start justify-between">
                      <div className="flex-1 min-w-0">
                        {/* Skill Header */}
                        <div className="flex items-center gap-3 mb-2">
                          <h3 className="font-semibold text-gray-900 dark:text-white font-mono">
                            ${skill.name}
                          </h3>
                          {skill.category && (
                            <span className="badge badge-info">
                              {skill.category}
                            </span>
                          )}
                          <span className={`badge ${skill.enabled ? 'badge-success' : 'badge-default'}`}>
                            {skill.enabled ? 'Enabled' : 'Disabled'}
                          </span>
                        </div>

                        {/* Description */}
                        {skill.description && (
                          <p className="text-gray-600 dark:text-gray-400 text-sm mb-3">
                            {skill.description}
                          </p>
                        )}

                        {/* Prompt Preview */}
                        <div className="bg-gray-50 dark:bg-gray-900/50 rounded-lg p-3 mb-3 max-h-32 overflow-y-auto">
                          <p className="text-gray-700 dark:text-gray-300 text-sm font-mono whitespace-pre-wrap">
                            {skill.prompt}
                          </p>
                        </div>

                        {/* Assigned Tools */}
                        {skill.tools && skill.tools.length > 0 && (
                          <div className="flex flex-wrap items-center gap-2 mb-3">
                            <span className="text-xs font-medium text-gray-500 dark:text-gray-400">Tools:</span>
                            {skill.tools.map((tool) => (
                              <span key={tool.id} className="badge badge-warning">
                                <Wrench className="w-3 h-3" />
                                {tool.name}
                              </span>
                            ))}
                          </div>
                        )}

                        {/* Metadata */}
                        <p className="text-xs text-gray-400 dark:text-gray-500">
                          Created: {new Date(skill.created_at).toLocaleString()}
                        </p>

                        {/* Scripts Section */}
                        <div className="mt-4 pt-4 border-t border-gray-200 dark:border-gray-700">
                          <button
                            onClick={() => toggleScriptsExpanded(skill.name)}
                            className="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300 hover:text-gray-900 dark:hover:text-white transition-colors"
                          >
                            {expandedScripts.has(skill.name) ? (
                              <ChevronDown className="w-4 h-4" />
                            ) : (
                              <ChevronRight className="w-4 h-4" />
                            )}
                            <FileCode className="w-4 h-4" />
                            <span>Scripts</span>
                            {skillScripts[skill.name] && (
                              <span className="px-1.5 py-0.5 text-xs bg-gray-100 dark:bg-gray-700 rounded-full">
                                {skillScripts[skill.name].length}
                              </span>
                            )}
                          </button>

                          {expandedScripts.has(skill.name) && (
                            <div className="mt-3">
                              {scriptsLoading.has(skill.name) ? (
                                <div className="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400 py-2">
                                  <Loader2 className="w-4 h-4 animate-spin" />
                                  Loading scripts...
                                </div>
                              ) : skillScripts[skill.name]?.length === 0 ? (
                                <p className="text-sm text-gray-500 dark:text-gray-400 py-2">
                                  No scripts stored for this skill
                                </p>
                              ) : (
                                <>
                                  {/* Clear All button */}
                                  {skillScripts[skill.name]?.length > 0 && (
                                    <div className="flex justify-end mb-2">
                                      <button
                                        onClick={() => handleClearAllScripts(skill.name, skillScripts[skill.name].length)}
                                        className="text-xs text-red-600 dark:text-red-400 hover:text-red-700 dark:hover:text-red-300 transition-colors"
                                      >
                                        Clear All
                                      </button>
                                    </div>
                                  )}

                                  {/* Scripts list */}
                                  <div className="space-y-1 max-h-48 overflow-y-auto">
                                    {skillScripts[skill.name]?.map((filename) => (
                                      <div
                                        key={filename}
                                        className="flex items-center justify-between py-1.5 px-2 bg-gray-50 dark:bg-gray-900/50 rounded group hover:bg-gray-100 dark:hover:bg-gray-800/50"
                                      >
                                        <span className="font-mono text-xs text-gray-700 dark:text-gray-300 truncate">
                                          {filename}
                                        </span>
                                        <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                                          <button
                                            onClick={() => handleViewScript(skill.name, filename)}
                                            className="p-1 text-gray-500 hover:text-primary-600 dark:hover:text-primary-400"
                                            title="View/Edit"
                                          >
                                            <Eye className="w-3.5 h-3.5" />
                                          </button>
                                          <button
                                            onClick={() => handleDeleteScript(skill.name, filename)}
                                            className="p-1 text-gray-500 hover:text-red-600 dark:hover:text-red-400"
                                            title="Delete"
                                          >
                                            <Trash2 className="w-3.5 h-3.5" />
                                          </button>
                                        </div>
                                      </div>
                                    ))}
                                  </div>
                                </>
                              )}
                            </div>
                          )}
                        </div>
                      </div>

                      {/* Actions */}
                      <div className="flex gap-2 ml-4 flex-shrink-0">
                        <button
                          onClick={() => handleEdit(skill)}
                          className="btn btn-ghost p-2 text-primary-600 dark:text-primary-400 hover:bg-primary-50 dark:hover:bg-primary-900/20"
                          title="Edit"
                        >
                          <Edit2 className="w-4 h-4" />
                        </button>
                        <button
                          onClick={() => handleDelete(skill.name)}
                          className="btn btn-ghost p-2 text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20"
                          title="Delete"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </>
      )}

      {/* Script Viewer Modal */}
      <ScriptViewerModal
        isOpen={scriptModalOpen}
        onClose={() => {
          setScriptModalOpen(false);
          setSelectedScript(null);
          setCurrentSkillName(null);
        }}
        scriptInfo={selectedScript}
        loading={scriptLoading}
        onSave={handleSaveScript}
      />
    </div>
  );
}
