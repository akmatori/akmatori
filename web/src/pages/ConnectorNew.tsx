import { useEffect, useMemo, useState } from 'react';
import { Link, Navigate, useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft } from 'lucide-react';
import { connectorsApi } from '../api/connectors';
import { getErrorMessage } from '../utils/errors';
import type { ConnectorCatalogEntry, ConnectorPartKind, ConnectorSummary } from '../types/connectors';
import type { MessagingProvider } from '../types';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorMessage from '../components/ErrorMessage';
import ConnectorTile from '../components/connectors/ConnectorTile';
import ToolsSection from '../components/connectors/ToolsSection';
import AlertSourcesManager from '../components/AlertSourcesManager';
import { NewIntegrationForm } from '../components/connectors/IntegrationPanel';
import type { PartScope } from '../components/connectors/partScope';

// ConnectorNew adds a connector from the catalog: it creates the first part
// (a tool instance, an alert webhook or a messaging integration), which
// creates the connector, then opens the connector page.
export default function ConnectorNew() {
  const { catalogId = '' } = useParams<{ catalogId: string }>();
  const navigate = useNavigate();
  const [entry, setEntry] = useState<ConnectorCatalogEntry | null | undefined>(undefined);
  const [existing, setExisting] = useState<ConnectorSummary[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    Promise.all([connectorsApi.catalog(), connectorsApi.list()])
      .then(([cat, list]) => {
        if (cancelled) return;
        setEntry(cat.find((e) => e.id === catalogId) ?? null);
        setExisting(list.filter((c) => c.catalog_id === catalogId));
      })
      .catch((err) => !cancelled && setError(getErrorMessage(err, 'Failed to load the catalog')));
    return () => {
      cancelled = true;
    };
  }, [catalogId]);

  const openCreated = useMemo(
    () => (kind: ConnectorPartKind) => async (id: number) => {
      try {
        const { connector_uuid } = await connectorsApi.lookup(kind, id);
        navigate(`/connectors/${connector_uuid}`, { replace: true });
      } catch {
        navigate('/connectors', { replace: true });
      }
    },
    [navigate],
  );

  if (error) return <ErrorMessage message={error} />;
  if (entry === undefined) return <LoadingSpinner />;
  if (entry === null) return <ErrorMessage message={`Unknown connector type "${catalogId}".`} />;
  if (entry.kind === 'custom_mcp') return <Navigate to="/connectors?add=mcp" replace />;

  // One integration per messaging provider: open the existing one.
  if (entry.messaging_provider && existing.length > 0) {
    return <Navigate to={`/connectors/${existing[0].uuid}`} replace />;
  }

  const back = () => navigate('/connectors?tab=discover');
  const baseScope = { connectorId: -1, connectorUUID: '', startCreating: true, onCancel: back };
  const hasTools = (entry.tool_types ?? []).length > 0;
  const toolScope: PartScope = { ...baseScope, allowedTypes: entry.tool_types ?? [], onCreated: openCreated('tool_instance') };
  const alertScope: PartScope = { ...baseScope, allowedTypes: entry.alert_source_types ?? [], onCreated: openCreated('alert_source') };

  return (
    <div className="space-y-5">
      <Link to="/connectors?tab=discover" className="text-sm text-fg-muted hover:text-fg inline-flex items-center gap-1">
        <ArrowLeft className="w-4 h-4" />Discover
      </Link>
      <header className="flex items-start gap-4">
        <ConnectorTile name={entry.name} kind={entry.kind} />
        <div>
          <h1 className="text-xl font-semibold text-fg">Add {entry.name}</h1>
          <p className="text-sm text-fg-muted mt-1">{entry.description}</p>
          {existing.length > 0 && (
            <p className="text-xs text-fg-subtle mt-1">
              You already have {existing.length} {entry.name} {existing.length === 1 ? 'connector' : 'connectors'}. This adds another one
              (for example a second environment).
            </p>
          )}
        </div>
      </header>

      {entry.messaging_provider ? (
        <NewIntegrationForm
          provider={entry.messaging_provider as MessagingProvider}
          onCreated={(row) => void openCreated('integration')(row.id)}
        />
      ) : hasTools ? (
        <>
          {(entry.alert_source_types ?? []).length > 0 && (
            <p className="text-sm text-fg-muted">Set up the API access first. You can add the alert webhook on the connector’s Alerts tab.</p>
          )}
          <ToolsSection scope={toolScope} />
        </>
      ) : (
        <AlertSourcesManager scope={alertScope} />
      )}
    </div>
  );
}
