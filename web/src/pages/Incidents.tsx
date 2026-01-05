import { useEffect, useState, useRef, useCallback, useMemo } from 'react';
import { RefreshCw, X, Plus, MessageSquare, Activity, Clock, CheckCircle, AlertCircle, Terminal, ChevronDown, ChevronRight } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { SuccessMessage } from '../components/ErrorMessage';
import TimeRangePicker from '../components/TimeRangePicker';
import { incidentsApi } from '../api/client';
import type { Incident } from '../types';

// Default: last 30 minutes
const DEFAULT_TIME_RANGE = 30 * 60;
// Default: refresh every 1 minute
const DEFAULT_REFRESH_INTERVAL = 60000;

type ModalType = 'reasoning' | 'response';

export default function Incidents() {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selectedIncident, setSelectedIncident] = useState<Incident | null>(null);
  const [showModal, setShowModal] = useState(false);
  const [modalType, setModalType] = useState<ModalType>('reasoning');
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [newTask, setNewTask] = useState('');
  const [creating, setCreating] = useState(false);
  const [createSuccess, setCreateSuccess] = useState<string | null>(null);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [showToolCalls, setShowToolCalls] = useState(false);
  const refreshIntervalRef = useRef<number | null>(null);
  const logContainerRef = useRef<HTMLDivElement | null>(null);

  // Time range picker state
  const now = Math.floor(Date.now() / 1000);
  const [timeFrom, setTimeFrom] = useState(now - DEFAULT_TIME_RANGE);
  const [timeTo, setTimeTo] = useState(now);
  const [listRefreshInterval, setListRefreshInterval] = useState(DEFAULT_REFRESH_INTERVAL);
  const listRefreshRef = useRef<number | null>(null);

  // Load incidents with current time range
  const loadIncidents = useCallback(async (from?: number, to?: number) => {
    try {
      setLoading(true);
      setError('');
      // For relative time ranges, recalculate "to" as now
      const currentNow = Math.floor(Date.now() / 1000);
      const effectiveFrom = from ?? timeFrom;
      const effectiveTo = to ?? currentNow;
      const data = await incidentsApi.list(effectiveFrom, effectiveTo);
      setIncidents(data);
      // Update timeTo to current time for relative ranges
      if (!to) {
        setTimeTo(currentNow);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load incidents');
    } finally {
      setLoading(false);
    }
  }, [timeFrom]);

  // Initial load
  useEffect(() => {
    loadIncidents();
  }, []);

  // Auto-refresh for incidents list
  useEffect(() => {
    if (listRefreshInterval > 0) {
      listRefreshRef.current = window.setInterval(() => {
        loadIncidents();
      }, listRefreshInterval);
    }

    return () => {
      if (listRefreshRef.current) {
        clearInterval(listRefreshRef.current);
        listRefreshRef.current = null;
      }
    };
  }, [listRefreshInterval, loadIncidents]);

  // Handle time range change
  const handleTimeRangeChange = useCallback((from: number, to: number) => {
    setTimeFrom(from);
    setTimeTo(to);
    loadIncidents(from, to);
  }, [loadIncidents]);

  // Handle refresh interval change
  const handleRefreshIntervalChange = useCallback((interval: number) => {
    setListRefreshInterval(interval);
  }, []);

  useEffect(() => {
    if (showModal && selectedIncident && selectedIncident.status === 'running' && autoRefresh) {
      refreshIntervalRef.current = window.setInterval(async () => {
        try {
          const updated = await incidentsApi.get(selectedIncident.uuid);
          setSelectedIncident(updated);
          setIncidents(prev => prev.map(i => i.uuid === updated.uuid ? updated : i));
        } catch (err) {
          console.error('Failed to refresh incident:', err);
        }
      }, 2000);
    }

    return () => {
      if (refreshIntervalRef.current) {
        clearInterval(refreshIntervalRef.current);
        refreshIntervalRef.current = null;
      }
    };
  }, [showModal, selectedIncident?.uuid, selectedIncident?.status, autoRefresh]);

  // Auto-scroll to bottom when log updates
  useEffect(() => {
    if (logContainerRef.current && modalType === 'reasoning') {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
    }
  }, [selectedIncident?.full_log, modalType]);

  const getStatusConfig = (status: string) => {
    switch (status) {
      case 'completed':
        return { class: 'badge-success', icon: CheckCircle, label: 'Completed' };
      case 'running':
        return { class: 'badge-primary', icon: Activity, label: 'Running' };
      case 'failed':
        return { class: 'badge-error', icon: AlertCircle, label: 'Failed' };
      default:
        return { class: 'badge-default', icon: Clock, label: 'Pending' };
    }
  };

  const openModal = useCallback(async (incident: Incident, type: ModalType) => {
    try {
      const latest = await incidentsApi.get(incident.uuid);
      setSelectedIncident(latest);
    } catch {
      setSelectedIncident(incident);
    }
    setModalType(type);
    setShowModal(true);
  }, []);

  const closeModal = () => {
    setShowModal(false);
    setSelectedIncident(null);
  };

  // Parse log to separate tool calls from other entries
  const parsedLog = useMemo(() => {
    if (!selectedIncident?.full_log) return null;

    const lines = selectedIncident.full_log.split('\n');
    const entries: Array<{
      type: 'regular' | 'tool_call';
      content: string;
      output?: string;
      isMultiline?: boolean
    }> = [];

    let inToolCall = false;
    let inOutput = false;
    let heredocDelimiter: string | null = null;
    let toolCallLines: string[] = [];
    let outputLines: string[] = [];

    const flushToolCall = () => {
      if (toolCallLines.length > 0) {
        const fullContent = toolCallLines.join('\n');
        const fullOutput = outputLines.length > 0 ? outputLines.join('\n') : undefined;
        entries.push({
          type: 'tool_call',
          content: fullContent,
          output: fullOutput,
          isMultiline: toolCallLines.length > 1
        });
        toolCallLines = [];
        outputLines = [];
      }
      inToolCall = false;
      inOutput = false;
      heredocDelimiter = null;
    };

    // Markers that indicate a new section (end current tool call)
    const isNewSection = (line: string) =>
      line.startsWith('✅ Ran:') ||
      line.startsWith('❌ Failed:') ||
      line.startsWith('🤔 ') ||
      line.startsWith('📝 ');

    for (const line of lines) {
      if (inToolCall) {
        // Check if this is a new section (flush current tool call first)
        if (isNewSection(line)) {
          flushToolCall();
          // Fall through to process this line as new tool call or regular
        } else if (inOutput) {
          // Collecting output lines
          outputLines.push(line);
          continue;
        } else if (line === 'Output:') {
          // Start of output section
          inOutput = true;
          continue;
        } else if (heredocDelimiter) {
          // In heredoc mode - check for termination
          toolCallLines.push(line);
          if (line.startsWith(heredocDelimiter) || line.match(new RegExp(`^${heredocDelimiter}["']?\\)?$`))) {
            // Heredoc ended, now wait for Output:
            heredocDelimiter = null;
          }
          continue;
        } else {
          // Collecting additional lines for this tool call (command continuation)
          toolCallLines.push(line);
          continue;
        }
      }

      // Not in tool call (or just flushed) - check for new tool call
      if (line.startsWith('✅ Ran:') || line.startsWith('❌ Failed:')) {
        // Check for heredoc pattern
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

    // Flush any remaining tool call (in case heredoc wasn't properly closed)
    flushToolCall();

    const toolCallCount = entries.filter(e => e.type === 'tool_call').length;
    return { entries, toolCallCount };
  }, [selectedIncident?.full_log]);

  const handleCreateIncident = async () => {
    if (!newTask.trim()) return;

    try {
      setCreating(true);
      setError('');
      const response = await incidentsApi.create({ task: newTask.trim() });
      setCreateSuccess(`Incident created: ${response.uuid.slice(0, 8)}...`);
      setNewTask('');
      setShowCreateModal(false);
      loadIncidents();
      setTimeout(() => setCreateSuccess(null), 5000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create incident');
    } finally {
      setCreating(false);
    }
  };

  return (
    <div>
      <PageHeader
        title="Incidents"
        description="Monitor all incident manager sessions and execution logs"
        action={
          <div className="flex items-center gap-3">
            <TimeRangePicker
              from={timeFrom}
              to={timeTo}
              refreshInterval={listRefreshInterval}
              onChange={handleTimeRangeChange}
              onRefreshIntervalChange={handleRefreshIntervalChange}
            />
            <button onClick={() => setShowCreateModal(true)} className="btn btn-primary">
              <Plus className="w-4 h-4" />
              New Incident
            </button>
            <button onClick={() => loadIncidents()} className="btn btn-secondary" disabled={loading}>
              <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
              Refresh
            </button>
          </div>
        }
      />

      {error && <ErrorMessage message={error} />}
      {createSuccess && <SuccessMessage message={createSuccess} />}

      {loading ? (
        <LoadingSpinner />
      ) : (
        <div className="card">
          {incidents.length === 0 ? (
            <div className="py-16 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
              <Activity className="w-12 h-12 mx-auto text-gray-400 mb-3" />
              <p className="text-gray-500 dark:text-gray-400">No incidents found</p>
              <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">Create a new incident to get started</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="table">
                <thead>
                  <tr>
                    <th>UUID</th>
                    <th>Source</th>
                    <th>Title</th>
                    <th>Status</th>
                    <th>Started</th>
                    <th>Duration</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {incidents.map((incident) => {
                    const statusConfig = getStatusConfig(incident.status);
                    const StatusIcon = statusConfig.icon;
                    const duration = incident.completed_at
                      ? Math.round((new Date(incident.completed_at).getTime() - new Date(incident.started_at).getTime()) / 1000)
                      : null;

                    return (
                      <tr key={incident.id}>
                        <td>
                          <code className="text-xs bg-gray-100 dark:bg-gray-800 px-2 py-1 rounded">
                            {incident.uuid.slice(0, 8)}
                          </code>
                        </td>
                        <td className="text-gray-600 dark:text-gray-300 capitalize">
                          {incident.source}
                        </td>
                        <td className="max-w-xs">
                          <span className="text-gray-700 dark:text-gray-200 text-sm truncate block" title={incident.title || '-'}>
                            {incident.title || <span className="text-gray-400 italic">No title</span>}
                          </span>
                        </td>
                        <td>
                          <span className={`badge ${statusConfig.class} inline-flex items-center gap-1`}>
                            <StatusIcon className="w-3 h-3" />
                            {statusConfig.label}
                          </span>
                        </td>
                        <td className="text-gray-500 dark:text-gray-400 text-sm">
                          {new Date(incident.started_at).toLocaleString('en-US', {
                            month: 'short',
                            day: '2-digit',
                            hour: '2-digit',
                            minute: '2-digit',
                            hour12: false
                          })}
                        </td>
                        <td className="text-gray-500 dark:text-gray-400 text-sm font-mono">
                          {duration !== null ? `${duration}s` : '-'}
                        </td>
                        <td>
                          <div className="flex items-center gap-2">
                            <button
                              onClick={() => openModal(incident, 'reasoning')}
                              className={`btn btn-ghost p-1.5 ${incident.status === 'running' ? 'text-primary-500 animate-pulse' : ''}`}
                              title="View reasoning log"
                            >
                              <Terminal className="w-4 h-4" />
                            </button>
                            {(incident.status === 'completed' || incident.status === 'failed') && (
                              <button
                                onClick={() => openModal(incident, 'response')}
                                className="btn btn-ghost p-1.5"
                                title="View response"
                              >
                                <MessageSquare className="w-4 h-4" />
                              </button>
                            )}
                          </div>
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

      {/* Detail Modal */}
      {showModal && selectedIncident && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-800 rounded-xl shadow-2xl max-w-5xl w-full max-h-[90vh] flex flex-col animate-fade-in">
            {/* Modal Header */}
            <div className="flex items-center justify-between p-6 border-b border-gray-200 dark:border-gray-700">
              <div>
                <div className="flex items-center gap-3">
                  <h2 className="text-xl font-semibold text-gray-900 dark:text-white">
                    {modalType === 'reasoning' ? 'Reasoning Log' : 'Response'}
                  </h2>
                  <span className={`badge ${getStatusConfig(selectedIncident.status).class}`}>
                    {selectedIncident.status}
                  </span>
                </div>
                <div className="mt-1 flex items-center gap-4 text-sm text-gray-500 dark:text-gray-400">
                  <span>
                    UUID: <code className="text-primary-600 dark:text-primary-400">{selectedIncident.uuid.slice(0, 8)}</code>
                  </span>
                  <span className="text-gray-300 dark:text-gray-600">|</span>
                  <span>Source: {selectedIncident.source}</span>
                </div>
              </div>
              <button onClick={closeModal} className="btn btn-ghost p-2" title="Close">
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Modal Body */}
            <div ref={logContainerRef} className="flex-1 overflow-y-auto p-6">
              {modalType === 'reasoning' ? (
                <div className="bg-gray-900 rounded-lg p-6 font-mono text-sm overflow-x-auto text-gray-100 min-h-[200px]">
                  <div className="flex items-center gap-2 text-gray-500 mb-4 pb-4 border-b border-gray-700">
                    <Terminal className="w-4 h-4" />
                    <span className="text-xs font-medium uppercase tracking-wide">Execution Log</span>
                    {parsedLog && parsedLog.toolCallCount > 0 && (
                      <button
                        onClick={() => setShowToolCalls(!showToolCalls)}
                        className="ml-4 flex items-center gap-1.5 px-2 py-1 rounded text-xs bg-gray-800 hover:bg-gray-700 transition-colors"
                      >
                        {showToolCalls ? (
                          <ChevronDown className="w-3 h-3" />
                        ) : (
                          <ChevronRight className="w-3 h-3" />
                        )}
                        <span>Tool Calls ({parsedLog.toolCallCount})</span>
                      </button>
                    )}
                    {selectedIncident.status === 'running' && autoRefresh && (
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
                              {/* Command box */}
                              <div className="text-gray-300 bg-gray-800/70 px-3 py-2 rounded border-l-2 border-blue-500">
                                {entry.content}
                              </div>
                              {/* Output box */}
                              {entry.output && (
                                <div className="mt-1 text-gray-400 bg-gray-800/40 px-3 py-2 rounded border-l-2 border-gray-600 text-xs">
                                  <div className="text-gray-500 text-[10px] uppercase tracking-wide mb-1">Output:</div>
                                  {entry.output}
                                </div>
                              )}
                            </div>
                          );
                        }
                        return <div key={index}>{entry.content}</div>;
                      })}
                    </div>
                  ) : (
                    selectedIncident.status === 'pending'
                      ? '> Waiting for execution to start...'
                      : '> No log available yet'
                  )}
                </div>
              ) : (
                <div className="bg-gray-50 dark:bg-gray-900 rounded-lg p-6 min-h-[200px]">
                  {selectedIncident.response ? (
                    <div className="whitespace-pre-wrap text-gray-700 dark:text-gray-300 font-mono text-sm">
                      {selectedIncident.response
                        .replace(/\[FINAL_RESULT\]\n?/g, '')
                        .replace(/\[\/FINAL_RESULT\]\n?/g, '')
                        .trim()}
                    </div>
                  ) : (
                    <p className="text-gray-500 text-center py-8">
                      {selectedIncident.status === 'pending' || selectedIncident.status === 'running'
                        ? 'Response will be available when the incident completes...'
                        : 'No response available'}
                    </p>
                  )}
                </div>
              )}
            </div>

            {/* Modal Footer */}
            <div className="flex items-center justify-between p-6 border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
              <div className="flex items-center gap-4">
                {modalType === 'reasoning' && selectedIncident.status === 'running' && (
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={autoRefresh}
                      onChange={(e) => setAutoRefresh(e.target.checked)}
                    />
                    <span className="text-sm text-gray-600 dark:text-gray-400">Auto-refresh (2s)</span>
                  </label>
                )}
              </div>
              <div className="flex items-center gap-3">
                {(selectedIncident.status === 'completed' || selectedIncident.status === 'failed') && (
                  <button
                    onClick={() => setModalType(modalType === 'reasoning' ? 'response' : 'reasoning')}
                    className="btn btn-secondary"
                  >
                    {modalType === 'reasoning' ? (
                      <>
                        <MessageSquare className="w-4 h-4" />
                        View Response
                      </>
                    ) : (
                      <>
                        <Terminal className="w-4 h-4" />
                        View Log
                      </>
                    )}
                  </button>
                )}
                <button onClick={closeModal} className="btn btn-secondary">
                  Close
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Create Incident Modal */}
      {showCreateModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-800 rounded-xl shadow-2xl max-w-2xl w-full animate-fade-in">
            {/* Modal Header */}
            <div className="flex items-center justify-between p-6 border-b border-gray-200 dark:border-gray-700">
              <div>
                <h2 className="text-xl font-semibold text-gray-900 dark:text-white">Create Incident</h2>
                <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  Start an incident investigation by providing a task description
                </p>
              </div>
              <button onClick={() => setShowCreateModal(false)} className="btn btn-ghost p-2" title="Close">
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Modal Body */}
            <div className="p-6">
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
                Task Description <span className="text-red-500">*</span>
              </label>
              <textarea
                value={newTask}
                onChange={(e) => setNewTask(e.target.value)}
                placeholder="Describe the task or investigation you want to perform..."
                className="input-field min-h-[180px] resize-y"
                autoFocus
              />
              <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">
                The incident manager will analyze this task and coordinate with skills to resolve it.
              </p>
            </div>

            {/* Modal Footer */}
            <div className="flex items-center justify-end gap-3 p-6 border-t border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
              <button onClick={() => setShowCreateModal(false)} className="btn btn-secondary" disabled={creating}>
                Cancel
              </button>
              <button
                onClick={handleCreateIncident}
                className="btn btn-primary"
                disabled={creating || !newTask.trim()}
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
      )}
    </div>
  );
}
