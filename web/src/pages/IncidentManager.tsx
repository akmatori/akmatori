import { useEffect, useState } from 'react';
import { Save, RefreshCw, Info, Terminal, Folder, FileCode, Lightbulb, ChevronRight } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage } from '../components/ErrorMessage';
import { incidentManagerApi } from '../api/client';

export default function IncidentManager() {
  const [prompt, setPrompt] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState(false);

  useEffect(() => {
    loadConfig();
  }, []);

  const loadConfig = async () => {
    try {
      setLoading(true);
      setError('');
      const config = await incidentManagerApi.getConfig();
      setPrompt(config.prompt);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load configuration');
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    try {
      setSaving(true);
      setError('');
      setSuccess(false);

      await incidentManagerApi.updatePrompt(prompt);

      setSuccess(true);
      setTimeout(() => setSuccess(false), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save configuration');
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <LoadingSpinner />;

  return (
    <div>
      <PageHeader
        title="Incident Manager"
        description="Configure the static prompt for the incident manager"
      />

      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message="Configuration saved successfully!" />}

      {/* Main Configuration Card */}
      <div className="card mb-8">
        <div className="space-y-6">
          {/* Prompt Editor */}
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              Incident Manager Prompt <span className="text-red-500">*</span>
            </label>
            <p className="text-sm text-gray-500 dark:text-gray-400 mb-4">
              This prompt defines the behavior and role of the incident manager. It will be
              combined with dynamic information about available skills.
            </p>
            <textarea
              className="input-field font-mono text-sm"
              rows={20}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder="Enter the incident manager prompt..."
            />
          </div>

          {/* Guidelines */}
          <div className="p-4 rounded-lg bg-primary-50 dark:bg-primary-900/20 border border-primary-200 dark:border-primary-800">
            <div className="flex items-center gap-2 mb-3">
              <Info className="w-4 h-4 text-primary-600 dark:text-primary-400" />
              <h4 className="font-medium text-primary-900 dark:text-primary-100">Prompt Guidelines</h4>
            </div>
            <ul className="space-y-2 text-sm text-primary-800 dark:text-primary-200">
              <li className="flex items-start gap-2">
                <ChevronRight className="w-4 h-4 text-primary-500 mt-0.5 flex-shrink-0" />
                <span>Define the incident manager's role and responsibilities clearly</span>
              </li>
              <li className="flex items-start gap-2">
                <ChevronRight className="w-4 h-4 text-primary-500 mt-0.5 flex-shrink-0" />
                <span>Specify how it should handle different types of incidents</span>
              </li>
              <li className="flex items-start gap-2">
                <ChevronRight className="w-4 h-4 text-primary-500 mt-0.5 flex-shrink-0" />
                <span>Explain when and how to invoke skills</span>
              </li>
              <li className="flex items-start gap-2">
                <ChevronRight className="w-4 h-4 text-primary-500 mt-0.5 flex-shrink-0" />
                <span>Include any standard operating procedures</span>
              </li>
              <li className="flex items-start gap-2">
                <ChevronRight className="w-4 h-4 text-primary-500 mt-0.5 flex-shrink-0" />
                <span>Keep the tone professional and action-oriented</span>
              </li>
            </ul>
          </div>

          {/* Actions */}
          <div className="flex gap-3 pt-4 border-t border-gray-200 dark:border-gray-700">
            <button onClick={handleSave} className="btn btn-primary" disabled={saving}>
              <Save className="w-4 h-4" />
              {saving ? 'Saving...' : 'Save Configuration'}
            </button>
            <button onClick={loadConfig} className="btn btn-secondary" disabled={saving}>
              <RefreshCw className="w-4 h-4" />
              Reset
            </button>
          </div>
        </div>
      </div>

      {/* How It Works */}
      <div className="card">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-white mb-6">How It Works</h3>

        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {/* Step 1 */}
          <div className="p-4 rounded-lg border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
            <div className="flex items-center gap-3 mb-3">
              <div className="w-8 h-8 rounded-full bg-primary-100 dark:bg-primary-900/50 flex items-center justify-center text-primary-600 dark:text-primary-400 font-semibold text-sm">
                1
              </div>
              <span className="font-medium text-gray-900 dark:text-white">Event Received</span>
            </div>
            <p className="text-sm text-gray-600 dark:text-gray-400">
              When a Slack message or Zabbix alert arrives, the system spawns an incident manager.
            </p>
          </div>

          {/* Step 2 */}
          <div className="p-4 rounded-lg border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
            <div className="flex items-center gap-3 mb-3">
              <div className="w-8 h-8 rounded-full bg-primary-100 dark:bg-primary-900/50 flex items-center justify-center text-primary-600 dark:text-primary-400 font-semibold text-sm">
                2
              </div>
              <span className="font-medium text-gray-900 dark:text-white">Folder Creation</span>
            </div>
            <p className="text-sm text-gray-600 dark:text-gray-400">
              A unique folder is created at:
            </p>
            <code className="mt-2 block px-2 py-1 bg-gray-100 dark:bg-gray-800 rounded text-xs text-primary-600 dark:text-primary-400 break-all">
              /akmatori/incidents/{'<UUID>'}/
            </code>
          </div>

          {/* Step 3 */}
          <div className="p-4 rounded-lg border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
            <div className="flex items-center gap-3 mb-3">
              <div className="w-8 h-8 rounded-full bg-primary-100 dark:bg-primary-900/50 flex items-center justify-center text-primary-600 dark:text-primary-400 font-semibold text-sm">
                3
              </div>
              <span className="font-medium text-gray-900 dark:text-white">AGENTS.md Generated</span>
            </div>
            <p className="text-sm text-gray-600 dark:text-gray-400">
              The static prompt (configured here) is combined with dynamic information about available skills.
            </p>
          </div>

          {/* Step 4 */}
          <div className="p-4 rounded-lg border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
            <div className="flex items-center gap-3 mb-3">
              <div className="w-8 h-8 rounded-full bg-primary-100 dark:bg-primary-900/50 flex items-center justify-center text-primary-600 dark:text-primary-400 font-semibold text-sm">
                4
              </div>
              <span className="font-medium text-gray-900 dark:text-white">Skills Available</span>
            </div>
            <p className="text-sm text-gray-600 dark:text-gray-400">
              Skills are linked via symlink:
            </p>
            <code className="mt-2 block px-2 py-1 bg-gray-100 dark:bg-gray-800 rounded text-xs text-primary-600 dark:text-primary-400">
              .codex/skills/
            </code>
          </div>

          {/* Step 5 */}
          <div className="p-4 rounded-lg border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
            <div className="flex items-center gap-3 mb-3">
              <div className="w-8 h-8 rounded-full bg-primary-100 dark:bg-primary-900/50 flex items-center justify-center text-primary-600 dark:text-primary-400 font-semibold text-sm">
                5
              </div>
              <span className="font-medium text-gray-900 dark:text-white">Execution</span>
            </div>
            <p className="text-sm text-gray-600 dark:text-gray-400">
              The incident manager can then invoke skills using native Codex skill invocation.
            </p>
          </div>

          {/* Visual Legend */}
          <div className="p-4 rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800">
            <div className="flex items-center gap-2 mb-3">
              <Lightbulb className="w-4 h-4 text-amber-500" />
              <span className="font-medium text-gray-900 dark:text-white">Key Files</span>
            </div>
            <div className="space-y-2 text-xs">
              <div className="flex items-center gap-2 text-gray-600 dark:text-gray-400">
                <Folder className="w-3 h-3 text-amber-500" />
                <span>/akmatori/incidents/</span>
              </div>
              <div className="flex items-center gap-2 text-gray-600 dark:text-gray-400 ml-4">
                <FileCode className="w-3 h-3 text-primary-500" />
                <span>AGENTS.md</span>
              </div>
              <div className="flex items-center gap-2 text-gray-600 dark:text-gray-400 ml-4">
                <Terminal className="w-3 h-3 text-green-500" />
                <span>.codex/skills/ (symlink)</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
