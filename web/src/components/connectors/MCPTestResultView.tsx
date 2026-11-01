import { CheckCircle2, XCircle } from 'lucide-react';
import type { MCPTestResult } from '../../types/connectors';

interface Props {
  result: MCPTestResult;
  oauthSelected?: boolean;
  onUseOAuth?: () => void;
}

// MCPTestResultView shows the outcome of a connection test.
export default function MCPTestResultView({ result, oauthSelected, onUseOAuth }: Props) {
  if (result.ok) {
    const tools = result.tools ?? [];
    const server = result.server_info?.name;
    return (
      <div className="rounded-md border border-line p-3 text-sm">
        <p className="flex items-center gap-2 text-fg">
          <CheckCircle2 className="w-4 h-4 text-state-ok" aria-hidden="true" />
          Connected{server ? ` to ${server}` : ''}. {tools.length} {tools.length === 1 ? 'tool' : 'tools'} found.
        </p>
        {tools.length > 0 && (
          <ul className="mt-2 max-h-40 overflow-y-auto space-y-1">
            {tools.map((t) => (
              <li key={t.name} className="text-xs">
                <code className="text-fg">{t.name}</code>
                {t.description && <span className="text-fg-subtle"> — {t.description}</span>}
              </li>
            ))}
          </ul>
        )}
      </div>
    );
  }
  return (
    <div className="rounded-md border border-state-fail/40 bg-state-fail/5 p-3 text-sm">
      <p className="flex items-start gap-2 text-fg">
        <XCircle className="w-4 h-4 mt-0.5 text-state-fail flex-shrink-0" aria-hidden="true" />
        <span>
          {result.auth_required
            ? 'The server requires authentication.'
            : result.error || 'The connection failed.'}
        </span>
      </p>
      {result.auth_required && !oauthSelected && onUseOAuth && (
        <button type="button" className="mt-2 text-sm text-primary-500 hover:underline" onClick={onUseOAuth}>
          Sign in with OAuth instead
        </button>
      )}
    </div>
  );
}
