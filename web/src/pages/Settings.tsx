import { Navigate, NavLink, useParams } from 'react-router-dom';
import {
  MessageSquare,
  Cpu,
  Bell,
  Globe,
  Settings2,
  Trash2,
  Sparkles,
  Hash,
  type LucideIcon,
} from 'lucide-react';
import AlertSourcesManager from '../components/AlertSourcesManager';
import ProxySettings from '../components/ProxySettings';
import LLMSettingsSection from '../components/settings/LLMSettingsSection';
import GeneralSettingsSection from '../components/settings/GeneralSettingsSection';
import RetentionSettingsSection from '../components/settings/RetentionSettingsSection';
import FormattingRulesSection from '../components/settings/FormattingRulesSection';
import IntegrationsManager from '../components/settings/IntegrationsManager';
import ChannelsManager from '../components/channels/ChannelsManager';

interface SettingsSectionDef {
  id: string;
  title: string;
  description: string;
  icon: LucideIcon;
  render: () => React.ReactNode;
}

// Each section has its own URL (/settings/<id>) so a section can be linked
// to directly, e.g. from the top-bar "LLM not configured" status.
const SECTIONS: SettingsSectionDef[] = [
  {
    id: 'general',
    title: 'General',
    description: 'Instance name and the external URL used in links.',
    icon: Settings2,
    render: () => <GeneralSettingsSection />,
  },
  {
    id: 'ai',
    title: 'AI models',
    description: 'The LLM provider and model the agent uses to investigate.',
    icon: Cpu,
    render: () => <LLMSettingsSection />,
  },
  {
    id: 'integrations',
    title: 'Integrations',
    description: 'Messaging providers such as Slack.',
    icon: MessageSquare,
    render: () => <IntegrationsManager />,
  },
  {
    id: 'channels',
    title: 'Channels',
    description: 'Where alert notifications go and which channels the agent listens to.',
    icon: Hash,
    render: () => <ChannelsManager />,
  },
  {
    id: 'alert-sources',
    title: 'Alert sources',
    description: 'Webhooks that receive alerts from your monitoring systems.',
    icon: Bell,
    render: () => <AlertSourcesManager />,
  },
  {
    id: 'formatting',
    title: 'Response formatting',
    description: 'Rules that reshape agent responses before they are delivered.',
    icon: Sparkles,
    render: () => <FormattingRulesSection />,
  },
  {
    id: 'retention',
    title: 'Data retention',
    description: 'How long incident data is kept before automatic cleanup.',
    icon: Trash2,
    render: () => <RetentionSettingsSection />,
  },
  {
    id: 'proxy',
    title: 'Proxy',
    description: 'HTTP proxy for outbound connections.',
    icon: Globe,
    render: () => <ProxySettings />,
  },
];

export default function Settings() {
  const { section } = useParams<{ section?: string }>();
  const active = SECTIONS.find((s) => s.id === section);
  if (!active) return <Navigate to="/settings/general" replace />;

  return (
    <div className="flex flex-col md:flex-row gap-6 md:gap-10">
      <nav aria-label="Settings" className="md:w-52 flex-shrink-0">
        <ul className="flex md:flex-col gap-0.5 overflow-x-auto md:overflow-visible -mx-1 px-1 pb-1 md:pb-0">
          {SECTIONS.map(({ id, title, icon: Icon }) => (
            <li key={id} className="flex-shrink-0">
              <NavLink
                to={`/settings/${id}`}
                className={({ isActive }) =>
                  `flex items-center gap-2.5 h-9 px-2.5 rounded-md text-sm whitespace-nowrap transition-colors ${
                    isActive ? 'bg-surface-2 text-fg' : 'text-fg-muted hover:bg-surface-2 hover:text-fg'
                  }`
                }
              >
                <Icon size={15} aria-hidden="true" />
                {title}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>

      <section aria-labelledby="settings-title" className="flex-1 min-w-0 max-w-4xl">
        <header className="mb-6">
          <h1 id="settings-title" className="text-xl font-semibold text-fg">{active.title}</h1>
          <p className="mt-1 text-fg-muted">{active.description}</p>
        </header>
        {active.render()}
      </section>
    </div>
  );
}
