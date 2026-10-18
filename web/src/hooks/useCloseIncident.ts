import { useState, useCallback } from 'react';
import { incidentsApi, ApiError } from '../api/client';
import { getErrorMessage } from '../utils/errors';

export interface CloseConfirmation {
  firingAlertCount: number;
  inProgress: boolean;
}

interface CloseConflictBody {
  requires_confirmation?: boolean;
  firing_alert_count?: number;
  in_progress?: boolean;
}

// useCloseIncident drives the "Close Incident" action. A first attempt is
// unforced; when the API answers 409 with requires_confirmation the caller
// shows CloseIncidentModal (driven by `confirmClose`) to force the close.
export function useCloseIncident(uuid: string | undefined, refresh: () => Promise<void>) {
  const [closing, setClosing] = useState(false);
  const [closeError, setCloseError] = useState('');
  const [confirmClose, setConfirmClose] = useState<CloseConfirmation | null>(null);

  const requestClose = useCallback(async () => {
    if (!uuid) return;
    setCloseError('');
    setClosing(true);
    try {
      await incidentsApi.close(uuid, false);
      await refresh();
    } catch (err) {
      const body = err instanceof ApiError && err.status === 409
        ? (err.body as CloseConflictBody | undefined)
        : undefined;
      if (body?.requires_confirmation) {
        setConfirmClose({ firingAlertCount: body.firing_alert_count ?? 0, inProgress: body.in_progress ?? false });
      } else {
        setCloseError(getErrorMessage(err, 'Failed to close incident'));
      }
    } finally {
      setClosing(false);
    }
  }, [uuid, refresh]);

  const dismissConfirm = useCallback(() => setConfirmClose(null), []);

  const handleConfirmed = useCallback(() => {
    setConfirmClose(null);
    void refresh();
  }, [refresh]);

  return { closing, closeError, confirmClose, requestClose, dismissConfirm, handleConfirmed };
}
