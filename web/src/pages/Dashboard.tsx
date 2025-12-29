import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Activity, Bot, Wrench, ArrowRight, Clock, CheckCircle, AlertCircle, Settings } from 'lucide-react';
import PageHeader from '../components/PageHeader';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import { incidentsApi, skillsApi, toolsApi } from '../api/client';
import type { Incident } from '../types';

export default function Dashboard() {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [skillsCount, setSkillsCount] = useState(0);
  const [toolsCount, setToolsCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    loadDashboardData();
  }, []);

  const loadDashboardData = async () => {
    try {
      setLoading(true);
      setError('');

      const [incidentsData, skillsData, toolsData] = await Promise.all([
        incidentsApi.list(),
        skillsApi.list(),
        toolsApi.list(),
      ]);

      setIncidents(incidentsData?.slice(0, 10) ?? []);
      setSkillsCount(skillsData?.length ?? 0);
      setToolsCount(toolsData?.length ?? 0);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load dashboard');
    } finally {
      setLoading(false);
    }
  };

  const getStatusConfig = (status: string) => {
    switch (status) {
      case 'completed':
        return { class: 'badge-success', icon: CheckCircle, label: 'Completed' };
      case 'running':
        return { class: 'badge-primary', icon: Activity, label: 'Running' };
      case 'failed':
        return { class: 'badge-error', icon: AlertCircle, label: 'Failed' };
      default:
        return { class: 'badge-default', icon: Clock, label: 'Pending' };
    }
  };

  const runningCount = incidents.filter(i => i.status === 'running').length;
  const failedCount = incidents.filter(i => i.status === 'failed').length;
  const completedCount = incidents.filter(i => i.status === 'completed').length;

  if (loading) return <LoadingSpinner />;
  if (error) return <ErrorMessage message={error} />;

  return (
    <div>
      <PageHeader
        title="Dashboard"
        description="System overview and operational metrics"
      />

      {/* Stats Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-8">
        {/* Total Incidents */}
        <Link to="/incidents" className="card group hover:shadow-md transition-shadow">
          <div className="flex items-start justify-between">
            <div>
              <p className="text-sm font-medium text-gray-500 dark:text-gray-400">Total Incidents</p>
              <p className="mt-2 text-3xl font-semibold text-gray-900 dark:text-white">
                {incidents.length}
              </p>
            </div>
            <div className="p-3 rounded-lg bg-primary-50 dark:bg-primary-900/20">
              <Activity className="w-6 h-6 text-primary-600 dark:text-primary-400" />
            </div>
          </div>
          <div className="mt-4 flex items-center text-sm text-gray-500 dark:text-gray-400 group-hover:text-primary-600 dark:group-hover:text-primary-400 transition-colors">
            <span>View all</span>
            <ArrowRight className="w-4 h-4 ml-1 group-hover:translate-x-1 transition-transform" />
          </div>
        </Link>

        {/* Skills */}
        <Link to="/skills" className="card group hover:shadow-md transition-shadow">
          <div className="flex items-start justify-between">
            <div>
              <p className="text-sm font-medium text-gray-500 dark:text-gray-400">Skills</p>
              <p className="mt-2 text-3xl font-semibold text-gray-900 dark:text-white">
                {skillsCount}
              </p>
            </div>
            <div className="p-3 rounded-lg bg-green-50 dark:bg-green-900/20">
              <Bot className="w-6 h-6 text-green-600 dark:text-green-400" />
            </div>
          </div>
          <div className="mt-4 flex items-center text-sm text-gray-500 dark:text-gray-400 group-hover:text-primary-600 dark:group-hover:text-primary-400 transition-colors">
            <span>Manage</span>
            <ArrowRight className="w-4 h-4 ml-1 group-hover:translate-x-1 transition-transform" />
          </div>
        </Link>

        {/* Tool Instances */}
        <Link to="/tools" className="card group hover:shadow-md transition-shadow">
          <div className="flex items-start justify-between">
            <div>
              <p className="text-sm font-medium text-gray-500 dark:text-gray-400">Tool Instances</p>
              <p className="mt-2 text-3xl font-semibold text-gray-900 dark:text-white">
                {toolsCount}
              </p>
            </div>
            <div className="p-3 rounded-lg bg-amber-50 dark:bg-amber-900/20">
              <Wrench className="w-6 h-6 text-amber-600 dark:text-amber-400" />
            </div>
          </div>
          <div className="mt-4 flex items-center text-sm text-gray-500 dark:text-gray-400 group-hover:text-primary-600 dark:group-hover:text-primary-400 transition-colors">
            <span>Configure</span>
            <ArrowRight className="w-4 h-4 ml-1 group-hover:translate-x-1 transition-transform" />
          </div>
        </Link>

        {/* System Status */}
        <div className="card">
          <div className="flex items-start justify-between">
            <div>
              <p className="text-sm font-medium text-gray-500 dark:text-gray-400">System Status</p>
              <div className="mt-2 flex items-center gap-2">
                <span className="w-2.5 h-2.5 rounded-full bg-green-500 animate-pulse" />
                <span className="text-lg font-semibold text-green-600 dark:text-green-400">Operational</span>
              </div>
            </div>
          </div>
          <div className="mt-4 grid grid-cols-3 gap-2 pt-4 border-t border-gray-100 dark:border-gray-700">
            <div className="text-center">
              <p className="text-xl font-semibold text-primary-600 dark:text-primary-400">{runningCount}</p>
              <p className="text-xs text-gray-500 dark:text-gray-400">Running</p>
            </div>
            <div className="text-center border-x border-gray-100 dark:border-gray-700">
              <p className="text-xl font-semibold text-green-600 dark:text-green-400">{completedCount}</p>
              <p className="text-xs text-gray-500 dark:text-gray-400">Done</p>
            </div>
            <div className="text-center">
              <p className="text-xl font-semibold text-red-600 dark:text-red-400">{failedCount}</p>
              <p className="text-xs text-gray-500 dark:text-gray-400">Failed</p>
            </div>
          </div>
        </div>
      </div>

      {/* Recent Incidents */}
      <div className="card">
        <div className="flex items-center justify-between mb-6">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-white">Recent Incidents</h2>
          <Link
            to="/incidents"
            className="flex items-center gap-1 text-sm text-primary-600 dark:text-primary-400 hover:underline"
          >
            <span>View all</span>
            <ArrowRight className="w-4 h-4" />
          </Link>
        </div>

        {incidents.length === 0 ? (
          <div className="py-12 text-center border-2 border-dashed border-gray-200 dark:border-gray-700 rounded-lg">
            <Activity className="w-12 h-12 mx-auto text-gray-400 mb-3" />
            <p className="text-gray-500 dark:text-gray-400">No incidents recorded</p>
            <p className="text-sm text-gray-400 dark:text-gray-500 mt-1">Incidents will appear here when triggered</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="table">
              <thead>
                <tr>
                  <th>UUID</th>
                  <th>Source</th>
                  <th>Status</th>
                  <th>Started</th>
                  <th>Duration</th>
                </tr>
              </thead>
              <tbody>
                {incidents.map((incident) => {
                  const statusConfig = getStatusConfig(incident.status);
                  const StatusIcon = statusConfig.icon;
                  const startTime = new Date(incident.started_at);
                  const endTime = incident.completed_at ? new Date(incident.completed_at) : new Date();
                  const duration = incident.status === 'pending'
                    ? '-'
                    : `${Math.round((endTime.getTime() - startTime.getTime()) / 1000)}s`;

                  return (
                    <tr key={incident.id}>
                      <td>
                        <code className="text-xs bg-gray-100 dark:bg-gray-800 px-2 py-1 rounded">
                          {incident.uuid.slice(0, 8)}
                        </code>
                      </td>
                      <td className="text-gray-600 dark:text-gray-300 capitalize">
                        {incident.source}
                      </td>
                      <td>
                        <span className={`badge ${statusConfig.class} inline-flex items-center gap-1`}>
                          <StatusIcon className="w-3 h-3" />
                          {statusConfig.label}
                        </span>
                      </td>
                      <td className="text-gray-500 dark:text-gray-400 text-sm">
                        {startTime.toLocaleString('en-US', {
                          month: 'short',
                          day: '2-digit',
                          hour: '2-digit',
                          minute: '2-digit',
                          hour12: false
                        })}
                      </td>
                      <td className="text-gray-500 dark:text-gray-400 text-sm font-mono">
                        {duration}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Quick Actions */}
      <div className="mt-6 grid grid-cols-1 md:grid-cols-3 gap-4">
        <Link to="/skills" className="btn btn-secondary justify-center">
          <Bot className="w-4 h-4" />
          <span>Create Skill</span>
        </Link>
        <Link to="/tools" className="btn btn-secondary justify-center">
          <Wrench className="w-4 h-4" />
          <span>Add Tool</span>
        </Link>
        <Link to="/settings" className="btn btn-secondary justify-center">
          <Settings className="w-4 h-4" />
          <span>Settings</span>
        </Link>
      </div>
    </div>
  );
}
