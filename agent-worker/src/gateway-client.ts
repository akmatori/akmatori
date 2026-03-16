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
  /** Tool instances this incident is authorized to use. When undefined, the X-Tool-Allowlist header is omitted and the gateway allows all tools. */
  toolAllowlist?: ToolAllowlistEntry[];
}

export interface SearchToolsResult {
  tools: Array<{
    name: string;
    description: string;
    instances: string[];
  }>;
}

export interface ToolDetailResult {
  name: string;
  description: string;
  input_schema: Record<string, unknown>;
  instances: Array<{ id: number; logical_name: string; name: string }>;
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
// Smart preview
// ---------------------------------------------------------------------------

/**
 * Build an actionable summary of a large serialized tool output.
 *
 * Attempts to parse the payload as JSON and produces a structured summary
 * depending on the shape of the data.  Falls back to a raw character slice
 * when the payload is not valid JSON.
 */
export function buildSmartPreview(serialized: string, outputFile: string): string {
  const tip =
    `\nFull output saved to: ${outputFile}` +
    `\nTip: Use execute_script with fs.readFileSync('${outputFile}', 'utf-8') to search/filter the full data.`;

  let parsed: unknown;
  try {
    parsed = JSON.parse(serialized);
  } catch {
    return serialized.slice(0, 1024) + `\n\n... [truncated]` + tip;
  }

  // Prometheus envelope ({ resultType, result: [] })
  if (
    parsed !== null &&
    typeof parsed === "object" &&
    !Array.isArray(parsed) &&
    "resultType" in parsed &&
    "result" in parsed &&
    Array.isArray((parsed as Record<string, unknown>).result)
  ) {
    const prom = parsed as {
      resultType: string;
      result: Array<{ metric?: Record<string, string>; value?: unknown; values?: unknown }>;
    };
    const resultType = prom.resultType;
    const resultCount = prom.result.length;

    const names = new Set<string>();
    for (const item of prom.result) {
      const metricName = item.metric?.["__name__"];
      if (metricName && names.size < 5) {
        names.add(metricName);
      }
    }
    const nameList = names.size > 0 ? `[${[...names].join(", ")}]` : "(none)";

    const sampleLines = prom.result.slice(0, 3).map((item, idx) => {
      const metricStr = JSON.stringify(item.metric ?? {});
      return `  [${idx}] metric: ${metricStr.slice(0, 200)}`;
    });

    const lines: string[] = [
      `Prometheus ${resultType} result — ${resultCount} series`,
      `Metric names (up to 5): ${nameList}`,
      `First ${sampleLines.length} series:`,
      ...sampleLines,
      `Tip: Use execute_script to parse the full data and filter by label.`,
    ];
    return lines.join("\n") + tip;
  }

  // Plain array
  if (Array.isArray(parsed)) {
    const count = parsed.length;
    const sampleLines = (parsed as unknown[]).slice(0, 3).map((item, idx) => {
      const str = JSON.stringify(item);
      return `  [${idx}] ${str.slice(0, 200)}`;
    });
    const lines: string[] = [
      `Array with ${count} item(s):`,
      ...sampleLines,
      ...(count > 3 ? [`  ... and ${count - 3} more`] : []),
    ];
    return lines.join("\n") + tip;
  }

  // Plain object
  if (typeof parsed === "object" && parsed !== null) {
    const obj = parsed as Record<string, unknown>;
    const keys = Object.keys(obj);
    const shownKeys = keys.slice(0, 10);

    const keyLines = shownKeys.map((k) => {
      const val = obj[k];
      const valStr = JSON.stringify(val) ?? String(val);
      const typeLabel = Array.isArray(val) ? `array[${(val as unknown[]).length}]` : typeof val;
      return `  ${k} (${typeLabel}): ${valStr.slice(0, 200)}`;
    });

    const lines: string[] = [
      `Object with ${keys.length} top-level key(s): ${keys.slice(0, 10).join(", ")}${keys.length > 10 ? ", ..." : ""}`,
      ...keyLines,
    ];
    return lines.join("\n") + tip;
  }

  return serialized.slice(0, 1024) + `\n\n... [truncated]` + tip;
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
    signal?: AbortSignal,
  ): Promise<CallResult> {
    const params: Record<string, unknown> = {
      name: toolName,
      arguments: args,
    };
    if (instanceHint) {
      params.instance = instanceHint;
    }

    const raw = await this.rpc("tools/call", params, signal);

    // MCP result is { content: [{type, text}], isError? }
    const data = this.extractResult(raw);

    // Output management: large responses go to file
    const serialized = typeof data === "string" ? data : JSON.stringify(data);
    if (serialized.length >= OUTPUT_SIZE_THRESHOLD && this.workDir) {
      const outputFile = this.writeOutputFile(toolName, serialized);
      const preview = buildSmartPreview(serialized, outputFile);
      return { data: preview, outputFile };
    }

    return { data };
  }

  /** Search for tools by query string and optional tool type filter. */
  async searchTools(
    query: string,
    toolType?: string,
    signal?: AbortSignal,
  ): Promise<SearchToolsResult> {
    const params: Record<string, unknown> = { query };
    if (toolType) {
      params.tool_type = toolType;
    }
    return (await this.rpc("tools/search", params, signal)) as SearchToolsResult;
  }

  /** List all available tool types for this incident. */
  async listToolTypes(signal?: AbortSignal): Promise<{ types: string[] }> {
    return (await this.rpc("tools/list_types", {}, signal)) as { types: string[] };
  }

  /** Get full detail for a specific tool. */
  async getToolDetail(toolName: string, signal?: AbortSignal): Promise<ToolDetailResult> {
    return (await this.rpc("tools/detail", {
      tool_name: toolName,
    }, signal)) as ToolDetailResult;
  }

  // -------------------------------------------------------------------------
  // Internal
  // -------------------------------------------------------------------------

  private nextId(): number {
    return ++this.requestId;
  }

  /** Send a JSON-RPC 2.0 request to the gateway. */
  private async rpc(method: string, params: unknown, signal?: AbortSignal): Promise<unknown> {
    const body = JSON.stringify({
      jsonrpc: "2.0",
      method,
      params,
      id: this.nextId(),
    });

    const respBody = await this.httpPost(body, signal);

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
  private httpPost(body: string, signal?: AbortSignal): Promise<string> {
    return new Promise((resolve, reject) => {
      // If already aborted, reject immediately without starting the request.
      if (signal?.aborted) {
        reject(new GatewayError(-32000, "Request aborted"));
        return;
      }

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

      // Cancel the in-flight request when the signal fires.
      const onAbort = () => {
        req.destroy();
        reject(new GatewayError(-32000, "Request aborted"));
      };
      signal?.addEventListener("abort", onAbort, { once: true });

      // Clean up the abort listener once the request settles.
      req.on("close", () => signal?.removeEventListener("abort", onAbort));

      req.write(body);
      req.end();
    });
  }
}
