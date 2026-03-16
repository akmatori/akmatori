/**
 * Gateway tool definitions for the pi-mono coding agent.
 *
 * Registers `gateway_call` as a custom tool that replaces the Python wrapper
 * pattern. The agent calls tools on the MCP Gateway directly via the
 * TypeScript GatewayClient instead of shelling out to Python scripts.
 */

import { Type, type TSchema, type Static } from "@sinclair/typebox";
import type { GatewayClient, CallResult, SearchToolsResult, ToolDetailResult } from "./gateway-client.js";
import { ScriptExecutor } from "./script-executor.js";

// Re-export the ToolDefinition type from pi-coding-agent for convenience.
// We define our own compatible interface to avoid tight coupling with the
// extension system's full type surface (renderCall, renderResult, etc.).
// The `customTools` option on createAgentSession accepts this shape.

export interface GatewayToolContext {
  client: GatewayClient;
}

// ---------------------------------------------------------------------------
// gateway_call tool schema
// ---------------------------------------------------------------------------

export const GatewayCallParams = Type.Object({
  tool_name: Type.String({ description: "The MCP tool name to call (e.g. 'ssh.execute_command', 'zabbix.get_problems')" }),
  args: Type.Record(Type.String(), Type.Unknown(), { description: "Arguments to pass to the tool" }),
  instance: Type.Optional(Type.String({ description: "Logical name of the tool instance to use (optional, falls back to default)" })),
});

export type GatewayCallInput = Static<typeof GatewayCallParams>;

// ---------------------------------------------------------------------------
// search_tools tool schema
// ---------------------------------------------------------------------------

export const SearchToolsParams = Type.Object({
  query: Type.String({ description: "Search query to match against tool names and descriptions" }),
  tool_type: Type.Optional(Type.String({ description: "Optional filter by tool type (e.g. 'ssh', 'zabbix', 'victoriametrics')" })),
});

export type SearchToolsInput = Static<typeof SearchToolsParams>;

// ---------------------------------------------------------------------------
// get_tool_detail tool schema
// ---------------------------------------------------------------------------

export const GetToolDetailParams = Type.Object({
  tool_name: Type.String({ description: "The full tool name to get details for (e.g. 'ssh.execute_command')" }),
});

export type GetToolDetailInput = Static<typeof GetToolDetailParams>;

// ---------------------------------------------------------------------------
// execute_script tool schema
// ---------------------------------------------------------------------------

export const ExecuteScriptParams = Type.Object({
  code: Type.String({ description: "JavaScript code to execute. Has access to gateway_call(), search_tools(), get_tool_detail(), console.log(), and a scoped fs object. Top-level await is supported." }),
});

export type ExecuteScriptInput = Static<typeof ExecuteScriptParams>;

// ---------------------------------------------------------------------------
// Tool definition factory
// ---------------------------------------------------------------------------

/**
 * Create the `gateway_call` tool definition for registration with pi-mono.
 *
 * The returned object conforms to the `ToolDefinition` interface expected by
 * `createAgentSession({ customTools: [...] })`.
 */
export function createGatewayCallTool(ctx: GatewayToolContext) {
  return {
    name: "gateway_call",
    label: "Gateway Call",
    description:
      "Call a tool on the MCP Gateway. Use this to execute infrastructure tools " +
      "(SSH commands, Zabbix queries, VictoriaMetrics queries, etc.) by name. " +
      "Each skill's SKILL.md lists the available tools and their logical instance names.",
    promptGuidelines: [
      "Use gateway_call to invoke infrastructure tools instead of Python wrappers.",
      "Each skill's SKILL.md lists assigned tools with their logical names and available operations. Read SKILL.md first.",
      "Example: gateway_call({ tool_name: \"ssh.execute_command\", args: { command: \"uptime\", servers: [\"web-01\"] }, instance: \"prod-ssh\" })",
      "Example: gateway_call({ tool_name: \"zabbix.get_problems\", args: { severity_min: 3 }, instance: \"prod-zabbix\" })",
      "For large result sets, the output is automatically saved to a file and a preview is returned.",
    ],
    parameters: GatewayCallParams,
    execute: async (
      _toolCallId: string,
      params: GatewayCallInput,
      signal: AbortSignal | undefined,
      _onUpdate: unknown,
    ) => {
      try {
        const result: CallResult = await ctx.client.call(
          params.tool_name,
          params.args as Record<string, unknown>,
          params.instance,
          signal,
        );

        let text: string;
        if (typeof result.data === "string") {
          text = result.data;
        } else {
          text = JSON.stringify(result.data, null, 2);
        }

        if (result.outputFile) {
          text += `\n\n[Full output saved to: ${result.outputFile}]`;
        }

        return {
          content: [{ type: "text" as const, text }],
          details: {},
        };
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        return {
          content: [{ type: "text" as const, text: `Error: ${message}` }],
          details: {},
        };
      }
    },
  };
}

// ---------------------------------------------------------------------------
// search_tools tool factory
// ---------------------------------------------------------------------------

/**
 * Create the `search_tools` tool definition for registration with pi-mono.
 *
 * Allows the agent to discover available tools by searching with a query
 * string and optional tool type filter.
 */
