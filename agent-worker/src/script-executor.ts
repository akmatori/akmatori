/**
 * Isolated script execution engine for the execute_script tool.
 *
 * Runs agent-written JavaScript code in a Node.js `vm` context with injected
 * gateway functions (gateway_call, list_tools_for_tool_type, get_tool_detail) and scoped
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

/**
 * Detect common mistakes in script code before execution.
 * Returns an error message if a mistake is found, or null if the code looks ok.
 */
export function detectCommonMistakes(code: string): string | null {
  if (/\brequire\s*\(/.test(code)) {
    return "require() is not available in the sandbox. " +
      "All globals are pre-injected — use them directly: " +
      "fs (readFileSync, writeFileSync, existsSync, readdirSync, mkdirSync), " +
      "gateway_call(), list_tools_for_tool_type(), get_tool_detail(), console.";
  }
  if (/^\s*import\s+/m.test(code)) {
    return "import statements are not available in the sandbox. " +
      "All globals are pre-injected — use them directly: " +
      "fs, gateway_call(), list_tools_for_tool_type(), get_tool_detail(), console.";
  }
  return null;
}

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
   * - `list_tools_for_tool_type(query, toolType?)` — async, lists available tools by type
   * - `get_tool_detail(toolName)` — async, gets tool schema
   * - `console.log(...)` — captures output
   * - `fs` — scoped to workDir (readFileSync, writeFileSync, mkdirSync, readdirSync, existsSync)
   * - `JSON` — standard JSON object
   * - `setTimeout` / `clearTimeout` — for async delays
   *
   * The script is wrapped in an async IIFE so `await` works at the top level.
   * The return value of the last expression (or explicit `return`) becomes the output.
   */
  async execute(code: string, signal?: AbortSignal): Promise<ScriptResult> {
    // Detect common mistakes and return helpful errors before execution
    const codeCheck = detectCommonMistakes(code);
    if (codeCheck) {
      throw new Error(codeCheck);
    }

    const logs: string[] = [];

    // Create an internal AbortController that combines the external signal and
    // the execution timeout.  Any of the three triggers (external abort, timeout,
    // or normal completion) will settle the promise exactly once.
    const controller = new AbortController();
    const internalSignal = controller.signal;

    // If the caller's signal is already aborted, reject immediately without
    // building a vm context or running any script code.
    if (signal?.aborted) {
      throw new Error("Script execution aborted");
    }

    // Forward future aborts from the caller.
    const onExternalAbort = () => controller.abort(signal!.reason);
    signal?.addEventListener("abort", onExternalAbort, { once: true });

    // Build scoped fs object that restricts paths to workDir
    const scopedFs = this.createScopedFs();

    // Use null-prototype object to prevent this.constructor escape chain.
    // Standard globals (Array, Object, JSON, Math, Promise, etc.) are provided
    // natively by the vm context — do NOT inject host-realm versions as they
    // expose the outer Function constructor via obj.constructor.constructor.
    const context = Object.create(null) as Record<string, unknown>;

    // Gateway functions — injected as __private refs, wrapped inside the
    // context later to prevent direct .constructor access on outer-realm closures.
    // Each checks the internal abort signal before making an RPC so that a
    // timed-out or externally aborted script cannot start new requests.
    context.__gw_call = async (
      toolName: string,
      args: Record<string, unknown> = {},
      instance?: string,
    ) => {
      if (internalSignal.aborted) {
        throw new Error("Script aborted");
      }
      const result = await this.client.call(toolName, args, instance, internalSignal);
      return result.data;
    };
    context.__gw_search = async (toolType: string) => {
      if (internalSignal.aborted) {
        throw new Error("Script aborted");
      }
      return await this.client.listToolsByType(toolType, internalSignal);
    };
    context.__gw_detail = async (toolName: string) => {
      if (internalSignal.aborted) {
        throw new Error("Script aborted");
      }
      return await this.client.getToolDetail(toolName, internalSignal);
    };

    // Console capture
    context.__console_log = (...args: unknown[]) => {
      logs.push(args.map(String).join(" "));
    };
    context.__console_warn = (...args: unknown[]) => {
      logs.push(`[warn] ${args.map(String).join(" ")}`);
    };
    context.__console_error = (...args: unknown[]) => {
      logs.push(`[error] ${args.map(String).join(" ")}`);
    };

    // Scoped filesystem — each method is an outer-realm closure
    context.__fs = scopedFs;

    // Timers — needed for async patterns, injected as private refs
    context.__setTimeout = setTimeout;
    context.__clearTimeout = clearTimeout;

    vm.createContext(context);

    // Inside the vm context, create inner-realm wrapper functions so user code
    // cannot traverse .constructor chains back to the host-realm Function.
    // Then delete the raw outer-realm references from globalThis.
    vm.runInContext(
      `'use strict';
      // Wrap outer-realm functions with inner-realm arrow functions to prevent
      // .constructor chain traversal back to the host Function constructor.
      // Use an IIFE to capture references before deleting the raw globals.
      (function() {
        var gc = __gw_call, gs = __gw_search, gd = __gw_detail;
        var cl = __console_log, cw = __console_warn, ce = __console_error;
        var sfs = __fs, st = __setTimeout, ct = __clearTimeout;

        globalThis.gateway_call = async (name, args, instance) => gc(name, args, instance);
        globalThis.list_tools_for_tool_type = async (toolType) => gs(toolType);
        globalThis.get_tool_detail = async (toolName) => gd(toolName);
        globalThis.console = {
          log: (...a) => cl(...a),
          warn: (...a) => cw(...a),
          error: (...a) => ce(...a),
        };
        globalThis.fs = {
          readFileSync: (p, e) => sfs.readFileSync(p, e),
          writeFileSync: (p, d) => sfs.writeFileSync(p, d),
          mkdirSync: (p, o) => sfs.mkdirSync(p, o),
          readdirSync: (p) => sfs.readdirSync(p),
          existsSync: (p) => sfs.existsSync(p),
        };
        globalThis.setTimeout = (fn, ms) => st(fn, ms);
        globalThis.clearTimeout = (id) => ct(id);

        // Remove raw outer-realm references from globalThis
        delete globalThis.__gw_call;
        delete globalThis.__gw_search;
        delete globalThis.__gw_detail;
        delete globalThis.__console_log;
        delete globalThis.__console_warn;
        delete globalThis.__console_error;
        delete globalThis.__fs;
        delete globalThis.__setTimeout;
        delete globalThis.__clearTimeout;
      })();
      `,
      context,
    );

    // Wrap code in an async IIFE so top-level await works
    const wrappedCode = `(async () => {\n${code}\n})()`;

    let script: vm.Script;
    try {
      script = new vm.Script(wrappedCode, {
        filename: "execute_script",
      });
    } catch (err) {
      signal?.removeEventListener("abort", onExternalAbort);
      throw new Error(`Script compilation error: ${(err as Error).message}`);
    }

    let returnValue: unknown;
    try {
      // vm.Script.timeout only applies to synchronous execution.
      // For async code we use AbortController + setTimeout for the timeout.
      const result = script.runInContext(context);

      // result is the Promise from the async IIFE.
      // Race it against a promise that rejects when the internal signal fires
      // (either from the timeout or from an external caller abort).
      const timeoutId = setTimeout(
        () => controller.abort(new Error("Script execution timed out")),
        this.timeoutMs,
      );

      try {
        returnValue = await Promise.race([
          result,
          new Promise<never>((_, reject) => {
            const rejectWithReason = () => {
              const reason = internalSignal.reason;
              const msg =
                reason instanceof Error
                  ? reason.message
                  : String(reason ?? "");
              if (msg.includes("timed out")) {
                reject(new Error("Script execution timed out"));
              } else {
                reject(new Error("Script aborted"));
              }
            };
            if (internalSignal.aborted) {
              rejectWithReason();
              return;
            }
            internalSignal.addEventListener("abort", rejectWithReason, {
              once: true,
            });
          }),
        ]);
      } finally {
        clearTimeout(timeoutId);
        controller.abort(); // no-op if already aborted; ensures cleanup
        signal?.removeEventListener("abort", onExternalAbort);
      }
    } catch (err) {
      // Clean up listeners if an error escapes the inner try block above
      // (e.g. script compilation threw before the timeout was registered).
      signal?.removeEventListener("abort", onExternalAbort);

      const message = (err as Error).message ?? String(err);
      if (
        message.includes("Script execution timed out") ||
        message.includes("timed out")
      ) {
        throw new Error("Script execution timed out");
      }
      if (
        message.includes("Script aborted") ||
        message.includes("operation was aborted") ||
        (err as { name?: string }).name === "AbortError"
      ) {
        throw new Error("Script execution aborted");
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
