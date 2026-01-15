import { useState, useEffect } from 'react';
import { Save, Server, MessageSquare, Shield, Terminal } from 'lucide-react';
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
        ? 'border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800/50 opacity-60'
        : 'border-gray-200 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800/50'
    } ${!isDisabled ? 'cursor-pointer' : 'cursor-not-allowed'}`}>
      <div className="flex items-center gap-3">
        <div className={`p-2 rounded-lg ${isDisabled ? 'bg-gray-200 dark:bg-gray-700' : 'bg-gray-100 dark:bg-gray-800'}`}>
          <Icon className={`w-4 h-4 ${isDisabled ? 'text-gray-400' : 'text-gray-600 dark:text-gray-400'}`} />
        </div>
        <div>
          <div className={`font-medium ${isDisabled ? 'text-gray-400 dark:text-gray-500' : 'text-gray-900 dark:text-white'}`}>
            {name}
          </div>
          <div className="text-sm text-gray-500 dark:text-gray-400">
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
  const [openaiEnabled, setOpenaiEnabled] = useState(true);
  const [slackEnabled, setSlackEnabled] = useState(true);
  const [zabbixEnabled, setZabbixEnabled] = useState(false);

  useEffect(() => {
    loadSettings();
  }, []);

  const loadSettings = async () => {
    try {
      setLoading(true);
      const data = await proxySettingsApi.get();
      setProxyUrl(data.proxy_url || '');
      setNoProxy(data.no_proxy || '');
      setOpenaiEnabled(data.services.openai.enabled);
      setSlackEnabled(data.services.slack.enabled);
      setZabbixEnabled(data.services.zabbix.enabled);
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
          openai: { enabled: openaiEnabled },
          slack: { enabled: slackEnabled },
          zabbix: { enabled: zabbixEnabled },
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
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
          Proxy URL
        </label>
        <input
          type="text"
          value={proxyUrl}
          onChange={(e) => setProxyUrl(e.target.value)}
          placeholder="http://proxy:8080"
          className="w-full px-4 py-2.5 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent"
        />
        <p className="mt-1.5 text-sm text-gray-500 dark:text-gray-400">
          HTTP or HTTPS proxy for outbound connections
        </p>
      </div>

      {/* No Proxy */}
      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
          No Proxy
        </label>
        <input
          type="text"
          value={noProxy}
          onChange={(e) => setNoProxy(e.target.value)}
          placeholder="localhost,127.0.0.1,*.internal"
          className="w-full px-4 py-2.5 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent"
        />
        <p className="mt-1.5 text-sm text-gray-500 dark:text-gray-400">
          Comma-separated hosts that bypass the proxy
        </p>
      </div>

      {/* Services */}
      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">
          Services
        </label>
        {!hasProxy && (
          <p className="mb-3 text-sm text-amber-600 dark:text-amber-400">
            Configure a proxy URL to enable service toggles
          </p>
        )}
        <div className="space-y-2">
          <ServiceToggle
            name="OpenAI API"
            description="External AI service"
            icon={Server}
            enabled={openaiEnabled}
            supported={true}
            disabled={!hasProxy}
            onChange={setOpenaiEnabled}
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