export function createSearchToolsTool(ctx: GatewayToolContext) {
  return {
    name: "search_tools",
    label: "Search Tools",
    description:
      "Search for available infrastructure tools on the MCP Gateway. " +
      "Returns a list of matching tools with their descriptions and available instances. " +
      "Use this to discover what tools are available before calling them.",
    promptGuidelines: [
      "Use search_tools to discover available infrastructure tools when you're unsure what's available.",
      "Example: search_tools({ query: \"ssh\" }) — finds all SSH-related tools",
      "Example: search_tools({ query: \"metrics\", tool_type: \"victoria_metrics\" }) — finds VictoriaMetrics tools",
      "After finding a tool, use get_tool_detail to see its full parameter schema before calling it.",
    ],
    parameters: SearchToolsParams,
    execute: async (
      _toolCallId: string,
      params: SearchToolsInput,
      signal: AbortSignal | undefined,
      _onUpdate: unknown,
    ) => {
      try {
        const result: SearchToolsResult = await ctx.client.searchTools(
          params.query,
          params.tool_type,
          signal,
        );

        const text = JSON.stringify(result, null, 2);
        return {
          content: [{ type: "text" as const, text }],
          details: {},
        };
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        return {
          content: [{ type: "text" as const, text: `Error: ${message}` }],
          details: {},
        };
      }
    },
  };
}

// ---------------------------------------------------------------------------
// get_tool_detail tool factory
// ---------------------------------------------------------------------------

/**
 * Create the `get_tool_detail` tool definition for registration with pi-mono.
 *
 * Returns the full JSON schema and available instances for a specific tool.
 */
export function createGetToolDetailTool(ctx: GatewayToolContext) {
  return {
    name: "get_tool_detail",
    label: "Get Tool Detail",
    description:
      "Get full details for a specific MCP Gateway tool, including its parameter schema " +
      "and available instances. Use this after search_tools to understand how to call a tool.",
    promptGuidelines: [
      "Use get_tool_detail to see the full parameter schema for a tool before calling it with gateway_call.",
      "Example: get_tool_detail({ tool_name: \"ssh.execute_command\" }) — shows parameters and instances",
    ],
    parameters: GetToolDetailParams,
    execute: async (
      _toolCallId: string,
      params: GetToolDetailInput,
      signal: AbortSignal | undefined,
      _onUpdate: unknown,
    ) => {
      try {
        const result: ToolDetailResult = await ctx.client.getToolDetail(
          params.tool_name,
          signal,
        );

        const text = JSON.stringify(result, null, 2);
        return {
          content: [{ type: "text" as const, text }],
          details: {},
        };
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        return {
          content: [{ type: "text" as const, text: `Error: ${message}` }],
          details: {},
        };
      }
    },
  };
}

// ---------------------------------------------------------------------------
// execute_script tool factory
// ---------------------------------------------------------------------------

export interface ExecuteScriptToolContext {
  client: GatewayClient;
  workDir: string;
}

/**
 * Create the `execute_script` tool definition for registration with pi-mono.
 *
 * Runs agent-written JavaScript in an isolated vm context with built-in
 * gateway_call(), search_tools(), and get_tool_detail() functions.
 */
export function createExecuteScriptTool(ctx: ExecuteScriptToolContext) {
  const executor = new ScriptExecutor({
    client: ctx.client,
    workDir: ctx.workDir,
  });

  return {
    name: "execute_script",
    label: "Execute Script",
    description:
      "Execute a JavaScript script in an isolated runtime with built-in gateway functions. " +
      "Use this for batch operations, complex data processing, or orchestrating multiple tool calls. " +
      "The script has access to gateway_call(), search_tools(), get_tool_detail(), console.log(), and a scoped fs object.",
    promptGuidelines: [
      "Use execute_script for batch operations that require multiple gateway_call invocations or data processing.",
      "Top-level await is supported. The script runs in an async context.",
      "Example: execute_script({ code: `const hosts = await gateway_call(\"zabbix.get_hosts\", {}); return hosts;` })",
      "Example: execute_script({ code: `const results = []; for (const h of [\"web-01\",\"web-02\"]) { results.push(await gateway_call(\"ssh.execute_command\", {command: \"uptime\", servers: [h]}, \"prod-ssh\")); } return results;` })",
      "The script has scoped fs access (readFileSync, writeFileSync, existsSync, readdirSync, mkdirSync) within the incident workspace.",
      "Scripts time out after 5 minutes.",
    ],
    parameters: ExecuteScriptParams,
    execute: async (
      _toolCallId: string,
      params: ExecuteScriptInput,
      signal: AbortSignal | undefined,
      _onUpdate: unknown,
    ) => {
      try {
        const result = await executor.execute(params.code, signal);

        let text = result.output;
        if (result.logs.length > 0 && result.output !== result.logs.join("\n")) {
          text += `\n\n--- Console Output ---\n${result.logs.join("\n")}`;
        }

        return {
          content: [{ type: "text" as const, text }],
          details: {},
        };
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        return {
          content: [{ type: "text" as const, text: `Error: ${message}` }],
          details: {},
        };
      }
    },
  };
}
