import { useEffect, useState } from 'react';
import { AlertTriangle } from 'lucide-react';
import { subscribeApiAvailability } from '../api/client';

/**
 * Global banner shown when the Akmatori API becomes unreachable (network failure
 * or a 502/503/504 from the reverse proxy). It listens to the shared
 * availability state in the API client and clears itself as soon as any request
 * succeeds again.
 */
export default function ApiStatusBanner() {
  const [available, setAvailable] = useState(true);

  useEffect(() => subscribeApiAvailability(setAvailable), []);

  if (available) return null;

  return (
    <div
      role="alert"
      className="fixed top-0 inset-x-0 z-50 flex items-center justify-center gap-2 px-4 py-2 text-sm font-medium text-white bg-red-600 shadow-md"
    >
      <AlertTriangle size={16} />
      <span>
        The Akmatori API is currently unavailable. Reconnecting&hellip; some data may be out of date.
      </span>
    </div>
  );
}
