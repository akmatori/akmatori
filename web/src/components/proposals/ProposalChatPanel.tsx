import { useCallback, useEffect, useRef, useState } from 'react';
import { Send, Bot, User } from 'lucide-react';
import { proposalsApi } from '../../api/client';
import type { ProposalChatMessage } from '../../types';

interface ProposalChatPanelProps {
  proposalUUID: string;
  // Chat is only actionable while the proposal is still open for refinement.
  disabled: boolean;
  // Called after each completed assistant turn so the parent can re-fetch
  // the proposal and refresh the diff (the agent may have revised the draft).
  onTurnComplete: () => void;
}

// ProposalChatPanel renders the operator↔assistant refinement conversation.
// Sending a message POSTs the turn and then polls GET .../chat every 2s
// (the incident-detail polling pattern) until the backing agent run leaves
// the running state.
export default function ProposalChatPanel({
  proposalUUID,
  disabled,
  onTurnComplete,
}: ProposalChatPanelProps) {
  const [messages, setMessages] = useState<ProposalChatMessage[]>([]);
  const [chatStatus, setChatStatus] = useState('');
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const wasRunningRef = useRef(false);

  const isRunning = chatStatus === 'running' || chatStatus === 'pending' || sending;

  const refresh = useCallback(async () => {
    try {
      const res = await proposalsApi.getChat(proposalUUID);
      setMessages(res.messages ?? []);
      setChatStatus(res.chat_status);
      return res.chat_status;
    } catch {
      return '';
    }
  }, [proposalUUID]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Poll while the assistant is generating; fire onTurnComplete on the
  // running → done edge so the parent refreshes the proposal diff.
  useEffect(() => {
    if (!isRunning) {
      if (wasRunningRef.current) {
        wasRunningRef.current = false;
        onTurnComplete();
      }
      return;
    }
    wasRunningRef.current = true;
    const interval = window.setInterval(refresh, 2000);
    return () => clearInterval(interval);
  }, [isRunning, refresh, onTurnComplete]);

  // Keep the transcript scrolled to the newest message.
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [messages.length, isRunning]);

  const send = async () => {
    const message = input.trim();
    if (!message || sending || disabled) return;
    setSending(true);
    setError('');
    try {
      await proposalsApi.sendChat(proposalUUID, message);
      setInput('');
      setChatStatus('running');
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to send message');
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="flex flex-col h-full">
      {/* Transcript */}
      <div ref={scrollRef} className="flex-1 overflow-y-auto p-4 space-y-3 min-h-[200px]">
        {messages.length === 0 && !isRunning && (
          <p className="text-sm text-gray-400 dark:text-gray-500 text-center py-8">
            Ask the assistant to explain, verify, or revise this proposal. It can
            read the cited incidents, runbooks, and memories before editing the draft.
          </p>
        )}
        {messages.map((m) => (
          <div key={m.id} className={`flex gap-2 ${m.role === 'operator' ? 'justify-end' : ''}`}>
            {m.role === 'assistant' && (
              <Bot className="w-5 h-5 mt-1 text-primary-500 flex-shrink-0" />
            )}
            <div
              className={`max-w-[85%] rounded-lg px-3 py-2 text-sm whitespace-pre-wrap break-words ${
                m.role === 'operator'
                  ? 'bg-primary-500 text-white'
                  : 'bg-gray-100 dark:bg-gray-700 text-gray-800 dark:text-gray-100'
              }`}
            >
              {m.content}
            </div>
            {m.role === 'operator' && (
              <User className="w-5 h-5 mt-1 text-gray-400 flex-shrink-0" />
            )}
          </div>
        ))}
        {isRunning && (
          <div className="flex gap-2 items-center text-sm text-gray-400 dark:text-gray-500">
            <Bot className="w-5 h-5 text-primary-500" />
            <span className="animate-pulse">Assistant is working…</span>
          </div>
        )}
      </div>

      {/* Composer */}
      <div className="border-t border-gray-200 dark:border-gray-700 p-3">
        {error && <p className="text-xs text-red-500 mb-2">{error}</p>}
        <div className="flex gap-2">
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                send();
              }
            }}
            placeholder={
              disabled
                ? 'This proposal has been decided — chat is closed.'
                : 'Refine this proposal… (Enter to send, Shift+Enter for newline)'
            }
            disabled={disabled || isRunning}
            rows={2}
            className="flex-1 resize-none rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:opacity-50"
          />
          <button
            onClick={send}
            disabled={disabled || isRunning || !input.trim()}
            className="self-end p-2.5 rounded-lg bg-primary-500 text-white hover:bg-primary-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            title="Send"
          >
            <Send className="w-4 h-4" />
          </button>
        </div>
      </div>
    </div>
  );
}
