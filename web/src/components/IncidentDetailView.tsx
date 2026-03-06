import { useState, useRef, useEffect, useMemo } from 'react';
import { Terminal, MessageSquare, FileCode, ChevronDown, ChevronRight, RefreshCw } from 'lucide-react';
import { JsonView, darkStyles } from 'react-json-view-lite';
import 'react-json-view-lite/dist/index.css';
import { IncidentAlertsPanel } from './IncidentAlertsPanel';
import type { Incident } from '../types';

type TabType = 'reasoning' | 'response' | 'raw';

const jsonViewerStyles = {
  ...darkStyles,
  container: 'bg-transparent font-mono text-sm',
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

function RawPayloadViewer({ payload }: { payload: unknown }) {
  if (payload === null || payload === undefined) {
    return (
      <p className="text-gray-500 text-center py-8">
        No raw payload available for this incident
      </p>
    );
  }
  if (typeof payload === 'object') {
    return (
      <JsonView
        data={payload}
        style={jsonViewerStyles}
        shouldExpandNode={() => true}
      />
    );
  }
  return (
    <pre className="whitespace-pre-wrap text-gray-300 font-mono text-sm">
      {String(payload)}
    </pre>
  );
}

interface IncidentDetailViewProps {
  incident: Incident;
  autoRefresh?: boolean;
}

export default function IncidentDetailView({ incident, autoRefresh = false }: IncidentDetailViewProps) {
  const [activeTab, setActiveTab] = useState<TabType>('reasoning');
  const [showToolCalls, setShowToolCalls] = useState(false);
  const logContainerRef = useRef<HTMLDivElement | null>(null);

  // Auto-scroll to bottom when log updates
  useEffect(() => {
    if (logContainerRef.current && activeTab === 'reasoning') {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
    }
  }, [incident.full_log, activeTab]);

  const parsedLog = useMemo(() => {
    if (!incident.full_log) return null;

    const lines = incident.full_log.split('\n');
    const entries: Array<{
      type: 'regular' | 'tool_call';
      content: string;
      output?: string;
      isMultiline?: boolean;
    }> = [];

    let inToolCall = false;
    let inOutput = false;
    let heredocDelimiter: string | null = null;
    let toolCallLines: string[] = [];
    let outputLines: string[] = [];

    const flushToolCall = () => {
      if (toolCallLines.length > 0) {
        entries.push({
          type: 'tool_call',
          content: toolCallLines.join('\n'),
          output: outputLines.length > 0 ? outputLines.join('\n') : undefined,
          isMultiline: toolCallLines.length > 1,
        });
        toolCallLines = [];
        outputLines = [];
      }
      inToolCall = false;
      inOutput = false;
      heredocDelimiter = null;
    };

    const isNewSection = (line: string) =>
      line.startsWith('✅ Ran:') ||
      line.startsWith('❌ Failed:') ||
      line.startsWith('🤔 ') ||
      line.startsWith('📝 ') ||
      line.startsWith('🛠️ Running:') ||
      line.startsWith('--- Final Response ---') ||
      line.startsWith('--- ');

    for (const line of lines) {
      if (inToolCall) {
        if (isNewSection(line)) {
          flushToolCall();
        } else if (inOutput) {
          outputLines.push(line);
          continue;
        } else if (line === 'Output:') {
          inOutput = true;
          continue;
        } else if (heredocDelimiter) {
          toolCallLines.push(line);
          if (line.startsWith(heredocDelimiter) || line.match(new RegExp(`^${heredocDelimiter}["']?\\)?$`))) {
            heredocDelimiter = null;
          }
          continue;
        } else {
          toolCallLines.push(line);
          continue;
        }
      }

      if (line.startsWith('✅ Ran:') || line.startsWith('❌ Failed:')) {
        const heredocMatch = line.match(/<<[-'"\\]*(\w+)/);
        if (heredocMatch) {
          heredocDelimiter = heredocMatch[1];
        }
        inToolCall = true;
        inOutput = false;
        toolCallLines = [line];
        outputLines = [];
      } else {
        entries.push({ type: 'regular', content: line });
      }
    }

    flushToolCall();

    // Group consecutive Running lines
    const grouped: typeof entries = [];
    let i = 0;
    while (i < entries.length) {
      const entry = entries[i];
      if (entry.type === 'regular' && entry.content.startsWith('🛠️ Running:')) {
        const batch: string[] = [];
        let j = i;
        while (j < entries.length) {
          const e = entries[j];
          if (e.type === 'regular' && e.content.startsWith('🛠️ Running:')) {
            batch.push(e.content.replace('🛠️ Running:', '').trim());
            j++;
          } else if (e.type === 'regular' && e.content.trim() === '') {
            j++;
          } else {
            break;
          }
        }
        if (batch.length > 1) {
          const counts = new Map<string, number>();
          for (const name of batch) counts.set(name, (counts.get(name) || 0) + 1);
          const parts = Array.from(counts.entries()).map(([name, count]) => `${count}× ${name}`);
          grouped.push({ type: 'regular', content: `🛠️ Running: ${batch.length} tools (${parts.join(', ')})` });
        } else {
          grouped.push(entry);
        }
        i = j;
      } else {
        grouped.push(entry);
        i++;
      }
    }

    const toolCallCount = grouped.filter(e => e.type === 'tool_call').length;
    return { entries: grouped, toolCallCount };
  }, [incident.full_log]);

  return (
    <div>
      {/* Tab Navigation */}
      <div className="flex border-b border-gray-200 dark:border-gray-700 px-6">
        <button
          onClick={() => setActiveTab('reasoning')}
          className={`px-4 py-3 text-sm font-medium border-b-2 transition-colors ${
            activeTab === 'reasoning'
              ? 'border-primary-500 text-primary-600 dark:text-primary-400'
              : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
          }`}
          disabled={incident.status === 'pending'}
        >
          <span className="flex items-center gap-2">
            <Terminal className="w-4 h-4" />
            Reasoning
          </span>
        </button>
        <button
          onClick={() => setActiveTab('response')}
          className={`px-4 py-3 text-sm font-medium border-b-2 transition-colors ${
            activeTab === 'response'
              ? 'border-primary-500 text-primary-600 dark:text-primary-400'
              : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
          }`}
          disabled={incident.status === 'pending' || incident.status === 'running'}
        >
          <span className="flex items-center gap-2">
            <MessageSquare className="w-4 h-4" />
            Response
          </span>
        </button>
        <button
          onClick={() => setActiveTab('raw')}
          className={`px-4 py-3 text-sm font-medium border-b-2 transition-colors ${
            activeTab === 'raw'
              ? 'border-primary-500 text-primary-600 dark:text-primary-400'
              : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
          }`}
        >
          <span className="flex items-center gap-2">
            <FileCode className="w-4 h-4" />
            Raw Alert
          </span>
        </button>
      </div>

      {/* Tab Content */}
      <div ref={logContainerRef} className="flex-1 overflow-y-auto p-6">
        {activeTab === 'reasoning' ? (
          <div className="bg-gray-900 rounded-lg p-6 font-mono text-sm overflow-x-auto text-gray-100 min-h-[200px]">
            <div className="flex items-center gap-2 text-gray-500 mb-4 pb-4 border-b border-gray-700">
              <Terminal className="w-4 h-4" />
              <span className="text-xs font-medium uppercase tracking-wide">Execution Log</span>
              {parsedLog && parsedLog.toolCallCount > 0 && (
                <button
                  onClick={() => setShowToolCalls(!showToolCalls)}
                  className="ml-4 flex items-center gap-1.5 px-2 py-1 rounded text-xs bg-gray-800 hover:bg-gray-700 transition-colors"
                >
                  {showToolCalls ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
                  <span>Tool Calls ({parsedLog.toolCallCount})</span>
                </button>
              )}
              {incident.status === 'running' && autoRefresh && (
                <span className="ml-auto flex items-center gap-2 text-primary-400">
                  <RefreshCw className="w-3 h-3 animate-spin" />
                  <span className="text-xs">Live</span>
                </span>
              )}
            </div>
            {parsedLog ? (
              <div className="whitespace-pre-wrap">
                {parsedLog.entries.map((entry, index) => {
                  if (entry.type === 'tool_call') {
                    if (!showToolCalls) return null;
                    return (
                      <div key={index} className="my-3">
                        <div className="text-gray-300 bg-gray-800/70 px-3 py-2 rounded border-l-2 border-blue-500">
                          {entry.content}
                        </div>
                        {entry.output && (
                          <div className="mt-1 text-gray-400 bg-gray-800/40 px-3 py-2 rounded border-l-2 border-gray-600 text-xs">
                            <div className="text-gray-500 text-[10px] uppercase tracking-wide mb-1">Output:</div>
                            {entry.output}
                          </div>
                        )}
                      </div>
                    );
                  }
                  if (entry.content.match(/^🛠️ Running: \d+ tools/)) {
                    return <div key={index} className="text-blue-400">{entry.content}</div>;
                  }
                  return <div key={index}>{entry.content}</div>;
                })}
              </div>
            ) : (
              incident.status === 'pending'
                ? '> Waiting for execution to start...'
                : '> No log available yet'
            )}
          </div>
        ) : activeTab === 'response' ? (
          <div className="bg-gray-50 dark:bg-gray-900 rounded-lg p-6 min-h-[200px]">
            {incident.response ? (
              <div className="whitespace-pre-wrap text-gray-700 dark:text-gray-300 font-mono text-sm">
                {incident.response
                  .replace(/\[FINAL_RESULT\]\n?/g, '')
                  .replace(/\[\/FINAL_RESULT\]\n?/g, '')
                  .trim()}
              </div>
            ) : (
              <p className="text-gray-500 text-center py-8">
                {incident.status === 'pending' || incident.status === 'running'
                  ? 'Response will be available when the incident completes...'
                  : 'No response available'}
              </p>
            )}
          </div>
        ) : (
          <div className="bg-gray-900 rounded-lg p-6 min-h-[200px] overflow-x-auto">
            <div className="flex items-center gap-2 text-gray-500 mb-4 pb-4 border-b border-gray-700">
              <FileCode className="w-4 h-4" />
              <span className="text-xs font-medium uppercase tracking-wide">Original Webhook Payload</span>
            </div>
            <RawPayloadViewer payload={incident.context?.raw_payload} />
          </div>
        )}

        {incident.alert_count > 0 && (
          <div className="mt-6">
            <IncidentAlertsPanel incidentUuid={incident.uuid} />
          </div>
        )}
      </div>
    </div>
  );
}
