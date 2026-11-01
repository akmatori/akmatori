import { Server } from 'lucide-react';
import type { ConnectorKind } from '../../types/connectors';
import { monogram } from './connectorHelpers';

// ConnectorTile is the square icon of a connector: a monogram for
// built-in connectors, a server icon for custom ones.
export default function ConnectorTile({ name, kind }: { name: string; kind: ConnectorKind }) {
  return (
    <div
      aria-hidden="true"
      className="w-9 h-9 flex-shrink-0 rounded-md border border-line bg-surface-2 flex items-center justify-center text-xs font-semibold text-fg-muted"
    >
      {kind === 'builtin' ? monogram(name) : <Server className="w-4 h-4" />}
    </div>
  );
}
