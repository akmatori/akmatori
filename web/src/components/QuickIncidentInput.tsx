import { useState, useRef, useEffect } from 'react';
import { ArrowRight, Loader2 } from 'lucide-react';
import { incidentsApi } from '../api/client';

interface QuickIncidentInputProps {
  onSuccess?: (uuid: string) => void;
  onError?: (error: string) => void;
}

export default function QuickIncidentInput({ onSuccess, onError }: QuickIncidentInputProps) {
  const [task, setTask] = useState('');
  const [creating, setCreating] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus input on mount
  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!task.trim() || creating) return;

    try {
      setCreating(true);
      const response = await incidentsApi.create({ task: task.trim() });
      setTask('');
      onSuccess?.(response.uuid);
    } catch (err) {
      onError?.(err instanceof Error ? err.message : 'Failed to create incident');
    } finally {
      setCreating(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      handleSubmit(e);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="w-full">
      <div className="relative">
        <input
          ref={inputRef}
          type="text"
          value={task}
          onChange={(e) => setTask(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="What do you want to investigate?"
          disabled={creating}
          className="w-full h-16 px-6 pr-16 text-lg bg-white dark:bg-gray-800 border-2 border-gray-200 dark:border-gray-700 rounded-2xl shadow-lg focus:outline-none focus:border-primary-500 focus:ring-4 focus:ring-primary-500/20 dark:focus:ring-primary-500/30 transition-all duration-200 placeholder:text-gray-400 dark:placeholder:text-gray-500 text-gray-900 dark:text-white disabled:opacity-60"
        />
        <button
          type="submit"
          disabled={!task.trim() || creating}
          className="absolute right-3 top-1/2 -translate-y-1/2 w-10 h-10 flex items-center justify-center rounded-xl bg-primary-600 hover:bg-primary-700 disabled:bg-gray-300 dark:disabled:bg-gray-700 disabled:cursor-not-allowed text-white transition-colors duration-200"
          aria-label="Create incident"
        >
          {creating ? (
            <Loader2 className="w-5 h-5 animate-spin" />
          ) : (
            <ArrowRight className="w-5 h-5" />
          )}
        </button>
      </div>
      <p className="mt-3 text-center text-sm text-gray-500 dark:text-gray-400">
        Press Enter to start an investigation
      </p>
    </form>
  );
}
