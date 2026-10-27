import { Loader2 } from 'lucide-react';

// ResponseState is the per-incident cache entry for a lazily fetched response.
export interface ResponseState {
  loading: boolean;
  title?: string;
  status?: string;
  response?: string;
  error?: string;
}

// IncidentResponseContent renders the linked incident's final response on demand.
export default function IncidentResponseContent({ state }: { state?: ResponseState }) {
  if (!state || state.loading) {
    return (
      <div className="flex items-center gap-2 text-fg-subtle text-xs">
        <Loader2 className="w-3.5 h-3.5 animate-spin" /> Loading incident response…
      </div>
    );
  }
  if (state.error) {
    return <p className="text-state-fail text-xs">{state.error}</p>;
  }
  if (state.response) {
    return (
      <pre className="whitespace-pre-wrap text-fg font-sans text-xs max-h-80 overflow-y-auto">
        {state.response}
      </pre>
    );
  }
  return (
    <p className="text-fg-subtle text-xs">
      {state.status === 'running' || state.status === 'pending'
        ? 'Response will be available when the investigation completes…'
        : 'No response available for this incident'}
    </p>
  );
}
