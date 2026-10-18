import { Loader2 } from 'lucide-react';
import { JsonView, darkStyles } from 'react-json-view-lite';
import 'react-json-view-lite/dist/index.css';

// RawState is the per-event cache entry for a lazily fetched raw payload.
export interface RawState {
  loading: boolean;
  raw?: unknown;
  original_message?: string;
  error?: string;
}

const jsonViewerStyles = {
  ...darkStyles,
  container: 'bg-transparent font-mono text-xs',
  basicChildStyle: 'pl-4',
  label: 'text-purple-400 mr-1',
  nullValue: 'text-gray-500',
  undefinedValue: 'text-gray-500',
  stringValue: 'text-green-400',
  booleanValue: 'text-red-400',
  numberValue: 'text-orange-400',
  otherValue: 'text-gray-300',
  punctuation: 'text-gray-500',
  expandIcon: 'text-gray-400 cursor-pointer select-none',
  collapseIcon: 'text-gray-400 cursor-pointer select-none',
};

// RawEventContent renders an event's raw payload / original message on demand.
export default function RawEventContent({ state }: { state?: RawState }) {
  if (!state || state.loading) {
    return (
      <div className="flex items-center gap-2 text-gray-400 text-xs">
        <Loader2 className="w-3.5 h-3.5 animate-spin" /> Loading raw message…
      </div>
    );
  }
  if (state.error) {
    return <p className="text-red-400 text-xs">{state.error}</p>;
  }
  if (state.original_message) {
    return (
      <pre className="whitespace-pre-wrap text-gray-300 font-mono text-xs max-h-80 overflow-y-auto">
        {state.original_message}
      </pre>
    );
  }
  if (state.raw !== undefined && state.raw !== null) {
    if (typeof state.raw === 'object') {
      return (
        <div className="max-h-80 overflow-y-auto">
          <JsonView data={state.raw as object} style={jsonViewerStyles} shouldExpandNode={() => true} />
        </div>
      );
    }
    return <pre className="whitespace-pre-wrap text-gray-300 font-mono text-xs">{String(state.raw)}</pre>;
  }
  return <p className="text-gray-500 text-xs">No raw message available for this event</p>;
}
