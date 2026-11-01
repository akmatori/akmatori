import { Cpu, Globe, Settings2, Trash2, Sparkles, Brain } from 'lucide-react';
import SectionedPage, { type PageSectionDef } from '../components/shared/SectionedPage';
import ProxySettings from '../components/ProxySettings';
import LLMSettingsSection from '../components/settings/LLMSettingsSection';
import GeneralSettingsSection from '../components/settings/GeneralSettingsSection';
import RetentionSettingsSection from '../components/settings/RetentionSettingsSection';
import FormattingRulesSection from '../components/settings/FormattingRulesSection';
import MemorySettingsSection from '../components/memory/MemorySettingsSection';

// Each section has its own URL (/settings/<id>) so a section can be linked
// to directly, e.g. from the top-bar "LLM not configured" status.
const SECTIONS: PageSectionDef[] = [
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
    id: 'memory',
    title: 'Memory',
    description: 'What the agent remembers across incidents. Edit or delete it, or tell it something new.',
    icon: Brain,
    render: () => <MemorySettingsSection />,
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
  return <SectionedPage basePath="/settings" label="Settings" sections={SECTIONS} />;
}
