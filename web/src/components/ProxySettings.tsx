import { useState, useEffect } from 'react';
import { Save, Server, MessageSquare, Shield, Terminal, BarChart3, Activity, LayoutDashboard, Bell, Box, Network, Ticket } from 'lucide-react';
import LoadingSpinner from './LoadingSpinner';
import ErrorMessage, { SuccessMessage } from './ErrorMessage';
import { proxySettingsApi } from '../api/client';
import type { ProxySettingsUpdate } from '../types';

interface ServiceToggleProps {
  name: string;
  description: string;
  icon: React.ElementType;
  enabled: boolean;
  supported: boolean;
  disabled: boolean;
  onChange: (enabled: boolean) => void;
}

function ServiceToggle({ name, description, icon: Icon, enabled, supported, disabled, onChange }: ServiceToggleProps) {
  const isDisabled = disabled || !supported;

  return (
    <label className={`flex items-center justify-between p-3 rounded-lg border ${
      isDisabled
        ? 'border-line bg-gray-50 dark:bg-gray-800/50 opacity-60'
        : 'border-line hover:bg-surface-2'
    } ${!isDisabled ? 'cursor-pointer' : 'cursor-not-allowed'}`}>
      <div className="flex items-center gap-3">
        <div className={`p-2 rounded-lg ${isDisabled ? 'bg-gray-200 dark:bg-gray-700' : 'bg-surface-2'}`}>
          <Icon className={`w-4 h-4 ${isDisabled ? 'text-gray-400' : 'text-fg-muted'}`} />
        </div>
        <div>
          <div className={`font-medium ${isDisabled ? 'text-fg-subtle' : 'text-fg'}`}>
            {name}
          </div>
          <div className="text-sm text-fg-subtle">
            {!supported ? 'Does not support HTTP proxy' : description}
          </div>
        </div>
      </div>
      <input
        type="checkbox"
        checked={enabled && supported}
        onChange={(e) => onChange(e.target.checked)}
        disabled={isDisabled}
        className="w-5 h-5 rounded border-gray-300 text-blue-600 focus:ring-blue-500 disabled:opacity-50"
      />
    </label>
  );
}

