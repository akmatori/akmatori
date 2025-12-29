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
    <div className="rounded-lg bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 p-4 mb-4 animate-fade-in">
      <div className="flex items-start gap-3">
        <AlertCircle className="w-5 h-5 text-red-500 flex-shrink-0 mt-0.5" />
        <p className="text-sm text-red-700 dark:text-red-300 flex-1">{message}</p>
        {dismissible && (
          <button
            onClick={handleDismiss}
            className="flex-shrink-0 text-red-400 hover:text-red-600 dark:hover:text-red-300 transition-colors"
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
    <div className="rounded-lg bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800 p-4 mb-4 animate-fade-in">
      <div className="flex items-start gap-3">
        <CheckCircle className="w-5 h-5 text-green-500 flex-shrink-0 mt-0.5" />
        <p className="text-sm text-green-700 dark:text-green-300 flex-1">{message}</p>
        {dismissible && (
          <button
            onClick={handleDismiss}
            className="flex-shrink-0 text-green-400 hover:text-green-600 dark:hover:text-green-300 transition-colors"
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
    <div className="rounded-lg bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 p-4 mb-4 animate-fade-in">
      <div className="flex items-start gap-3">
        <AlertTriangle className="w-5 h-5 text-amber-500 flex-shrink-0 mt-0.5" />
        <p className="text-sm text-amber-700 dark:text-amber-300 flex-1">{message}</p>
        {dismissible && (
          <button
            onClick={handleDismiss}
            className="flex-shrink-0 text-amber-400 hover:text-amber-600 dark:hover:text-amber-300 transition-colors"
            aria-label="Dismiss"
          >
            <X size={16} />
          </button>
        )}
      </div>
    </div>
  );
}
