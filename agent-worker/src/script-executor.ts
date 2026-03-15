/**
 * Isolated script execution engine for the execute_script tool.
 *
 * Runs agent-written JavaScript code in a Node.js `vm` context with injected
 * gateway functions (gateway_call, search_tools, get_tool_detail) and scoped
 * filesystem access.
 */

import * as vm from "node:vm";
import * as fs from "node:fs";
import * as path from "node:path";
import type { GatewayClient } from "./gateway-client.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ScriptExecutorOptions {
  client: GatewayClient;
  /** Incident workspace directory — fs access is scoped to this path */
  workDir: string;
  /** Execution timeout in milliseconds (default: 300000 = 5 minutes) */
  timeoutMs?: number;
}

export interface ScriptResult {
  /** The script's return value (or captured console output if no return) */
  output: string;
  /** Console log lines captured during execution */
  logs: string[];
}

// ---------------------------------------------------------------------------
// ScriptExecutor
// ---------------------------------------------------------------------------

const DEFAULT_TIMEOUT_MS = 300_000; // 5 minutes

export class ScriptExecutor {
  private readonly client: GatewayClient;
  private readonly workDir: string;
  private readonly timeoutMs: number;

  constructor(options: ScriptExecutorOptions) {
    this.client = options.client;
    this.workDir = options.workDir;
    this.timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  }

  /**
   * Execute a script in an isolated vm context.
   *
   * The script receives:
   * - `gateway_call(toolName, args, instance?)` — async, calls MCP Gateway
   * - `search_tools(query, toolType?)` — async, searches available tools
   * - `get_tool_detail(toolName)` — async, gets tool schema
   * - `console.log(...)` — captures output
   * - `fs` — scoped to workDir (readFileSync, writeFileSync, mkdirSync, readdirSync, existsSync)
   * - `JSON` — standard JSON object
   * - `setTimeout` / `clearTimeout` — for async delays
   *
   * The script is wrapped in an async IIFE so `await` works at the top level.
   * The return value of the last expression (or explicit `return`) becomes the output.
   */
  async execute(code: string): Promise<ScriptResult> {
    const logs: string[] = [];

    // Build scoped fs object that restricts paths to workDir
    const scopedFs = this.createScopedFs();

    // Collect pending promises from gateway calls so we can await them
    const context: Record<string, unknown> = {
      // Gateway functions
      gateway_call: async (
        toolName: string,
        args: Record<string, unknown> = {},
        instance?: string,
      ) => {
        const result = await this.client.call(toolName, args, instance);
        return result.data;
      },
      search_tools: async (query: string, toolType?: string) => {
        return await this.client.searchTools(query, toolType);
      },
      get_tool_detail: async (toolName: string) => {
        return await this.client.getToolDetail(toolName);
      },

      // Console capture
      console: {
        log: (...args: unknown[]) => {
          logs.push(args.map(String).join(" "));
        },
        warn: (...args: unknown[]) => {
          logs.push(`[warn] ${args.map(String).join(" ")}`);
        },
        error: (...args: unknown[]) => {
          logs.push(`[error] ${args.map(String).join(" ")}`);
        },
      },

      // Scoped filesystem
      fs: scopedFs,

      // Standard globals
      JSON,
      Array,
      Object,
      Map,
      Set,
      Date,
      Math,
      RegExp,
      Promise,
      Error,
      TypeError,
      RangeError,
      parseInt,
      parseFloat,
      isNaN,
      isFinite,
      encodeURIComponent,
      decodeURIComponent,
      encodeURI,
      decodeURI,

      // Timers (for async patterns)
      setTimeout,
      clearTimeout,
    };

    vm.createContext(context);

    // Wrap code in an async IIFE so top-level await works
    const wrappedCode = `(async () => {\n${code}\n})()`;

    let script: vm.Script;
    try {
      script = new vm.Script(wrappedCode, {
        filename: "execute_script",
        timeout: this.timeoutMs,
      });
    } catch (err) {
      throw new Error(`Script compilation error: ${(err as Error).message}`);
    }

    let returnValue: unknown;
    try {
      // vm.Script.timeout only applies to synchronous execution.
      // For async code we use AbortController + setTimeout for the timeout.
      const result = script.runInContext(context);

      // result is the Promise from the async IIFE
      let timer: ReturnType<typeof setTimeout>;
      try {
        returnValue = await Promise.race([
          result,
          new Promise((_, reject) => {
            timer = setTimeout(
              () => reject(new Error("Script execution timed out")),
              this.timeoutMs,
            );
          }),
        ]);
      } finally {
        clearTimeout(timer!);
      }
    } catch (err) {
      const message = (err as Error).message ?? String(err);
      if (
        message.includes("Script execution timed out") ||
        message.includes("timed out")
      ) {
        throw new Error("Script execution timed out");
      }
      throw new Error(`Script execution error: ${message}`);
    }

    // Build output: prefer return value, fall back to logs
    let output: string;
    if (returnValue !== undefined && returnValue !== null) {
      output =
        typeof returnValue === "string"
          ? returnValue
          : JSON.stringify(returnValue, null, 2);
    } else if (logs.length > 0) {
      output = logs.join("\n");
    } else {
      output = "(no output)";
    }

    return { output, logs };
  }

  /**
   * Create a filesystem object scoped to the workDir.
   * All path arguments are resolved relative to workDir and must not escape it.
   */
  private createScopedFs() {
    const workDir = this.workDir;

    function resolveSafe(filePath: string): string {
      const resolved = path.resolve(workDir, filePath);
      if (resolved !== workDir && !resolved.startsWith(workDir + path.sep)) {
        throw new Error(
          `Access denied: path "${filePath}" resolves outside workspace`,
        );
      }
      return resolved;
    }

    return {
      readFileSync: (filePath: string, encoding?: string) => {
        const safe = resolveSafe(filePath);
        return fs.readFileSync(safe, (encoding as BufferEncoding) ?? "utf-8");
      },
      writeFileSync: (filePath: string, data: string) => {
        const safe = resolveSafe(filePath);
        fs.mkdirSync(path.dirname(safe), { recursive: true });
        fs.writeFileSync(safe, data, "utf-8");
      },
      mkdirSync: (dirPath: string, options?: { recursive?: boolean }) => {
        const safe = resolveSafe(dirPath);
        fs.mkdirSync(safe, options);
      },
      readdirSync: (dirPath: string) => {
        const safe = resolveSafe(dirPath);
        return fs.readdirSync(safe);
      },
      existsSync: (filePath: string) => {
        const safe = resolveSafe(filePath);
        return fs.existsSync(safe);
      },
    };
  }
}
