/**
 * MCP Gateway tool definitions for the pi-mono agent.
 *
 * Creates pi-mono `ToolDefinition` objects that proxy tool calls through
 * the MCP Gateway via JSON-RPC 2.0. Each tool maps to a registered MCP
 * Gateway tool (SSH or Zabbix).
 *
 * Usage:
 *   const tools = createMCPTools("http://mcp-gateway:8080", "incident-123");
 *   // Pass `tools` to pi-mono session via `customTools` or `registerTool()`
 */

import { Type, type TObject } from "@sinclair/typebox";
import type {
  ToolDefinition,
  AgentToolResult,
  ExtensionContext,
  AgentToolUpdateCallback,
} from "@mariozechner/pi-coding-agent";
import { MCPClient } from "./mcp-client.js";

/**
 * Helper: build a ToolDefinition that proxies to the MCP Gateway.
 */
function mcpTool<T extends TObject>(opts: {
  name: string;
  label: string;
  description: string;
  parameters: T;
  mcpToolName: string;
  client: MCPClient;
}): ToolDefinition<T> {
  return {
    name: opts.name,
    label: opts.label,
    description: opts.description,
    parameters: opts.parameters,
    async execute(
      _toolCallId: string,
      params: Record<string, unknown>,
      signal: AbortSignal | undefined,
      _onUpdate: AgentToolUpdateCallback | undefined,
      _ctx: ExtensionContext,
    ): Promise<AgentToolResult<undefined>> {
      const result = await opts.client.callTool(opts.mcpToolName, params);
      return {
        content: [{ type: "text" as const, text: result }],
        details: undefined,
      };
    },
  };
}

/**
 * Create all MCP Gateway tool definitions for a given incident.
 *
 * @param gatewayUrl - MCP Gateway base URL (e.g. "http://mcp-gateway:8080")
 * @param incidentId - Incident ID for credential resolution
 * @returns Array of pi-mono ToolDefinition objects
 */
export function createMCPTools(
  gatewayUrl: string,
  incidentId: string,
): ToolDefinition[] {
  const client = new MCPClient({ gatewayUrl, incidentId });

  return [
    // --- SSH Tools ---

    mcpTool({
      name: "ssh_execute_command",
      label: "SSH Execute",
      description:
        "Execute a shell command on configured SSH servers in parallel. Returns stdout, stderr, exit code, and duration for each server.",
      parameters: Type.Object({
        command: Type.String({ description: "The shell command to execute" }),
        servers: Type.Optional(
          Type.Array(Type.String(), {
            description:
              "List of servers to target (defaults to all configured servers)",
          }),
        ),
      }),
      mcpToolName: "ssh.execute_command",
      client,
    }),

    mcpTool({
      name: "ssh_test_connectivity",
      label: "SSH Test",
      description:
        "Test SSH connectivity to all configured servers. Returns reachability status for each server.",
      parameters: Type.Object({}),
      mcpToolName: "ssh.test_connectivity",
      client,
    }),

    mcpTool({
      name: "ssh_get_server_info",
      label: "SSH Info",
      description:
        "Get basic system information (hostname, OS, uptime) from configured SSH servers.",
      parameters: Type.Object({
        servers: Type.Optional(
          Type.Array(Type.String(), {
            description:
              "List of servers to query (defaults to all configured servers)",
          }),
        ),
      }),
      mcpToolName: "ssh.get_server_info",
      client,
    }),

    // --- Zabbix Tools ---

    mcpTool({
      name: "zabbix_get_hosts",
      label: "Zabbix Hosts",
      description:
        "Get hosts from Zabbix monitoring system. Supports filtering and searching by host name, group, or other criteria.",
      parameters: Type.Object({
        search: Type.Optional(
          Type.Record(Type.String(), Type.String(), {
            description:
              'Substring/prefix search (e.g. {"name": "web-server"})',
          }),
        ),
        filter: Type.Optional(
          Type.Record(
            Type.String(),
            Type.Union([Type.String(), Type.Array(Type.String())]),
            {
              description:
                'Exact-match filter (e.g. {"host": ["server1", "server2"]})',
            },
          ),
        ),
        limit: Type.Optional(
          Type.Number({ description: "Maximum number of hosts to return" }),
        ),
      }),
      mcpToolName: "zabbix.get_hosts",
      client,
    }),

    mcpTool({
      name: "zabbix_get_problems",
      label: "Zabbix Problems",
      description:
        "Get current problems/alerts from Zabbix. Returns active problems with severity, host, and trigger information.",
      parameters: Type.Object({
        recent: Type.Optional(
          Type.Boolean({
            description: "Only return recent problems (default: true)",
          }),
        ),
        severity_min: Type.Optional(
          Type.Number({
            description: "Minimum severity level 0-5 (default: 0)",
          }),
        ),
      }),
      mcpToolName: "zabbix.get_problems",
      client,
    }),

    mcpTool({
      name: "zabbix_get_history",
      label: "Zabbix History",
      description:
        "Get metric history data from Zabbix for specific items. Returns timestamped values.",
      parameters: Type.Object({
        itemids: Type.Array(Type.Number(), {
          description: "Item IDs to get history for",
        }),
        history_type: Type.Optional(
          Type.Number({
            description:
              "History type: 0=float, 1=string, 2=log, 3=uint, 4=text",
          }),
        ),
        limit: Type.Optional(
          Type.Number({ description: "Maximum number of records to return" }),
        ),
      }),
      mcpToolName: "zabbix.get_history",
      client,
    }),

    mcpTool({
      name: "zabbix_get_items_batch",
      label: "Zabbix Items Batch",
      description:
        'Get multiple items (metrics) from Zabbix in a single efficient request with deduplication. Accepts multiple search patterns (e.g. ["cpu", "memory", "disk"]).',
      parameters: Type.Object({
        searches: Type.Array(Type.String(), {
          description:
            'Search patterns for items (e.g. ["cpu", "memory", "disk"])',
        }),
        hostids: Type.Optional(
          Type.Array(Type.String(), {
            description: "Filter by host IDs",
          }),
        ),
      }),
      mcpToolName: "zabbix.get_items_batch",
      client,
    }),

    mcpTool({
      name: "zabbix_acknowledge_event",
      label: "Zabbix Ack",
      description:
        "Acknowledge a Zabbix event/problem with a message. Used to mark problems as being investigated.",
      parameters: Type.Object({
        eventids: Type.Array(Type.String(), {
          description: "Event IDs to acknowledge",
        }),
        message: Type.String({
          description: "Acknowledgement message",
        }),
      }),
      mcpToolName: "zabbix.acknowledge_event",
      client,
    }),
  ] as unknown as ToolDefinition[];
}
