import type { AlertSourceInstance, AlertSourceType, ToolInstance, ToolType } from '../../types';

// PartScope limits a tools or alert-sources editor to one connector: it
// shows only that connector's parts, offers only the types the connector
// accepts, and links new parts to it.
export interface PartScope {
  connectorId: number;
  connectorUUID: string;
  allowedTypes: string[];
  // Called after a create, update or delete so the page can refresh.
  onChange?: () => void;
  // Called with the new part's id after a create.
  onCreated?: (id: number) => void;
  // Open the "new" form right away (add-from-catalog page).
  startCreating?: boolean;
}

// Credential-less built-in tools are part of Akmatori itself, not connectors.
export const HIDDEN_TOOL_TYPES = new Set(['incidents', 'proposals', 'events']);

export function isManagedToolType(t: Pick<ToolType, 'kind'> | undefined): boolean {
  return t?.kind === 'mcp';
}

export function scopedTools(tools: ToolInstance[], scope?: PartScope): ToolInstance[] {
  return tools.filter((t) => {
    if (isManagedToolType(t.tool_type)) return false;
    if (!scope) return true;
    return t.connector_id === scope.connectorId;
  });
}

export function creatableToolTypes(types: ToolType[], scope?: PartScope): ToolType[] {
  return types.filter((t) => {
    if (isManagedToolType(t) || HIDDEN_TOOL_TYPES.has(t.name)) return false;
    if (!scope) return true;
    return scope.allowedTypes.includes(t.name);
  });
}

export function scopedAlertSources(sources: AlertSourceInstance[], scope?: PartScope): AlertSourceInstance[] {
  if (!scope) return sources;
  return sources.filter((s) => s.connector_id === scope.connectorId);
}

export function creatableAlertTypes(types: AlertSourceType[], scope?: PartScope): AlertSourceType[] {
  if (!scope) return types;
  return types.filter((t) => scope.allowedTypes.includes(t.name));
}
