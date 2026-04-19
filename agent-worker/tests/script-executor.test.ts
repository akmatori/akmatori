import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { ScriptExecutor, type ScriptExecutorOptions, detectCommonMistakes } from "../src/script-executor.js";
import type { GatewayClient, CallResult } from "../src/gateway-client.js";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";

// ---------------------------------------------------------------------------
// Mock GatewayClient
// ---------------------------------------------------------------------------

function createMockClient(overrides?: Partial<GatewayClient>): GatewayClient {
  return {
    call: vi.fn(async (_toolName: string, _args?: Record<string, unknown>, _instance?: string) => ({
      data: { status: "ok", result: "mock-result" },
    } as CallResult)),
    listToolsByType: vi.fn(async () => ({
      tools: [
        { name: "ssh.execute_command", description: "Execute SSH", instances: ["prod-ssh"] },
      ],
    })),
    getToolDetail: vi.fn(async () => ({
      name: "ssh.execute_command",
      description: "Execute SSH command",
      input_schema: { command: { type: "string" } },
      instances: [{ id: 1, logical_name: "prod-ssh", name: "Production SSH" }],
    })),
    ...overrides,
  } as unknown as GatewayClient;
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

let tmpDir: string;

function createExecutor(
  clientOverrides?: Partial<GatewayClient>,
  opts?: Partial<ScriptExecutorOptions>,
): { executor: ScriptExecutor; client: GatewayClient } {
  const client = createMockClient(clientOverrides);
  const executor = new ScriptExecutor({
    client,
    workDir: tmpDir,
    ...opts,
  });
  return { executor, client };
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("ScriptExecutor", () => {
  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "script-exec-test-"));
  });

  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  describe("detectCommonMistakes", () => {
    it("should reject require('fs')", async () => {
      const { executor } = createExecutor();
      await expect(
        executor.execute('const fs = require("fs"); return fs.readFileSync("/tmp/test");'),
      ).rejects.toThrow("require() is not available in the sandbox");
    });

    it("should reject require('http')", async () => {
      const { executor } = createExecutor();
      await expect(
        executor.execute('const http = require("http");'),
      ).rejects.toThrow("require() is not available in the sandbox");
    });

    it("should reject import statements", async () => {
      const { executor } = createExecutor();
      await expect(
        executor.execute('import fs from "fs";\nreturn fs.readFileSync("/tmp/test");'),
      ).rejects.toThrow("import statements are not available in the sandbox");
    });

    it("should not flag comments containing import", async () => {
      const { executor } = createExecutor();
      const result = await executor.execute('// import fs from "fs"\nreturn 42;');
      expect(result.output).toBe("42");
    });

    it("should not flag valid code without require/import", async () => {
      const { executor } = createExecutor();
      // This will fail because test.txt doesn't exist, but it should be a runtime error, not a pre-flight error
      await expect(
        executor.execute('const data = fs.readFileSync("test.txt"); return "ok";'),
      ).rejects.toThrow("Script execution error: ENOENT");
    });

    it("should return null for valid code (unit test)", () => {
      expect(detectCommonMistakes('const x = await gateway_call("ssh.execute_command", {}); return x;')).toBeNull();
      expect(detectCommonMistakes('fs.readFileSync("test.txt")')).toBeNull();
    });

    it("should detect require with single quotes", () => {
      expect(detectCommonMistakes("const fs = require('fs');")).toContain("require() is not available");
    });

    it("should detect import default syntax", () => {
      expect(detectCommonMistakes('import fs from "fs";')).toContain("import statements are not available");
    });

    it("should detect import named syntax", () => {
      expect(detectCommonMistakes('import { readFileSync } from "fs";')).toContain("import statements are not available");
    });
  });

  describe("basic script execution", () => {
    it("should execute simple return value", async () => {
      const { executor } = createExecutor();
      const result = await executor.execute('return 42');
      expect(result.output).toBe("42");
    });

    it("should execute and return string", async () => {
      const { executor } = createExecutor();
      const result = await executor.execute('return "hello world"');
      expect(result.output).toBe("hello world");
    });

    it("should return JSON for object return values", async () => {
      const { executor } = createExecutor();
      const result = await executor.execute('return { a: 1, b: "two" }');
      const parsed = JSON.parse(result.output);
      expect(parsed.a).toBe(1);
      expect(parsed.b).toBe("two");
    });

    it("should capture console.log output when no return value", async () => {
      const { executor } = createExecutor();
      const result = await executor.execute('console.log("line 1"); console.log("line 2");');
      expect(result.output).toBe("line 1\nline 2");
      expect(result.logs).toEqual(["line 1", "line 2"]);
    });

    it("should capture console.warn and console.error", async () => {
      const { executor } = createExecutor();
      const result = await executor.execute('console.warn("warning"); console.error("error");');
      expect(result.logs).toEqual(["[warn] warning", "[error] error"]);
    });

    it("should return '(no output)' for empty scripts", async () => {
      const { executor } = createExecutor();
      const result = await executor.execute('const x = 1;');
      expect(result.output).toBe("(no output)");
    });

    it("should support top-level await", async () => {
      const { executor } = createExecutor();
      const result = await executor.execute('const x = await Promise.resolve(99); return x;');
      expect(result.output).toBe("99");
    });
  });

  describe("gateway_call within script", () => {
    it("should call gateway_call and return result", async () => {
      const { executor, client } = createExecutor({
        call: vi.fn(async () => ({ data: { uptime: "42 days" } })) as any,
      });

      const result = await executor.execute(
        'const r = await gateway_call("ssh.execute_command", { command: "uptime" }, "prod-ssh"); return r;',
      );

      expect(client.call).toHaveBeenCalledWith(
        "ssh.execute_command",
        { command: "uptime" },
        "prod-ssh",
        expect.any(AbortSignal),
      );
      const parsed = JSON.parse(result.output);
      expect(parsed.uptime).toBe("42 days");
    });

    it("should call gateway_call without instance", async () => {
      const { executor, client } = createExecutor();

      await executor.execute(
        'await gateway_call("zabbix.get_problems", { severity_min: 3 });',
      );

      expect(client.call).toHaveBeenCalledWith(
        "zabbix.get_problems",
        { severity_min: 3 },
        undefined,
        expect.any(AbortSignal),
      );
    });

    it("should handle multiple gateway_call invocations", async () => {
      let callCount = 0;
      const { executor, client } = createExecutor({
        call: vi.fn(async (name: string) => {
          callCount++;
          return { data: { call: callCount, tool: name } };
        }) as any,
      });

      const result = await executor.execute(`
        const r1 = await gateway_call("ssh.execute_command", { command: "uptime" });
        const r2 = await gateway_call("zabbix.get_problems", { severity_min: 3 });
        return [r1, r2];
      `);

      expect(client.call).toHaveBeenCalledTimes(2);
      const parsed = JSON.parse(result.output);
      expect(parsed).toHaveLength(2);
      expect(parsed[0].call).toBe(1);
      expect(parsed[1].call).toBe(2);
    });
  });

  describe("list_tools_for_tool_type and get_tool_detail within script", () => {
    it("should call list_tools_for_tool_type", async () => {
      const { executor, client } = createExecutor();

      const result = await executor.execute(
        'const tools = await list_tools_for_tool_type("ssh"); return tools;',
      );

      expect(client.listToolsByType).toHaveBeenCalledWith("ssh", expect.any(AbortSignal));
      const parsed = JSON.parse(result.output);
      expect(parsed.tools).toHaveLength(1);
      expect(parsed.tools[0].name).toBe("ssh.execute_command");
    });

    it("should call get_tool_detail", async () => {
      const { executor, client } = createExecutor();

      const result = await executor.execute(
        'const detail = await get_tool_detail("ssh.execute_command"); return detail;',
      );

      expect(client.getToolDetail).toHaveBeenCalledWith("ssh.execute_command", expect.any(AbortSignal));
      const parsed = JSON.parse(result.output);
      expect(parsed.name).toBe("ssh.execute_command");
    });
  });

  describe("scoped filesystem access", () => {
    it("should write and read files within workDir", async () => {
      const { executor } = createExecutor();

      const result = await executor.execute(`
        fs.writeFileSync("test-output.txt", "hello from script");
        return fs.readFileSync("test-output.txt");
      `);

      expect(result.output).toBe("hello from script");
      // Verify file actually exists on disk
      expect(fs.existsSync(path.join(tmpDir, "test-output.txt"))).toBe(true);
    });

    it("should support existsSync", async () => {
      const { executor } = createExecutor();
      fs.writeFileSync(path.join(tmpDir, "existing.txt"), "data");

      const result = await executor.execute(`
        return { exists: fs.existsSync("existing.txt"), missing: fs.existsSync("nope.txt") };
      `);

      const parsed = JSON.parse(result.output);
      expect(parsed.exists).toBe(true);
      expect(parsed.missing).toBe(false);
    });

    it("should support readdirSync", async () => {
      const { executor } = createExecutor();
      fs.writeFileSync(path.join(tmpDir, "a.txt"), "a");
      fs.writeFileSync(path.join(tmpDir, "b.txt"), "b");

      const result = await executor.execute('return fs.readdirSync(".");');
      const parsed = JSON.parse(result.output);
      expect(parsed).toContain("a.txt");
      expect(parsed).toContain("b.txt");
    });

    it("should support mkdirSync with recursive", async () => {
      const { executor } = createExecutor();

      await executor.execute('fs.mkdirSync("sub/dir", { recursive: true });');
      expect(fs.existsSync(path.join(tmpDir, "sub/dir"))).toBe(true);
    });

    it("should auto-create parent directories for writeFileSync", async () => {
      const { executor } = createExecutor();

      await executor.execute('fs.writeFileSync("deep/nested/file.txt", "deep data");');
      expect(fs.readFileSync(path.join(tmpDir, "deep/nested/file.txt"), "utf-8")).toBe("deep data");
    });

    it("should deny access outside workDir", async () => {
      const { executor } = createExecutor();

      await expect(
        executor.execute('fs.readFileSync("../../etc/passwd");'),
      ).rejects.toThrow("outside workspace");
    });

    it("should deny absolute paths outside workDir", async () => {
      const { executor } = createExecutor();

      await expect(
        executor.execute('fs.readFileSync("/etc/passwd");'),
      ).rejects.toThrow("outside workspace");
    });
  });

  describe("abort signal handling", () => {
    it("should reject immediately for pre-aborted signal without running script", async () => {
      const { executor } = createExecutor();
      const controller = new AbortController();
      controller.abort();

      await expect(
        executor.execute('fs.writeFileSync("should-not-exist.txt", "bad");', controller.signal),
      ).rejects.toThrow("Script execution aborted");

      // Verify no file was written — script body should not have executed
      expect(fs.existsSync(path.join(tmpDir, "should-not-exist.txt"))).toBe(false);
    });

    it("should classify standard AbortError as abort, not generic error", async () => {
      const { executor } = createExecutor({
        call: vi.fn(async (_name: string, _args?: Record<string, unknown>, _inst?: string, signal?: AbortSignal) => {
          // Simulate a long call that gets aborted by external signal
          return new Promise<any>((_resolve, reject) => {
            signal?.addEventListener("abort", () => {
              const err = new DOMException("The operation was aborted", "AbortError");
              reject(err);
            }, { once: true });
          });
        }) as any,
      });

      const controller = new AbortController();
      // Abort externally after a short delay
      setTimeout(() => controller.abort(), 50);

      await expect(
        executor.execute(
          'await gateway_call("ssh.execute_command", { command: "sleep 99" });',
          controller.signal,
        ),
      ).rejects.toThrow("Script execution aborted");
    }, 10_000);

    it("should classify custom abort reasons as abort, not generic error", async () => {
      const { executor } = createExecutor({
        call: vi.fn(async () => {
          return new Promise<any>((resolve) => {
            // Never resolves — will be aborted externally
            setTimeout(resolve, 60_000);
          });
        }) as any,
      });

      const controller = new AbortController();
      // Abort with a custom string reason after a short delay
      setTimeout(() => controller.abort("user cancelled"), 50);

      await expect(
        executor.execute('await gateway_call("ssh.execute_command", { command: "sleep 99" });', controller.signal),
      ).rejects.toThrow("Script execution aborted");
    }, 10_000);
  });

  describe("timeout enforcement", () => {
    it("should timeout long-running scripts", async () => {
      const { executor } = createExecutor({}, { timeoutMs: 200 });

      await expect(
        executor.execute('await new Promise(resolve => setTimeout(resolve, 5000)); return "done";'),
      ).rejects.toThrow("timed out");
    }, 10_000);

    it("should not crash with unhandled rejection when timeout fires during in-flight gateway_call", async () => {
      // Reproduce the crash: script awaits a gateway_call that never settles,
      // timeout fires, controller.abort() fires the in-flight request's abort
      // listener which calls reject() on a promise nobody is awaiting anymore.
      // Without the result.catch(() => {}) fix, this causes an unhandled rejection
      // that crashes Node.js 22.
      let resolveGateway!: () => void;
      const gatewayCallStarted = new Promise<void>((r) => { resolveGateway = r; });

      const { executor } = createExecutor({
        call: vi.fn((_toolName: string, _args?: Record<string, unknown>, _instance?: string, signal?: AbortSignal) =>
          new Promise<CallResult>((_resolve, reject) => {
            resolveGateway(); // notify that the gateway call has started
            signal?.addEventListener("abort", () => reject(new Error("Request aborted")), { once: true });
          })
        ) as any,
      }, { timeoutMs: 200 });

      const unhandledErrors: Error[] = [];
      const handler = (err: Error) => unhandledErrors.push(err);
      process.on("unhandledRejection", handler);

      try {
        // Script starts a gateway_call that will never resolve (only aborted on timeout)
        const execution = executor.execute(
          'await gateway_call("ssh.execute_command", { command: "uptime" }); return "done";',
        );

        // Wait for the gateway call to actually start before proceeding
        await gatewayCallStarted;

        // Execution should reject with timeout, not crash the process
        await expect(execution).rejects.toThrow("timed out");

        // Give microtasks a chance to fire any lingering rejections
        await new Promise<void>((resolve) => setImmediate(resolve));

        expect(unhandledErrors).toHaveLength(0);
      } finally {
        process.off("unhandledRejection", handler);
      }
    }, 10_000);
  });

  describe("error handling", () => {
    it("should throw on syntax errors", async () => {
      const { executor } = createExecutor();

      await expect(
        executor.execute('return {{{'),
      ).rejects.toThrow("Script compilation error");
    });

    it("should clean up abort listener on compilation error", async () => {
      const { executor } = createExecutor();
      const controller = new AbortController();
      const signal = controller.signal;
      const removeSpy = vi.spyOn(signal, "removeEventListener");

      await expect(
        executor.execute('return {{{', signal),
      ).rejects.toThrow("Script compilation error");

      // Verify that removeEventListener was called to clean up the listener
      expect(removeSpy).toHaveBeenCalledWith("abort", expect.any(Function));
      removeSpy.mockRestore();
    });

    it("should throw on runtime errors", async () => {
      const { executor } = createExecutor();

      await expect(
        executor.execute('throw new Error("custom error");'),
      ).rejects.toThrow("Script execution error: custom error");
    });

    it("should throw on gateway_call errors within script", async () => {
      const { executor } = createExecutor({
        call: vi.fn(async () => { throw new Error("Connection refused"); }) as any,
      });

      await expect(
        executor.execute('await gateway_call("ssh.execute_command", { command: "uptime" });'),
      ).rejects.toThrow("Connection refused");
    });

    it("should propagate undefined variable errors", async () => {
      const { executor } = createExecutor();

      await expect(
        executor.execute('return undefinedVariable.foo;'),
      ).rejects.toThrow("Script execution error");
    });
  });

  describe("return value vs console output", () => {
    it("should prefer return value over console output", async () => {
      const { executor } = createExecutor();

      const result = await executor.execute(`
        console.log("log line");
        return "return value";
      `);

      expect(result.output).toBe("return value");
      expect(result.logs).toEqual(["log line"]);
    });

    it("should return array values as JSON", async () => {
      const { executor } = createExecutor();

      const result = await executor.execute('return [1, 2, 3];');
      expect(JSON.parse(result.output)).toEqual([1, 2, 3]);
    });

    it("should handle null return", async () => {
      const { executor } = createExecutor();

      const result = await executor.execute('console.log("only log"); return null;');
      // null return → falls through to logs
      expect(result.output).toBe("only log");
    });
  });
});
