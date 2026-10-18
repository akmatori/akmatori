import { Link } from 'react-router-dom';
import type { Incident } from '../../types';
import { formatRelativeShort } from '../../utils/format';
import TrendSparkline from '../TrendSparkline';
import SourceKindChip from '../shared/SourceKindChip';
import { getIncidentState, TONE_CLASSES } from './incidentStatus';

interface IncidentListProps {
  incidents: Incident[];
  selectedUUID?: string;
}

// IncidentList is the left column of the Incidents split view. Each row is a
// link to /incidents/:uuid; the left rail color carries the incident state.
export default function IncidentList({ incidents, selectedUUID }: IncidentListProps) {
  return (
    <ul aria-label="Incidents">
      {incidents.map((incident) => {
        const state = getIncidentState(incident);
        const tone = TONE_CLASSES[state.tone];
        const selected = incident.uuid === selectedUUID;
        const alertCount = incident.alert_count ?? 0;
        const hasTrend = (incident.trend ?? []).some((v) => v > 0);
        return (
          <li key={incident.uuid}>
            <Link
              to={`/incidents/${incident.uuid}`}
              aria-current={selected ? 'page' : undefined}
              className={`flex flex-col gap-1.5 pl-3.5 pr-4 py-3 border-b border-line border-l-[3px] ${tone.rail} transition-colors ${
                selected ? 'bg-primary-500/10' : 'hover:bg-surface-2'
              }`}
            >
              <span className="flex items-start gap-3">
                <span className="flex-1 min-w-0 text-sm font-medium text-fg line-clamp-2 break-words">
                  {incident.title || `Incident ${incident.uuid.slice(0, 8)}`}
                </span>
                {hasTrend && (
                  <span className="hidden xl:block flex-shrink-0 text-fg-subtle">
                    <TrendSparkline buckets={incident.trend ?? []} />
                  </span>
                )}
              </span>
              <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-fg-subtle">
                <span className={tone.text}>{state.label}</span>
                <SourceKindChip kind={incident.source_kind} />
                {alertCount > 1 && <span>{alertCount} alerts</span>}
                <span className="ml-auto tabular-nums" title="Age">
                  {formatRelativeShort(incident.first_seen ?? incident.started_at)}
                </span>
              </span>
            </Link>
          </li>
        );
      })}
    </ul>
  );
}
