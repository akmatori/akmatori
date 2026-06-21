import { useState } from 'react';
import { Link } from 'react-router-dom';
import {
  MessageSquare,
  Cpu,
  Bell,
  ChevronDown,
  ChevronRight,
  CheckCircle2,
  AlertTriangle,
  Globe,
  Settings2,
  Trash2,
  Sparkles,
  Hash,
} from 'lucide-react';
import AlertSourcesManager from '../components/AlertSourcesManager';
import ProxySettings from '../components/ProxySettings';
import LLMSettingsSection from '../components/settings/LLMSettingsSection';
import GeneralSettingsSection from '../components/settings/GeneralSettingsSection';
import RetentionSettingsSection from '../components/settings/RetentionSettingsSection';
import FormattingSettingsSection from '../components/settings/FormattingSettingsSection';

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

function NavigationCard({
  title,
  description,
  icon: Icon,
  to,
}: {
  title: string;
  description: string;
  icon: React.ElementType;
  to: string;
}) {
  return (
    <Link
      to={to}
      className="block border border-gray-200 dark:border-gray-700 rounded-xl p-5 hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors"
    >
      <div className="flex items-center gap-4">
        <div className="p-2.5 rounded-lg bg-gray-100 dark:bg-gray-800">
          <Icon className="w-5 h-5 text-gray-600 dark:text-gray-400" />
        </div>
        <div className="flex-1">
          <h3 className="font-semibold text-gray-900 dark:text-white">{title}</h3>
          <p className="text-sm text-gray-500 dark:text-gray-400">{description}</p>
        </div>
        <ChevronRight className="w-5 h-5 text-gray-400" />
      </div>
    </Link>
  );
}

export default function Settings() {
  const [llmStatus, setLlmStatus] = useState<'configured' | 'not-configured'>('not-configured');
  const [generalStatus, setGeneralStatus] = useState<'configured' | undefined>();
  const [retentionStatus, setRetentionStatus] = useState<'configured' | 'disabled' | undefined>();
  const [formattingStatus, setFormattingStatus] = useState<'configured' | 'disabled' | undefined>();

  return (
    <div className="animate-fade-in max-w-3xl mx-auto">
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Settings</h1>
        <p className="mt-1 text-gray-600 dark:text-gray-400">
          Configure your Akmatori instance
        </p>
      </div>

      <div className="space-y-4">
        <SettingsSection
          title="General"
          description="Instance configuration and external access"
          icon={Settings2}
          status={generalStatus}
        >
          <GeneralSettingsSection onStatusChange={setGeneralStatus} />
        </SettingsSection>

        <SettingsSection
          title="AI Configuration"
          description="LLM provider settings for incident analysis"
          icon={Cpu}
          status={llmStatus}
          defaultExpanded={llmStatus === 'not-configured'}
        >
          <LLMSettingsSection onStatusChange={setLlmStatus} />
        </SettingsSection>

        <NavigationCard
          title="Integrations"
          description="Slack and other messaging providers (Telegram coming soon)"
          icon={MessageSquare}
          to="/settings/integrations"
        />

        <NavigationCard
          title="Channels"
          description="Addressable destinations within your integrations"
          icon={Hash}
          to="/settings/channels"
        />

        <SettingsSection
          title="Proxy"
          description="HTTP proxy configuration for outbound connections"
          icon={Globe}
          defaultExpanded={false}
        >
          <ProxySettings />
        </SettingsSection>

        <SettingsSection
          title="Data Retention"
          description="Automatic cleanup of old incident data"
          icon={Trash2}
          status={retentionStatus}
        >
          <RetentionSettingsSection onStatusChange={setRetentionStatus} />
        </SettingsSection>

        <SettingsSection
          title="Response Formatting"
          description="Reformat agent responses with an extra LLM pass before storing"
          icon={Sparkles}
          status={formattingStatus}
        >
          <FormattingSettingsSection onStatusChange={setFormattingStatus} />
        </SettingsSection>

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
