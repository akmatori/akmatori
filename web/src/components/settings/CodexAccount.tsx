import { useEffect, useState } from 'react';
import { codexAuthApi } from '../../api/client';
import type { CodexAuthStatus } from '../../types';
import { getErrorMessage } from '../../utils/errors';
import ErrorMessage from '../ErrorMessage';

export default function CodexAccount({ onConnectionChange }: { onConnectionChange: () => void }) {
  const [status, setStatus] = useState<CodexAuthStatus>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    if (busy || error || (status && !status.pending)) return;
    let current = true;
    const timer = window.setTimeout(async () => {
      try {
        const next = await codexAuthApi.status();
        if (!current) return;
        setStatus(next);
        if (status && next.connected !== status.connected) onConnectionChange();
      } catch (err) {
        if (current) setError(getErrorMessage(err, 'Could not check Codex sign-in'));
      }
    }, status?.pending ? 2000 : 0);
    return () => {
      current = false;
      window.clearTimeout(timer);
    };
  }, [busy, error, onConnectionChange, status]);

  async function act(action: keyof typeof codexAuthApi) {
    setBusy(true);
    setError(undefined);
    try {
      const next = await codexAuthApi[action]();
      setStatus(next);
      if (next.connected !== status?.connected) onConnectionChange();
    } catch (err) {
      setError(getErrorMessage(err, 'Could not update Codex sign-in'));
    } finally {
      setBusy(false);
    }
  }

  const message = error ?? status?.error;
  return (
    <section className="rounded-lg border border-line bg-surface p-4 space-y-3">
      <div>
        <h3 className="text-sm font-semibold text-fg">Codex subscription</h3>
        <p className="mt-1 text-xs text-fg-subtle">
          All Codex configurations use the same ChatGPT account.
        </p>
      </div>
      {message && <ErrorMessage message={message} />}
      {status?.pending ? (
        <div className="space-y-2" aria-live="polite">
          {status.user_code ? (
            <p className="text-sm text-fg">
              Open <a className="underline" href="https://auth.openai.com/codex/device" target="_blank" rel="noopener noreferrer">
                ChatGPT device sign-in
              </a> and enter <code className="rounded bg-surface-2 px-1.5 py-0.5 font-semibold select-all">{status.user_code}</code>.
            </p>
          ) : (
            <p className="text-sm text-fg-muted">Requesting a device code…</p>
          )}
          <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void act('cancel')}>
            Cancel sign-in
          </button>
        </div>
      ) : status?.connected ? (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm text-fg-muted">Connected to ChatGPT</span>
          <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void act('start')}>
            Reconnect
          </button>
          <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void act('disconnect')}>
            Disconnect
          </button>
        </div>
      ) : status ? (
        <div className="space-y-2">
          <p className="text-xs text-fg-subtle">Enable device code authorization in ChatGPT security settings before signing in.</p>
          <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void act('start')}>
            Connect ChatGPT
          </button>
        </div>
      ) : !error && (
        <p className="text-sm text-fg-muted" role="status">Checking Codex sign-in…</p>
      )}
      {error && (
        <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void act('status')}>
          Check connection
        </button>
      )}
    </section>
  );
}
