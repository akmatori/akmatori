/**
 * HTTP client for the MCP Gateway.
 *
 * Sends JSON-RPC 2.0 requests to the MCP Gateway `/mcp` endpoint.
 * Ports the Python `codex-tools/mcp_client.py` logic to TypeScript.
 */

/** JSON-RPC 2.0 request envelope. */
export interface JsonRpcRequest {
  jsonrpc: "2.0";
  method: string;
  params: {
    name: string;
    arguments: Record<string, unknown>;
  };
  id: number;
}

/** JSON-RPC 2.0 success response. */
export interface JsonRpcSuccessResponse {
  jsonrpc: "2.0";
  id: number;
  result: {
    content: Array<{ type: string; text: string }>;
    isError?: boolean;
  };
}

/** JSON-RPC 2.0 error response. */
export interface JsonRpcErrorResponse {
  jsonrpc: "2.0";
  id: number;
  error: {
    code: number;
    message: string;
    data?: unknown;
  };
}

export type JsonRpcResponse = JsonRpcSuccessResponse | JsonRpcErrorResponse;

export class MCPError extends Error {
  constructor(
    message: string,
    public readonly code?: number,
    public readonly data?: unknown,
  ) {
    super(message);
    this.name = "MCPError";
  }
}

export interface MCPClientOptions {
  /** MCP Gateway base URL (e.g. "http://mcp-gateway:8080") */
  gatewayUrl: string;
  /** Incident ID for credential resolution */
  incidentId: string;
  /** Request timeout in ms (default: 30000) */
  timeoutMs?: number;
}

export class MCPClient {
  private readonly gatewayUrl: string;
  private readonly incidentId: string;
  private readonly timeoutMs: number;
  private requestId = 0;

  constructor(opts: MCPClientOptions) {
    this.gatewayUrl = opts.gatewayUrl.replace(/\/+$/, "");
    this.incidentId = opts.incidentId;
    this.timeoutMs = opts.timeoutMs ?? 30_000;
  }

  /**
   * Call an MCP tool via JSON-RPC 2.0.
   *
   * @param toolName - Dot-separated tool name (e.g. "ssh.execute_command")
   * @param args - Tool arguments
   * @returns The text content from the tool result
   */
  async callTool(
    toolName: string,
    args: Record<string, unknown> = {},
  ): Promise<string> {
    const id = ++this.requestId;

    const body: JsonRpcRequest = {
      jsonrpc: "2.0",
      method: "tools/call",
      params: {
        name: toolName,
        arguments: args,
      },
      id,
    };

    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.timeoutMs);

    try {
      const response = await fetch(`${this.gatewayUrl}/mcp`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Incident-ID": this.incidentId,
        },
        body: JSON.stringify(body),
        signal: controller.signal,
      });

      if (!response.ok) {
        throw new MCPError(
          `MCP Gateway returned HTTP ${response.status}: ${response.statusText}`,
        );
      }

      const data = (await response.json()) as JsonRpcResponse;

      if ("error" in data && data.error) {
        throw new MCPError(
          data.error.message,
          data.error.code,
          data.error.data,
        );
      }

      const result = (data as JsonRpcSuccessResponse).result;

      if (result.isError) {
        const errorText =
          result.content
            .filter((c) => c.type === "text")
            .map((c) => c.text)
            .join("\n") || "Unknown tool error";
        throw new MCPError(errorText);
      }

      return result.content
        .filter((c) => c.type === "text")
        .map((c) => c.text)
        .join("\n");
    } catch (err) {
      if (err instanceof MCPError) throw err;
      if ((err as Error).name === "AbortError") {
        throw new MCPError(
          `MCP Gateway request timed out after ${this.timeoutMs}ms`,
        );
      }
      throw new MCPError(
        `MCP Gateway request failed: ${(err as Error).message}`,
      );
    } finally {
      clearTimeout(timeout);
    }
  }
}
