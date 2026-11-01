import { Wrench, MessageSquare, Hash, Bell } from 'lucide-react';
import SectionedPage, { type PageSectionDef } from '../components/shared/SectionedPage';
import ToolsSection from '../components/connectors/ToolsSection';
import IntegrationsManager from '../components/settings/IntegrationsManager';
import ChannelsManager from '../components/channels/ChannelsManager';
import AlertSourcesManager from '../components/AlertSourcesManager';

// Connectors are the agent's connections to other systems: what it can query
// (tools), where it talks (messaging and channels) and where alerts come from.
// Each section has its own URL (/connectors/<id>); the old /tools and
// /settings/{integrations,channels,alert-sources} URLs redirect here.
const SECTIONS: PageSectionDef[] = [
  {
    id: 'tools',
    title: 'Tools',
    description: 'Systems the agent can query and act on: monitoring, Kubernetes, SSH, databases.',
    icon: Wrench,
    render: () => <ToolsSection />,
  },
  {
    id: 'messaging',
    title: 'Messaging',
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
];

export default function Connectors() {
  return <SectionedPage basePath="/connectors" label="Connectors" sections={SECTIONS} />;
}
