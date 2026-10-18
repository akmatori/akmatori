import { AlertCircle, CheckCircle, AlertTriangle, X } from 'lucide-react';
import { useState } from 'react';

interface MessageProps {
  message: string;
  dismissible?: boolean;
  onDismiss?: () => void;
}

export default function ErrorMessage({ message, dismissible = false, onDismiss }: MessageProps) {
  const [dismissed, setDismissed] = useState(false);

  if (dismissed) return null;

  const handleDismiss = () => {
    setDismissed(true);
    onDismiss?.();
  };

  return (
    <div className="rounded-lg bg-state-fail/10 border border-state-fail/40 px-4 py-3 mb-4 animate-fade-in">
      <div className="flex items-start gap-3">
        <AlertCircle className="w-4 h-4 text-state-fail flex-shrink-0 mt-0.5" />
        <p className="text-sm text-fg flex-1">{message}</p>
        {dismissible && (
          <button
            onClick={handleDismiss}
            className="flex-shrink-0 text-fg-subtle hover:text-fg transition-colors"
            aria-label="Dismiss"
          >
            <X size={16} />
          </button>
        )}
      </div>
    </div>
  );
}

export function SuccessMessage({ message, dismissible = true, onDismiss }: MessageProps) {
  const [dismissed, setDismissed] = useState(false);

  if (dismissed) return null;

  const handleDismiss = () => {
    setDismissed(true);
    onDismiss?.();
  };

  return (
    <div className="rounded-lg bg-state-ok/10 border border-state-ok/40 px-4 py-3 mb-4 animate-fade-in">
      <div className="flex items-start gap-3">
        <CheckCircle className="w-4 h-4 text-state-ok flex-shrink-0 mt-0.5" />
        <p className="text-sm text-fg flex-1">{message}</p>
        {dismissible && (
          <button
            onClick={handleDismiss}
            className="flex-shrink-0 text-fg-subtle hover:text-fg transition-colors"
            aria-label="Dismiss"
          >
            <X size={16} />
          </button>
        )}
      </div>
    </div>
  );
}

export function WarningMessage({ message, dismissible = false, onDismiss }: MessageProps) {
  const [dismissed, setDismissed] = useState(false);

  if (dismissed) return null;

  const handleDismiss = () => {
    setDismissed(true);
    onDismiss?.();
  };

  return (
    <div className="rounded-lg bg-state-need/10 border border-state-need/40 px-4 py-3 mb-4 animate-fade-in">
      <div className="flex items-start gap-3">
        <AlertTriangle className="w-4 h-4 text-state-need flex-shrink-0 mt-0.5" />
        <p className="text-sm text-fg flex-1">{message}</p>
        {dismissible && (
          <button
            onClick={handleDismiss}
            className="flex-shrink-0 text-fg-subtle hover:text-fg transition-colors"
            aria-label="Dismiss"
          >
            <X size={16} />
          </button>
        )}
      </div>
    </div>
  );
}
