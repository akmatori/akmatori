import { Link } from 'react-router-dom';
import { Brain, Loader2, RotateCw } from 'lucide-react';
import type { ContextFile } from '../types';
import { memoryHref } from './memory/memoryHelpers';

interface ContextFileSummaryProps {
  file: ContextFile;
  onSummarize: (file: ContextFile) => void;
}

// ContextFileSummary shows the state of a file's summary memory: a link to
// the memory, "Summarizing…", or why there is none, with a button to run
// the job again.
export default function ContextFileSummary({ file, onSummarize }: ContextFileSummaryProps) {
  const status = file.summary_status ?? '';
  const rerun = (label: string) => (
    <button
      type="button"
      onClick={() => onSummarize(file)}
      className="inline-flex items-center gap-1 text-primary-600 hover:underline dark:text-primary-400"
    >
      <RotateCw size={12} />
      {label}
    </button>
  );

  if (status === 'pending') {
    return (
      <span className="inline-flex items-center gap-1 text-fg-subtle">
        <Loader2 size={12} className="animate-spin" />
        Summarizing into memory…
      </span>
    );
  }
  if (file.summary_memory_id) {
    return (
      <span className="inline-flex items-center gap-2">
        <Link to={memoryHref(file.summary_memory_id)} className="inline-flex items-center gap-1 text-primary-600 hover:underline dark:text-primary-400">
          <Brain size={12} />
          Summary in memory
        </Link>
        {rerun('Update')}
      </span>
    );
  }
  if (status === 'failed' || status === 'skipped') {
    return (
      <span className="inline-flex flex-wrap items-center gap-2">
        <span className={status === 'failed' ? 'text-state-fail' : 'text-fg-subtle'}>
          {status === 'failed' ? 'Summary failed' : 'No summary'}
          {file.summary_error ? `: ${file.summary_error}` : ''}
        </span>
        {status === 'failed' && rerun('Try again')}
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-2 text-fg-subtle">
      No summary in memory
      {rerun('Summarize')}
    </span>
  );
}
