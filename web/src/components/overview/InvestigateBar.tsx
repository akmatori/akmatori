import { useState } from 'react';
import { Loader2 } from 'lucide-react';
import { incidentsApi } from '../../api/client';
import { getErrorMessage } from '../../utils/errors';

interface InvestigateBarProps {
  onStarted: (uuid: string) => void;
  onError: (message: string) => void;
}

// InvestigateBar starts a manual investigation from a free-text problem
// description.
export default function InvestigateBar({ onStarted, onError }: InvestigateBarProps) {
  const [task, setTask] = useState('');
  const [creating, setCreating] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!task.trim() || creating) return;
    try {
      setCreating(true);
      const response = await incidentsApi.create({ task: task.trim() });
      setTask('');
      onStarted(response.uuid);
    } catch (err) {
      onError(getErrorMessage(err, 'Failed to start the investigation'));
    } finally {
      setCreating(false);
    }
  };

  return (
    <form
      onSubmit={handleSubmit}
      className="flex flex-wrap gap-2.5 p-3.5 rounded-xl border border-line bg-surface"
    >
      <label htmlFor="investigate-task" className="sr-only">Describe a problem</label>
      <input
        id="investigate-task"
        type="text"
        value={task}
        onChange={(e) => setTask(e.target.value)}
        disabled={creating}
        placeholder="Describe a problem and the agent investigates it. Example: 5xx errors doubled on edge since 13:40"
        className="input-field flex-[1_1_420px] min-w-0 h-11"
      />
      <button
        type="submit"
        disabled={!task.trim() || creating}
        className="btn btn-primary h-11 px-5 inline-flex items-center gap-2"
      >
        {creating && <Loader2 size={16} className="animate-spin" aria-hidden="true" />}
        Investigate
      </button>
    </form>
  );
}
