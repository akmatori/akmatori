import type { SSHHostConfig } from '../../types';

// ToolSchemaProperty describes one field of a tool type's settings schema as
// served by the MCP gateway (`GET /mcp/tools`).
export interface ToolSchemaProperty {
  type?: string;
  description?: string;
  secret?: boolean;
  advanced?: boolean;
  warning?: string;
  format?: string;
  example?: string;
  enum?: string[];
  default?: unknown;
}

export interface ToolSettingsSchema {
  type: string;
  required?: string[];
  properties: Record<string, ToolSchemaProperty>;
}

export interface ToolSchema {
  name: string;
  description: string;
  version: string;
  settings_schema: ToolSettingsSchema;
  functions: Array<{
    name: string;
    description: string;
    parameters?: string;
    returns?: string;
  }>;
}

// ToolSettings is the free-form settings object; only ssh_hosts has a shape
// the UI needs to know about.
export interface ToolSettings {
  [key: string]: unknown;
  ssh_hosts?: SSHHostConfig[];
}

export interface ToolFormData {
  tool_type_id: number;
  name: string;
  logical_name?: string;
  settings: ToolSettings;
  enabled: boolean;
}

export const EMPTY_TOOL_FORM: ToolFormData = {
  tool_type_id: 0,
  name: '',
  settings: {},
  enabled: true,
};

// inputValue narrows an unknown setting to something an <input> can display.
export function inputValue(v: unknown): string | number {
  return typeof v === 'string' || typeof v === 'number' ? v : '';
}
