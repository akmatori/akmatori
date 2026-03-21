/**
 * Gateway tool definitions for the pi-mono coding agent.
 *
 * Registers `gateway_call` and supporting tools as custom tools for the
 * pi-mono coding agent. The agent calls tools on the MCP Gateway directly
 * via the TypeScript GatewayClient.
 */

import { Type, type TSchema, type Static } from "@sinclair/typebox";
import type { GatewayClient, CallResult, ListToolsResult, ToolDetailResult } from "./gateway-client.js";
import { ScriptExecutor } from "./script-executor.js";

// Re-export the ToolDefinition type from pi-coding-agent for convenience.
// We define our own compatible interface to avoid tight coupling with the
// extension system's full type surface (renderCall, renderResult, etc.).
// The `customTools` option on createAgentSession accepts this shape.

// ---------------------------------------------------------------------------
// Direct tool call detection helpers
// ---------------------------------------------------------------------------

/** Known gateway tool names that the agent should invoke via gateway_call. */
const REGISTERED_TOOL_NAMES = new Set([
  "gateway_call",
  "list_tools_for_tool_type",
  "get_tool_detail",
  "list_tool_types",
  "execute_script",
]);


/** Error patterns that indicate a tool routing/discovery issue where the hint is relevant.
 * Patterns are intentionally specific to tool/method routing errors to avoid
 * false positives on general execution errors like "File not found: config.yaml"
 * or "Tool execution failed: Module not found: node.js".
 */
const TOOL_ROUTING_ERROR_PATTERNS = [
  /\bunknown tool\b/i,
  /\bunknown method\b/i,
  /\bmethod not found\b/i,
  /\btool not found\b/i,
  /\btool\s+\S+\s+not found\b/i,  // SDK format: "Tool <name> not found"
  /\bno such tool\b/i,
  /(?<!\d)-32601\b/,  // JSON-RPC method-not-found error code
];

/**
 * Returns true if the given string looks like a dot-namespaced MCP tool name
 * (e.g. "victoria_metrics.instant_query", "ssh.execute_command",
 * "internal-billing.get_invoice").
 *
 * Accepts any namespace that looks like a valid identifier (alphanumeric,
 * underscores, hyphens) so it also covers dynamically registered HTTP
 * connector tool types.
 */
export function isDotNamespacedToolName(name: string): boolean {
  if (!name || typeof name !== "string") return false;
  const dotIndex = name.indexOf(".");
  if (dotIndex <= 0 || dotIndex >= name.length - 1 || name.includes(" ")) return false;
  // Accept any identifier-like namespace (covers known types + dynamic HTTP connectors)
  const namespace = name.substring(0, dotIndex);
  return /^[a-zA-Z_][a-zA-Z0-9_-]*$/.test(namespace);
}

/**
 * If an error message indicates a tool-not-found scenario and contains a
 * dot-namespaced MCP tool name, return a hint suggesting gateway_call usage.
 * Suppressed for authorization errors (agent is already using gateway_call correctly).
 * Returns an empty string if no hint is applicable.
 *
 * NOTE: Currently exported for testing only. The pi-mono SDK does not expose a
 * hook for tool-not-found errors (the "Tool X not found" message is returned
 * directly to the LLM context without passing through afterToolCall). The
 * BASH_TOOL_GUIDELINES prompt provides equivalent guidance as a mitigation.
 * This function is ready to be wired in once the SDK adds such a hook.
 */
export function formatDirectToolCallHint(errorMessage: string): string {
  // Only hint on tool routing/discovery errors (not-found, unknown tool, etc.)
  // to avoid false positives on general execution errors like "node.js version 18".
  if (!TOOL_ROUTING_ERROR_PATTERNS.some((p) => p.test(errorMessage))) return "";

  // Extract potential tool names from the error message.
  // Try quoted first, then fall back to unquoted (gateway errors don't quote tool names).
  // Namespace segments may contain hyphens (e.g., "internal-billing.get_invoice",
  // "ext.github-enterprise.create_issue"). All dot-separated segments allow
  // alphanumerics, underscores, and hyphens to support proxy namespace prefixes.
  const quoted = errorMessage.match(/['"`]([a-zA-Z_][a-zA-Z0-9_-]*(?:\.[a-zA-Z_][a-zA-Z0-9_-]*)+)['"`]/);
  const unquoted = errorMessage.match(/\b([a-zA-Z_][a-zA-Z0-9_-]*(?:\.[a-zA-Z_][a-zA-Z0-9_-]*)+)\b/);
  const match = quoted ?? unquoted;
  if (match && isDotNamespacedToolName(match[1]) && !REGISTERED_TOOL_NAMES.has(match[1])) {
    return (
      `\n\nHint: '${match[1]}' is a gateway tool. ` +
      `Call it via gateway_call("${match[1]}", {<args>}) or gateway_call("${match[1]}", {<args>}, "<instance>").`
    );
  }
  return "";
}

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
// list_tools_for_tool_type tool schema
// ---------------------------------------------------------------------------

export const ListToolsForToolTypeParams = Type.Object({
  tool_type: Type.String({ description: "Tool type to list (e.g. 'ssh', 'zabbix', 'victoria_metrics'). Use list_tool_types to see available types." }),
});

export type ListToolsForToolTypeInput = Static<typeof ListToolsForToolTypeParams>;

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
  code: Type.String({ description: "JavaScript code to execute in an isolated sandbox. Pre-injected globals: gateway_call(), list_tools_for_tool_type(), list_tool_types(), get_tool_detail(), console.log(), and fs (synchronous: readFileSync, writeFileSync, existsSync, readdirSync, mkdirSync). Top-level await works for gateway functions. Do NOT use require() or import()." }),
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
      "Use gateway_call to invoke infrastructure tools. Always include the args parameter as an object, even if empty: gateway_call({ tool_name: \"...\", args: {} })",
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
        // Do not append the "use gateway_call" hint here — the agent is
        // already using gateway_call. The hint is only useful when the
        // pi-mono framework itself rejects a direct tool call attempt.
        return {
          content: [{ type: "text" as const, text: `Error: ${message}` }],
          details: {},
        };
      }
    },
  };
}

