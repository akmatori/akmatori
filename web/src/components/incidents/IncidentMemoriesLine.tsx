import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Brain } from 'lucide-react';
import { memoriesApi } from '../../api/client';

interface IncidentMemoriesLineProps {
  incidentUUID: string;
}

// IncidentMemoriesLine links to the memories that originated from a finished
// incident. It counts memories whose incident_uuid is this incident, so a
// memory the agent only updated (and kept an older incident on) is not
// counted. Nothing renders when there are none or the request fails.
export default function IncidentMemoriesLine({ incidentUUID }: IncidentMemoriesLineProps) {
  const [count, setCount] = useState(0);

  useEffect(() => {
    let cancelled = false;
    memoriesApi
      .list({ incidentUuid: incidentUUID })
      .then((list) => {
        if (!cancelled) setCount(list.length);
      })
      .catch(() => {
        // The line is optional; the summary works without it.
      });
    return () => {
      cancelled = true;
    };
  }, [incidentUUID]);

  if (count === 0) return null;

  return (
    <Link
      to={`/settings/memory?incident=${encodeURIComponent(incidentUUID)}`}
      className="mt-4 inline-flex items-center gap-2 text-sm text-fg-muted hover:text-fg"
    >
      <Brain size={15} className="text-primary-500" aria-hidden="true" />
      {count === 1 ? '1 memory from this incident' : `${count} memories from this incident`}
    </Link>
  );
}
