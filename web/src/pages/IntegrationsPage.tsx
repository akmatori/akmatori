import PageHeader from '../components/PageHeader';
import IntegrationsManager from '../components/settings/IntegrationsManager';

export default function IntegrationsPage() {
  return (
    <div className="animate-fade-in max-w-4xl mx-auto">
      <PageHeader
        title="Integrations"
        description="Connect Akmatori to messaging providers like Slack and Telegram."
      />
      <IntegrationsManager />
    </div>
  );
}