export default function ProxySettings() {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  const [proxyUrl, setProxyUrl] = useState('');
  const [noProxy, setNoProxy] = useState('');
  const [llmEnabled, setLlmEnabled] = useState(true);
  const [slackEnabled, setSlackEnabled] = useState(true);
  const [zabbixEnabled, setZabbixEnabled] = useState(false);
  const [victoriaMetricsEnabled, setVictoriaMetricsEnabled] = useState(false);
  const [catchpointEnabled, setCatchpointEnabled] = useState(false);
  const [grafanaEnabled, setGrafanaEnabled] = useState(false);
  const [pagerdutyEnabled, setPagerdutyEnabled] = useState(false);
  const [netboxEnabled, setNetboxEnabled] = useState(false);
  const [kubernetesEnabled, setKubernetesEnabled] = useState(false);
  const [jiraEnabled, setJiraEnabled] = useState(false);

  useEffect(() => {
    loadSettings();
  }, []);

  const loadSettings = async () => {
    try {
      setLoading(true);
      const data = await proxySettingsApi.get();
      setProxyUrl(data.proxy_url || '');
      setNoProxy(data.no_proxy || '');
      setLlmEnabled(data.services.llm.enabled);
      setSlackEnabled(data.services.slack.enabled);
      setZabbixEnabled(data.services.zabbix.enabled);
      setVictoriaMetricsEnabled(data.services.victoria_metrics.enabled);
      setCatchpointEnabled(data.services.catchpoint?.enabled ?? false);
      setGrafanaEnabled(data.services.grafana?.enabled ?? false);
      setPagerdutyEnabled(data.services.pagerduty?.enabled ?? false);
      setNetboxEnabled(data.services.netbox?.enabled ?? false);
      setKubernetesEnabled(data.services.kubernetes?.enabled ?? false);
      setJiraEnabled(data.services.jira?.enabled ?? false);
      setError(null);
    } catch (err) {
      setError('Failed to load proxy settings');
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    try {
      setSaving(true);
      setError(null);

      const update: ProxySettingsUpdate = {
        proxy_url: proxyUrl,
        no_proxy: noProxy,
        services: {
          llm: { enabled: llmEnabled },
          slack: { enabled: slackEnabled },
          zabbix: { enabled: zabbixEnabled },
          victoria_metrics: { enabled: victoriaMetricsEnabled },
          catchpoint: { enabled: catchpointEnabled },
          grafana: { enabled: grafanaEnabled },
          pagerduty: { enabled: pagerdutyEnabled },
          netbox: { enabled: netboxEnabled },
          kubernetes: { enabled: kubernetesEnabled },
          jira: { enabled: jiraEnabled },
        },
      };

      await proxySettingsApi.update(update);
      setSuccess(true);
      setTimeout(() => setSuccess(false), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save proxy settings');
    } finally {
      setSaving(false);
    }
  };

  const hasProxy = proxyUrl.trim() !== '';

  if (loading) {
    return <LoadingSpinner />;
  }

  return (
    <div className="space-y-6">
      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message="Proxy settings saved successfully" />}

      {/* Proxy URL */}
      <div>
        <label className="block text-sm font-medium text-fg mb-2">
          Proxy URL
        </label>
        <input
          type="text"
          value={proxyUrl}
          onChange={(e) => setProxyUrl(e.target.value)}
          placeholder="http://proxy:8080"
          className="w-full px-4 py-2.5 rounded-lg border border-line bg-surface text-fg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
        />
        <p className="mt-1.5 text-sm text-fg-subtle">
          HTTP or HTTPS proxy for outbound connections
        </p>
      </div>

      {/* No Proxy */}
      <div>
        <label className="block text-sm font-medium text-fg mb-2">
          No Proxy
        </label>
        <input
          type="text"
          value={noProxy}
          onChange={(e) => setNoProxy(e.target.value)}
          placeholder="localhost,127.0.0.1,*.internal"
          className="w-full px-4 py-2.5 rounded-lg border border-line bg-surface text-fg focus:ring-2 focus:ring-blue-500 focus:border-transparent"
        />
        <p className="mt-1.5 text-sm text-fg-subtle">
          Comma-separated hosts that bypass the proxy
        </p>
      </div>

      {/* Services */}
      <div>
        <label className="block text-sm font-medium text-fg mb-3">
          Services
        </label>
        {!hasProxy && (
          <p className="mb-3 text-sm text-state-need">
            Configure a proxy URL to enable service toggles
          </p>
        )}
        <div className="space-y-2">
          <ServiceToggle
            name="LLM API"
            description="External AI service"
            icon={Server}
            enabled={llmEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setLlmEnabled}
          />
          <ServiceToggle
            name="Slack"
            description="Messaging integration"
            icon={MessageSquare}
            enabled={slackEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setSlackEnabled}
          />
          <ServiceToggle
            name="Zabbix API"
            description="Monitoring system"
            icon={Shield}
            enabled={zabbixEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setZabbixEnabled}
          />
          <ServiceToggle
            name="VictoriaMetrics"
            description="Time-series database"
            icon={BarChart3}
            enabled={victoriaMetricsEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setVictoriaMetricsEnabled}
          />
          <ServiceToggle
            name="Catchpoint"
            description="Digital experience monitoring"
            icon={Activity}
            enabled={catchpointEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setCatchpointEnabled}
          />
          <ServiceToggle
            name="Grafana"
            description="Observability dashboards"
            icon={LayoutDashboard}
            enabled={grafanaEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setGrafanaEnabled}
          />
          <ServiceToggle
            name="PagerDuty"
            description="Incident management"
            icon={Bell}
            enabled={pagerdutyEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setPagerdutyEnabled}
          />
          <ServiceToggle
            name="NetBox"
            description="CMDB / DCIM / IPAM"
            icon={Box}
            enabled={netboxEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setNetboxEnabled}
          />
          <ServiceToggle
            name="Kubernetes"
            description="Container orchestration"
            icon={Network}
            enabled={kubernetesEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setKubernetesEnabled}
          />
          <ServiceToggle
            name="Jira"
            description="Issue tracking"
            icon={Ticket}
            enabled={jiraEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setJiraEnabled}
          />
          <ServiceToggle
            name="SSH"
            description="Remote server access"
            icon={Terminal}
            enabled={false}
            supported={false}
            disabled={true}
            onChange={() => {}}
          />
        </div>
      </div>

      {/* Save Button */}
      <div className="flex justify-end pt-4">
        <button
          onClick={handleSave}
          disabled={saving}
          className="flex items-center gap-2 px-6 py-2.5 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-400 text-white font-medium rounded-lg transition-colors"
        >
          {saving ? (
            <div className="w-5 h-5 border-2 border-white border-t-transparent rounded-full animate-spin" />
          ) : (
            <Save className="w-5 h-5" />
          )}
          Save Proxy Settings
        </button>
      </div>
    </div>
  );
}
