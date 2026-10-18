import { MessageSquare, Terminal, Bell } from 'lucide-react';
import type { Incident } from '../../types';
import { formatRelativeShort } from '../../utils/format';
import TrendSparkline from '../TrendSparkline';
import SourceKindChip from '../shared/SourceKindChip';
import { getIncidentStatusConfig, isFinishedStatus } from './incidentStatus';

interface IncidentsTableProps {
  incidents: Incident[];
  onOpen: (incident: Incident) => void;
}

// IncidentsTable renders the Incidents list. Row actions open the detail modal.
export default function IncidentsTable({ incidents, onOpen }: IncidentsTableProps) {
  return (
    <div className="overflow-x-auto">
      <table className="table">
        <thead>
          <tr>
            <th>Issue</th>
            <th>Trend</th>
            <th>Age</th>
            <th>Last seen</th>
            <th>Status</th>
            <th>Alerts</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {incidents.map((incident) => {
            const statusConfig = getIncidentStatusConfig(
              incident.status,
              incident.monitor_until,
              incident.source_kind,
              incident.closed_reason,
            );
            const StatusIcon = statusConfig.icon;
            const alertCount = incident.alert_count ?? 0;

            return (
              <tr key={incident.id}>
                <td className="max-w-xs">
                  <div className="flex flex-col gap-0.5">
                    <span
                      className="text-gray-800 dark:text-gray-100 font-semibold text-sm truncate"
                      title={incident.title || undefined}
                    >
                      {incident.title || <span className="text-gray-400 italic font-normal">No title</span>}
                    </span>
                    <div className="flex items-center gap-1.5 flex-wrap">
                      <SourceKindChip kind={incident.source_kind} />
                      <code className="text-xs text-gray-400 dark:text-gray-500">
                        {incident.uuid.slice(0, 8)}
                      </code>
                      {incident.source && (
                        <span className="text-xs text-gray-400 dark:text-gray-500 truncate max-w-[120px]" title={incident.source}>
                          {incident.source}
                        </span>
                      )}
                    </div>
                  </div>
                </td>
                <td className="text-gray-500 dark:text-gray-400">
                  <TrendSparkline buckets={incident.trend ?? []} />
                </td>
                <td className="text-gray-500 dark:text-gray-400 text-sm whitespace-nowrap">
                  {formatRelativeShort(incident.first_seen ?? incident.started_at)}
                </td>
                <td className="text-gray-500 dark:text-gray-400 text-sm whitespace-nowrap">
                  {formatRelativeShort(incident.last_seen ?? incident.started_at)}
                </td>
                <td>
                  <div className="flex flex-col gap-0.5">
                    <span className={`badge ${statusConfig.class} inline-flex items-center gap-1`}>
                      <StatusIcon className="w-3 h-3" />
                      {statusConfig.label}
                    </span>
                    {statusConfig.subLabel && (
                      <span className="text-xs text-gray-400 dark:text-gray-500">
                        {statusConfig.subLabel}
                      </span>
                    )}
                  </div>
                </td>
                <td>
                  {alertCount > 1 ? (
                    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
                      <Bell className="w-3 h-3" />
                      {alertCount}
                    </span>
                  ) : (
                    <span className="text-xs text-gray-400 dark:text-gray-500">
                      {alertCount}
                    </span>
                  )}
                </td>
                <td>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => onOpen(incident)}
                      className={`btn btn-ghost p-1.5 ${incident.status === 'running' ? 'text-primary-500 animate-pulse' : ''}`}
                      title="View reasoning log"
                    >
                      <Terminal className="w-4 h-4" />
                    </button>
                    {isFinishedStatus(incident.status) && (
                      <button
                        onClick={() => onOpen(incident)}
                        className="btn btn-ghost p-1.5"
                        title="View response"
                      >
                        <MessageSquare className="w-4 h-4" />
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
