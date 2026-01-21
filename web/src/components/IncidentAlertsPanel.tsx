import React, { useState, useEffect } from 'react';
import { incidentAlertsApi } from '../api/client';
import type { IncidentAlert } from '../types';

interface Props {
  incidentUuid: string;
  onDetach?: (alertId: number) => void;
}

export const IncidentAlertsPanel: React.FC<Props> = ({ incidentUuid, onDetach }) => {
  const [alerts, setAlerts] = useState<IncidentAlert[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    loadAlerts();
  }, [incidentUuid]);

  const loadAlerts = async () => {
    try {
      const data = await incidentAlertsApi.list(incidentUuid);
      setAlerts(data);
    } catch (err) {
      console.error('Failed to load alerts:', err);
    } finally {
      setLoading(false);
    }
  };

  const handleDetach = async (alertId: number) => {
    if (!confirm('Detach this alert and create a new incident?')) return;
    try {
      await incidentAlertsApi.detach(incidentUuid, alertId);
      await loadAlerts();
      onDetach?.(alertId);
    } catch (err) {
      console.error('Failed to detach alert:', err);
    }
  };

  const getSeverityColor = (severity: string) => {
    switch (severity) {
      case 'critical': return 'bg-red-100 text-red-800';
      case 'high': return 'bg-orange-100 text-orange-800';
      case 'warning': return 'bg-yellow-100 text-yellow-800';
      default: return 'bg-blue-100 text-blue-800';
    }
  };

  const getStatusColor = (status: string) => {
    return status === 'firing' ? 'text-red-600' : 'text-green-600';
  };

  if (loading) return <div className="p-4">Loading alerts...</div>;

  return (
    <div className="border rounded-lg">
      <div className="px-4 py-3 bg-gray-50 border-b flex justify-between items-center">
        <h4 className="font-medium">Aggregated Alerts ({alerts.length})</h4>
      </div>
      <div className="divide-y">
        {alerts.map((alert) => (
          <div key={alert.id} className="p-4 flex items-start justify-between">
            <div className="flex-1">
              <div className="flex items-center space-x-2">
                <span className={`w-2 h-2 rounded-full ${alert.status === 'firing' ? 'bg-red-500' : 'bg-green-500'}`} />
                <span className="font-medium">{alert.alert_name}</span>
                <span className={`px-2 py-0.5 text-xs rounded ${getSeverityColor(alert.severity)}`}>
                  {alert.severity}
                </span>
                <span className={`text-sm ${getStatusColor(alert.status)}`}>
                  {alert.status}
                </span>
              </div>
              <div className="mt-1 text-sm text-gray-600">
                {alert.target_host} {alert.target_service && `/ ${alert.target_service}`}
              </div>
              <div className="mt-1 text-sm text-gray-500">{alert.summary}</div>
              <div className="mt-2 text-xs text-gray-400">
                Confidence: {(alert.correlation_confidence * 100).toFixed(0)}% - {alert.correlation_reason}
              </div>
            </div>
            <button
              onClick={() => handleDetach(alert.id)}
              className="ml-4 px-3 py-1 text-sm text-gray-600 hover:text-red-600 border rounded"
            >
              Detach
            </button>
          </div>
        ))}
        {alerts.length === 0 && (
          <div className="p-4 text-gray-500 text-center">No alerts aggregated</div>
        )}
      </div>
    </div>
  );
};
