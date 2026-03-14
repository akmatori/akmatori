import { useState } from 'react';
import { MessageSquare, Cpu, Bell, ChevronDown, ChevronRight, CheckCircle2, AlertTriangle, Globe, Layers, Settings2 } from 'lucide-react';
import AlertSourcesManager from '../components/AlertSourcesManager';
import ProxySettings from '../components/ProxySettings';
import AggregationSettings from '../components/AggregationSettings';
import LLMSettingsSection from '../components/settings/LLMSettingsSection';
import SlackSettingsSection from '../components/settings/SlackSettingsSection';
import GeneralSettingsSection from '../components/settings/GeneralSettingsSection';

// Collapsible Section Component
function SettingsSection({
  title,
  description,
  icon: Icon,
  status,
  children,
  defaultExpanded = false,
}: {
  title: string;
  description: string;
  icon: React.ElementType;
  status?: 'configured' | 'not-configured' | 'disabled';
  children: React.ReactNode;
  defaultExpanded?: boolean;
}) {
  const [expanded, setExpanded] = useState(defaultExpanded);

  return (
    <div className="border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center justify-between p-5 hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors text-left"
      >
        <div className="flex items-center gap-4">
          <div className="p-2.5 rounded-lg bg-gray-100 dark:bg-gray-800">
            <Icon className="w-5 h-5 text-gray-600 dark:text-gray-400" />
          </div>
          <div>
            <h3 className="font-semibold text-gray-900 dark:text-white">{title}</h3>
            <p className="text-sm text-gray-500 dark:text-gray-400">{description}</p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          {status === 'configured' && (
            <span className="flex items-center gap-1.5 text-sm text-green-600 dark:text-green-400">
              <CheckCircle2 className="w-4 h-4" />
              Configured
            </span>
          )}
          {status === 'not-configured' && (
            <span className="flex items-center gap-1.5 text-sm text-amber-600 dark:text-amber-400">
              <AlertTriangle className="w-4 h-4" />
              Setup required
            </span>
          )}
          {status === 'disabled' && (
            <span className="text-sm text-gray-400 dark:text-gray-500">Disabled</span>
          )}
          {expanded ? (
            <ChevronDown className="w-5 h-5 text-gray-400" />
          ) : (
            <ChevronRight className="w-5 h-5 text-gray-400" />
          )}
        </div>
      </button>
      {expanded && (
        <div className="border-t border-gray-200 dark:border-gray-700 p-6 bg-gray-50/50 dark:bg-gray-900/30">
          {children}
        </div>
      )}
    </div>
  );
}

export default function Settings() {
  const [llmStatus, setLlmStatus] = useState<'configured' | 'not-configured'>('not-configured');
  const [slackStatus, setSlackStatus] = useState<'configured' | 'not-configured' | 'disabled' | undefined>();
  const [generalStatus, setGeneralStatus] = useState<'configured' | undefined>();

  return (
    <div className="animate-fade-in max-w-3xl mx-auto">
      {/* Page Header */}
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Settings</h1>
        <p className="mt-1 text-gray-600 dark:text-gray-400">
          Configure your Akmatori instance
        </p>
      </div>

      {/* Settings Sections */}
      <div className="space-y-4">
        {/* General Settings */}
        <SettingsSection
          title="General"
          description="Instance configuration and external access"
          icon={Settings2}
          status={generalStatus}
        >
          <GeneralSettingsSection onStatusChange={setGeneralStatus} />
        </SettingsSection>

        {/* LLM Provider Section */}
        <SettingsSection
          title="AI Configuration"
          description="LLM provider settings for incident analysis"
          icon={Cpu}
          status={llmStatus}
          defaultExpanded={llmStatus === 'not-configured'}
        >
          <LLMSettingsSection onStatusChange={setLlmStatus} />
        </SettingsSection>

        {/* Slack Section */}
        <SettingsSection
          title="Slack Integration"
          description="Receive alerts and interact via Slack"
          icon={MessageSquare}
          status={slackStatus}
        >
          <SlackSettingsSection onStatusChange={setSlackStatus} />
        </SettingsSection>

        {/* Proxy Settings */}
        <SettingsSection
          title="Proxy"
          description="HTTP proxy configuration for outbound connections"
          icon={Globe}
          defaultExpanded={false}
        >
          <ProxySettings />
        </SettingsSection>

        {/* Alert Aggregation Settings */}
        <SettingsSection
          title="Alert Aggregation"
          description="Automatically group related alerts into incidents"
          icon={Layers}
          defaultExpanded={false}
        >
          <AggregationSettings />
        </SettingsSection>

        {/* Alert Sources Section */}
        <SettingsSection
          title="Alert Sources"
          description="Webhook integrations for monitoring systems"
          icon={Bell}
        >
          <AlertSourcesManager />
        </SettingsSection>
      </div>
    </div>
  );
}
