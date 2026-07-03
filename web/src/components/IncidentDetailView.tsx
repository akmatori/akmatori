import { useState, useRef, useEffect, useMemo } from 'react';
import { Terminal, MessageSquare, ChevronDown, ChevronRight, RefreshCw, Bell, Shuffle } from 'lucide-react';
import { Link } from 'react-router-dom';
import type { Incident, Alert } from '../types';
import { incidentsApi } from '../api/client';
import MoveIncidentModal from './MoveIncidentModal';

type TabType = 'reasoning' | 'response' | 'alerts';

interface IncidentDetailViewProps {
  incident: Incident;
  autoRefresh?: boolean;
}

export default function IncidentDetailView({ incident, autoRefresh = false }: IncidentDetailViewProps) {
  const [activeTab, setActiveTab] = useState<TabType>('reasoning');
  const [showToolCalls, setShowToolCalls] = useState(false);
  const [alerts, setAlerts] = useState<Alert[] | null>(null);
  const [alertsLoading, setAlertsLoading] = useState(false);
  const [alertsError, setAlertsError] = useState('');
  const [expandedReasoning, setExpandedReasoning] = useState<Set<string>>(new Set());
  const [moveTargetAlert, setMoveTargetAlert] = useState<Alert | null>(null);
  const [moveResult, setMoveResult] = useState<{ incidentUUID: string; isNew: boolean } | null>(null);
  const alertsFetchedForRef = useRef<string | null>(null);
  const logContainerRef = useRef<HTMLDivElement | null>(null);

  // Reset alert fetch state and active tab when the viewed incident changes.
  useEffect(() => {
    alertsFetchedForRef.current = null;
    setAlerts(null);
    setAlertsError('');
    setMoveResult(null);
    setActiveTab('reasoning');
  }, [incident.uuid]);

  const handleMoved = (result: { incidentUUID: string; isNew: boolean }) => {
    setMoveTargetAlert(null);
    setMoveResult(result);
    // Refresh the alerts list — reset the guard first so the lazy-fetch effect
    // can retry if this inline refresh also fails.
    alertsFetchedForRef.current = null;
    setAlerts(null);
    incidentsApi.getAlerts(incident.uuid)
      .then(data => {
        alertsFetchedForRef.current = incident.uuid;
        setAlerts(data);
      })
      .catch(err => { setAlertsError(String(err)); });
  };

  // Auto-scroll to bottom when log updates
  useEffect(() => {
    if (logContainerRef.current && activeTab === 'reasoning') {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
    }
  }, [incident.full_log, activeTab]);

  // Lazy-fetch alerts on first tab open. We track the fetched UUID rather than
  // a boolean so that a fetch cancelled mid-flight (tab/incident switch) does
  // not permanently block a later retry for the same incident.
  useEffect(() => {
    if (activeTab !== 'alerts' || alertsFetchedForRef.current === incident.uuid) return;
    setAlertsLoading(true);
    let cancelled = false;
    incidentsApi.getAlerts(incident.uuid)
      .then(data => {
        if (!cancelled) {
          alertsFetchedForRef.current = incident.uuid;
          setAlerts(data);
          setAlertsLoading(false);
        }
      })
      .catch(err => {
        if (!cancelled) {
          alertsFetchedForRef.current = incident.uuid;
          setAlertsError(String(err));
          setAlertsLoading(false);
        }
      });
    return () => { cancelled = true; };
  }, [activeTab, incident.uuid]);

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
    <div className="flex flex-col min-h-0">
      {/* Tab Navigation */}
      <div className="flex border-b border-gray-200 dark:border-gray-700 px-6 shrink-0">
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
        {incident.source_kind === 'alert' && (
          <button
            onClick={() => setActiveTab('alerts')}
            className={`px-4 py-3 text-sm font-medium border-b-2 transition-colors ${
              activeTab === 'alerts'
                ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
            }`}
          >
            <span className="flex items-center gap-2">
              <Bell className="w-4 h-4" />
              Alerts
            </span>
          </button>
        )}
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
          <div className="min-h-[200px]">
            {moveResult && (
              <div className="mb-4 flex items-center gap-2 px-4 py-3 rounded-lg bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-300 text-sm">
                <span>{moveResult.isNew ? 'Alert unlinked. New investigation started:' : 'Alert linked to incident:'}</span>
                <Link
                  to={`/incidents/${moveResult.incidentUUID}`}
                  className="font-mono underline hover:no-underline"
                >
                  {moveResult.incidentUUID}
                </Link>
              </div>
            )}
            {alertsLoading ? (
              <div className="flex items-center justify-center py-12 text-gray-400">
                <RefreshCw className="w-5 h-5 animate-spin mr-2" />
                Loading alerts...
              </div>
            ) : alertsError ? (
              <p className="text-red-400 text-center py-8">{alertsError}</p>
            ) : !alerts || alerts.length === 0 ? (
              <p className="text-gray-500 text-center py-8">No alerts linked to this incident</p>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-gray-200 dark:border-gray-700 text-left text-xs text-gray-500 uppercase tracking-wide">
                      <th className="pb-2 pr-4 font-medium">Alert</th>
                      <th className="pb-2 pr-4 font-medium">Host</th>
                      <th className="pb-2 pr-4 font-medium">Status</th>
                      <th className="pb-2 pr-4 font-medium">Fired</th>
                      <th className="pb-2 pr-4 font-medium">Resolved</th>
                      <th className="pb-2 font-medium"></th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                    {alerts.map((alert) => {
                      const isOrigin = !alert.correlated;
                      const isExpanded = expandedReasoning.has(alert.uuid);
                      return (
                        <tr key={alert.uuid} className="align-top">
                          <td className="py-3 pr-4">
                            <div className="flex items-start gap-2">
                              <span className="font-medium text-gray-900 dark:text-gray-100">{alert.alert_name}</span>
                              {isOrigin && (
                                <span className="shrink-0 inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-semibold bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300">
                                  Origin
                                </span>
                              )}
                              {alert.correlated && (
                                <button
                                  onClick={() => setExpandedReasoning(prev => {
                                    const next = new Set(prev);
                                    if (next.has(alert.uuid)) next.delete(alert.uuid);
                                    else next.add(alert.uuid);
                                    return next;
                                  })}
                                  className="shrink-0 inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-semibold bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300 hover:bg-purple-200 dark:hover:bg-purple-900/60 transition-colors"
                                  title="Show correlation reasoning"
                                >
                                  Correlated
                                  {alert.correlation_confidence != null && (
                                    <span>{Math.round(alert.correlation_confidence * 100)}%</span>
                                  )}
                                  {isExpanded ? <ChevronDown className="w-2.5 h-2.5" /> : <ChevronRight className="w-2.5 h-2.5" />}
                                </button>
                              )}
                            </div>
                            {isExpanded && alert.correlation_reasoning && (
                              <div className="mt-2 text-xs text-gray-500 dark:text-gray-400 bg-gray-50 dark:bg-gray-800 rounded p-2 max-w-sm">
                                {alert.correlation_reasoning}
                              </div>
                            )}
                          </td>
                          <td className="py-3 pr-4 text-gray-600 dark:text-gray-400 font-mono text-xs">{alert.target_host}</td>
                          <td className="py-3 pr-4">
                            {alert.status === 'firing' ? (
                              <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300">Firing</span>
                            ) : (
                              <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300">Resolved</span>
                            )}
                          </td>
                          <td className="py-3 pr-4 text-gray-500 dark:text-gray-400 text-xs whitespace-nowrap">
                            {new Date(alert.fired_at).toLocaleString()}
                          </td>
                          <td className="py-3 pr-4 text-gray-500 dark:text-gray-400 text-xs whitespace-nowrap">
                            {alert.resolved_at ? new Date(alert.resolved_at).toLocaleString() : '—'}
                          </td>
                          <td className="py-3">
                            <button
                              onClick={() => setMoveTargetAlert(alert)}
                              className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs font-medium text-amber-700 dark:text-amber-300 bg-amber-50 dark:bg-amber-900/20 hover:bg-amber-100 dark:hover:bg-amber-900/40 transition-colors"
                              title="Move this alert to another incident (or unlink into a new one)"
                            >
                              <Shuffle className="w-3 h-3" />
                              Move
                            </button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        )}
      </div>

      {moveTargetAlert && (
        <MoveIncidentModal
          alertUUID={moveTargetAlert.uuid}
          alertTitle={moveTargetAlert.alert_name}
          currentIncidentUUID={incident.uuid}
          onClose={() => setMoveTargetAlert(null)}
          onMoved={handleMoved}
        />
      )}
    </div>
  );
}
