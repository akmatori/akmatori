import { useState } from 'react';
import { X, Plus, RefreshCw } from 'lucide-react';
import type { Incident } from '../../types';
import { incidentsApi } from '../../api/client';
import { getErrorMessage } from '../../utils/errors';

interface CreateIncidentModalProps {
  onClose: () => void;
  // Called with the fully loaded incident once the API has created it.
  onCreated: (incident: Incident) => void;
  // Called with a message to show on the page when creation fails.
  onError: (message: string) => void;
}

// CreateIncidentModal collects a free-text task and starts an investigation.
export default function CreateIncidentModal({ onClose, onCreated, onError }: CreateIncidentModalProps) {
  const [task, setTask] = useState('');
  const [creating, setCreating] = useState(false);

  const handleCreate = async () => {
    const trimmed = task.trim();
    if (!trimmed) return;

    try {
      setCreating(true);
      const response = await incidentsApi.create({ task: trimmed });
      // Fetch the full incident so the list can show it immediately.
      const incident = await incidentsApi.get(response.uuid);
      onCreated(incident);
    } catch (err) {
      onError(getErrorMessage(err, 'Failed to create incident'));
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
      <div className="bg-surface rounded-xl shadow-2xl max-w-2xl w-full animate-fade-in">
        {/* Modal Header */}
        <div className="flex items-center justify-between p-6 border-b border-line">
          <div>
            <h2 className="text-xl font-semibold text-fg">Create incident</h2>
            <p className="mt-1 text-sm text-fg-subtle">
              Start an incident investigation by providing a task description
            </p>
          </div>
          <button onClick={onClose} className="btn btn-ghost p-2" title="Close">
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Modal Body */}
        <div className="p-6">
          <label className="block text-sm font-medium text-fg mb-2">
            Task Description <span className="text-red-500">*</span>
          </label>
          <textarea
            value={task}
            onChange={(e) => setTask(e.target.value)}
            placeholder="Describe the task or investigation you want to perform..."
            className="input-field min-h-[180px] resize-y"
            autoFocus
          />
          <p className="mt-2 text-xs text-fg-subtle">
            The incident manager will analyze this task and coordinate with skills to resolve it.
          </p>
        </div>

        {/* Modal Footer */}
        <div className="flex items-center justify-end gap-3 p-6 border-t border-line bg-surface-2">
          <button onClick={onClose} className="btn btn-secondary" disabled={creating}>
            Cancel
          </button>
          <button
            onClick={handleCreate}
            className="btn btn-primary"
            disabled={creating || !task.trim()}
          >
            {creating ? (
              <>
                <RefreshCw className="w-4 h-4 animate-spin" />
                Creating...
              </>
            ) : (
              <>
                <Plus className="w-4 h-4" />
                Create
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}