// ---------------------------------------------------------------------------
// list_tools_for_tool_type tool factory
// ---------------------------------------------------------------------------

/**
 * Create the `list_tools_for_tool_type` tool definition for registration with pi-mono.
 *
 * Allows the agent to discover available tools by listing all tools of a given type.
 */
export function createListToolsForToolTypeTool(ctx: GatewayToolContext) {
  return {
    name: "list_tools_for_tool_type",
    label: "List Tools For Tool Type",
    description:
      "List available infrastructure tools on the MCP Gateway filtered by tool type. " +
      "Returns all tools of the specified type with their descriptions and available instances. " +
      "Use this to discover what tools are available before calling them.",
    promptGuidelines: [
      "Call list_tool_types first to see available tool types. Then list tools by type: list_tools_for_tool_type({ tool_type: \"victoria_metrics\" }).",
      "Example: list_tools_for_tool_type({ tool_type: \"ssh\" }) — lists all SSH tools",
      "Example: list_tools_for_tool_type({ tool_type: \"victoria_metrics\" }) — lists VictoriaMetrics tools",
      "After finding a tool, use get_tool_detail to see its full parameter schema before calling it.",
    ],
    parameters: ListToolsForToolTypeParams,
    execute: async (
      _toolCallId: string,
      params: ListToolsForToolTypeInput,
      signal: AbortSignal | undefined,
      _onUpdate: unknown,
    ) => {
      try {
        const result: ListToolsResult = await ctx.client.listToolsByType(
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
      "and available instances. Use this after list_tools_for_tool_type to understand how to call a tool.",
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
// list_tool_types tool factory
// ---------------------------------------------------------------------------

/**
 * Create the `list_tool_types` tool definition for registration with pi-mono.
 *
 * Returns the list of available tool types for the current incident,
 * allowing the agent to see what's available before searching.
 */
export function createListToolTypesTool(ctx: GatewayToolContext) {
  return {
    name: "list_tool_types",
    label: "List Tool Types",
    description:
      "List all available tool types for this incident. Call this first to see what infrastructure " +
      "tools are available before using list_tools_for_tool_type.",
    promptGuidelines: [
      "Call list_tool_types first to see what tool types are available (e.g. ssh, zabbix, victoria_metrics).",
      "Then use list_tools_for_tool_type with a specific type to find individual tools within that type.",
    ],
    parameters: Type.Object({}),
    execute: async (
      _toolCallId: string,
      _params: Record<string, never>,
      signal: AbortSignal | undefined,
      _onUpdate: unknown,
    ) => {
      try {
        const result = await ctx.client.listToolTypes(signal);
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
 * gateway_call(), list_tools_for_tool_type(), and get_tool_detail() functions.
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
      "Execute JavaScript code in an isolated sandbox with built-in gateway functions and synchronous file I/O. " +
      "Use this for batch operations, complex data processing, or orchestrating multiple tool calls. " +
      "IMPORTANT: require() and import() are NOT available. Use the pre-injected globals: gateway_call(), list_tools_for_tool_type(), get_tool_detail(), console.log(), and the synchronous fs object (readFileSync, writeFileSync, etc.).",
    promptGuidelines: [
      "Use execute_script for batch operations that require multiple gateway_call invocations or data processing.",
      "Top-level await is supported for gateway_call, list_tools_for_tool_type, and get_tool_detail only.",
      "IMPORTANT: Do NOT use require() or import() — they are not available. All globals (fs, gateway_call, etc.) are pre-injected.",
      "IMPORTANT: fs is synchronous only — use fs.readFileSync(path), NOT fs.readFile() or require('fs').",
      "Available fs methods: readFileSync(path), writeFileSync(path, data), existsSync(path), readdirSync(path), mkdirSync(path, {recursive: true})",
      "Example: const data = JSON.parse(fs.readFileSync('tool_outputs/result.json'));",
      "Example: const hosts = await gateway_call(\"zabbix.get_hosts\", {}); return hosts;",
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
