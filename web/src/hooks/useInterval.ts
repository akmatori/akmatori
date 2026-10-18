import { useEffect } from 'react';
import { useLatestRef } from './useLatestRef';

// useInterval runs `callback` every `delayMs` milliseconds. Pass `null` or a
// non-positive delay to pause. The callback is read through a ref, so the
// timer is not restarted when the callback identity changes.
export function useInterval(callback: () => void, delayMs: number | null): void {
  const callbackRef = useLatestRef(callback);

  useEffect(() => {
    if (delayMs === null || delayMs <= 0) return;
    const id = window.setInterval(() => callbackRef.current(), delayMs);
    return () => window.clearInterval(id);
  }, [delayMs, callbackRef]);
}
