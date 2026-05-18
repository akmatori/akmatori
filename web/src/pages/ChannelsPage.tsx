import PageHeader from '../components/PageHeader';
import ChannelsManager from '../components/channels/ChannelsManager';

export default function ChannelsPage() {
  return (
    <div className="animate-fade-in max-w-5xl mx-auto">
      <PageHeader
        title="Channels"
        description="Addressable destinations within your integrations — used for routing alert notifications and listening for inbound messages."
      />
      <ChannelsManager />
    </div>
  );
}
