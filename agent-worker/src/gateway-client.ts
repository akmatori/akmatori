/**
 * TypeScript client for the MCP Gateway using JSON-RPC 2.0.
 *
 * Replaces the Python mcp_client.py wrapper with a native TypeScript
 * implementation that supports tool calls, discovery, and output management.
 */

import * as http from "node:http";
import * as https from "node:https";
import * as fs from "node:fs";
import * as path from "node:path";
import type { ToolAllowlistEntry } from "./types.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type { ToolAllowlistEntry };

export interface GatewayClientOptions {
  gatewayUrl: string;
  incidentId: string;
  workDir?: string;
  /** Request timeout in milliseconds (default: 300000 = 5 minutes) */
  timeoutMs?: number;
  /** Tool instances this incident is authorized to use (undefined = allow all) */
  toolAllowlist?: ToolAllowlistEntry[];
}

export interface SearchToolsResult {
  tools: Array<{
    name: string;
    description: string;
    instances: Array<{ id: number; logical_name: string }>;
  }>;
}

export interface ToolDetailResult {
  name: string;
  description: string;
  params: Record<string, unknown>;
  instances: Array<{ id: number; logical_name: string }>;
}

export interface CallResult {
  /** The tool result (inline or truncated preview) */
  data: unknown;
  /** If output was too large, path to the full output file */
  outputFile?: string;
}

// ---------------------------------------------------------------------------
// Error
// ---------------------------------------------------------------------------

export class GatewayError extends Error {
  constructor(
    public readonly code: number,
    message: string,
    public readonly data?: unknown,
  ) {
    super(`MCP Error ${code}: ${message}`);
    this.name = "GatewayError";
  }
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

/** Output size threshold: responses >= 4KB are written to file */
const OUTPUT_SIZE_THRESHOLD = 4096;

export class GatewayClient {
  private readonly gatewayUrl: string;
  private readonly incidentId: string;
  private readonly workDir: string | undefined;
  private readonly timeoutMs: number;
  private readonly toolAllowlist: ToolAllowlistEntry[] | undefined;
  private requestId = 0;

  constructor(options: GatewayClientOptions) {
    this.gatewayUrl = options.gatewayUrl.replace(/\/+$/, "");
    this.incidentId = options.incidentId;
    this.workDir = options.workDir;
    this.timeoutMs = options.timeoutMs ?? 300_000;
    this.toolAllowlist = options.toolAllowlist;
  }

  /**
   * Call a tool on the MCP Gateway.
   *
   * If the response is >= 4KB and a workDir is configured, the full output
   * is written to a file and a truncated preview is returned inline.
   */
  async call(
    toolName: string,
    args: Record<string, unknown> = {},
    instanceHint?: string,
  ): Promise<CallResult> {
    const params: Record<string, unknown> = {
      name: toolName,
      arguments: args,
    };
    if (instanceHint) {
      params.instance = instanceHint;
    }

    const raw = await this.rpc("tools/call", params);

    // MCP result is { content: [{type, text}], isError? }
    const data = this.extractResult(raw);

    // Output management: large responses go to file
    const serialized = typeof data === "string" ? data : JSON.stringify(data);
    if (serialized.length >= OUTPUT_SIZE_THRESHOLD && this.workDir) {
      const outputFile = this.writeOutputFile(toolName, serialized);
      const preview = serialized.slice(0, 1024) + `\n\n... [truncated, full output at ${outputFile}]`;
      return { data: preview, outputFile };
    }

    return { data };
  }

  /** Search for tools by query string and optional tool type filter. */
  async searchTools(
    query: string,
    toolType?: string,
  ): Promise<SearchToolsResult> {
    const params: Record<string, unknown> = { query };
    if (toolType) {
      params.tool_type = toolType;
    }
    return (await this.rpc("tools/search", params)) as SearchToolsResult;
  }

  /** Get full detail for a specific tool. */
  async getToolDetail(toolName: string): Promise<ToolDetailResult> {
    return (await this.rpc("tools/detail", {
      tool_name: toolName,
    })) as ToolDetailResult;
  }

