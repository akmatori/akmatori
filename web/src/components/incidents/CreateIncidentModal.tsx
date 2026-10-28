import { useState } from 'react';
import { X, Plus, RefreshCw } from 'lucide-react';
import type { CreateIncidentResponse } from '../../types';
import { incidentsApi } from '../../api/client';
import { getErrorMessage } from '../../utils/errors';

interface CreateIncidentModalProps {
  onClose: () => void;
  // Called with the API result: an incident (a reported problem), request
  // work (a request) or a stored change/info event.
  onCreated: (result: CreateIncidentResponse) => void;
  // Called with a message to show on the page when creation fails.
  onError: (message: string) => void;
}

// CreateIncidentModal collects a free-text task. The API classifies it: a
// reported problem opens or joins an incident, a request starts an agent
// run that is not an incident, a change or info is only stored.
export default function CreateIncidentModal({ onClose, onCreated, onError }: CreateIncidentModalProps) {
  const [task, setTask] = useState('');
  const [creating, setCreating] = useState(false);

  const handleCreate = async () => {
    const trimmed = task.trim();
    if (!trimmed) return;

    try {
      setCreating(true);
      onCreated(await incidentsApi.create({ task: trimmed }));
    } catch (err) {
      onError(getErrorMessage(err, 'Failed to start the task'));
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
            <h2 className="text-xl font-semibold text-fg">Report a problem or ask the agent</h2>
            <p className="mt-1 text-sm text-fg-subtle">
              A reported problem opens an incident or joins the open one. A question starts an agent run.
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
            Akmatori classifies the text first. A deploy or change notice is only stored as an event.
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
                Starting...
              </>
            ) : (
              <>
                <Plus className="w-4 h-4" />
                Start
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}