  // -------------------------------------------------------------------------
  // Internal
  // -------------------------------------------------------------------------

  private nextId(): number {
    return ++this.requestId;
  }

  /** Send a JSON-RPC 2.0 request to the gateway. */
  private async rpc(method: string, params: unknown): Promise<unknown> {
    const body = JSON.stringify({
      jsonrpc: "2.0",
      method,
      params,
      id: this.nextId(),
    });

    const respBody = await this.httpPost(body);

    let parsed: Record<string, unknown>;
    try {
      parsed = JSON.parse(respBody) as Record<string, unknown>;
    } catch {
      throw new GatewayError(-32700, `Invalid JSON response: ${respBody.slice(0, 200)}`);
    }

    if (parsed.error) {
      const err = parsed.error as { code?: number; message?: string; data?: unknown };
      throw new GatewayError(
        err.code ?? -32000,
        err.message ?? "Unknown error",
        err.data,
      );
    }

    return parsed.result;
  }

  /** Extract tool result from MCP content envelope. */
  private extractResult(raw: unknown): unknown {
    if (raw == null) return null;

    if (typeof raw === "object" && raw !== null && "content" in raw) {
      const envelope = raw as { content?: unknown[]; isError?: boolean };
      if (Array.isArray(envelope.content) && envelope.content.length > 0) {
        const first = envelope.content[0] as { text?: string };
        const text = first.text ?? "";

        if (envelope.isError) {
          throw new GatewayError(-32000, `Tool execution failed: ${text}`);
        }

        try {
          return JSON.parse(text);
        } catch {
          return text;
        }
      }

      if (envelope.isError) {
        throw new GatewayError(-32000, "Tool execution failed");
      }
    }

    return raw;
  }

  /** Write large output to a file and return the file path. */
  private writeOutputFile(toolName: string, content: string): string {
    const dir = path.join(this.workDir!, "tool_outputs");
    fs.mkdirSync(dir, { recursive: true });

    const safeName = toolName.replace(/[^a-zA-Z0-9._-]/g, "_");
    const timestamp = Date.now();
    const filePath = path.join(dir, `${safeName}_${timestamp}.json`);
    fs.writeFileSync(filePath, content, "utf-8");
    return filePath;
  }

  /** HTTP POST to the gateway /mcp endpoint, bypassing proxy. */
  private httpPost(body: string): Promise<string> {
    return new Promise((resolve, reject) => {
      const url = new URL(`${this.gatewayUrl}/mcp`);
      const isHttps = url.protocol === "https:";
      const mod = isHttps ? https : http;

      const headers: Record<string, string> = {
        "Content-Type": "application/json",
        "Content-Length": Buffer.byteLength(body).toString(),
      };
      if (this.incidentId) {
        headers["X-Incident-ID"] = this.incidentId;
      }
      if (this.toolAllowlist) {
        headers["X-Tool-Allowlist"] = JSON.stringify(this.toolAllowlist);
      }

      const req = mod.request(
        {
          hostname: url.hostname,
          port: url.port || (isHttps ? 443 : 80),
          path: url.pathname,
          method: "POST",
          headers,
          timeout: this.timeoutMs,
        },
        (res) => {
          const chunks: Buffer[] = [];
          res.on("data", (chunk: Buffer) => chunks.push(chunk));
          res.on("end", () => {
            const body = Buffer.concat(chunks).toString("utf-8");
            if (res.statusCode && res.statusCode >= 400) {
              reject(new GatewayError(-32000, `Gateway returned HTTP ${res.statusCode}: ${body.slice(0, 200)}`));
              return;
            }
            resolve(body);
          });
          res.on("error", (err) => reject(new GatewayError(-32000, `Response error: ${err.message}`)));
        },
      );

      req.on("error", (err) =>
        reject(new GatewayError(-32000, `Connection error: ${err.message}`)),
      );
      req.on("timeout", () => {
        req.destroy();
        reject(new GatewayError(-32000, "Request timed out"));
      });

      req.write(body);
      req.end();
    });
  }
}
