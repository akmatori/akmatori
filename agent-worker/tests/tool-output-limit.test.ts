import { describe, it, expect, beforeEach, afterEach } from "vitest";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import {
  buildHeadPreview,
  limitToolOutput,
  createToolOutputLimitExtension,
  TOOL_OUTPUT_MAX_BYTES,
} from "../src/tool-output-limit.js";

describe("buildHeadPreview", () => {
  it("clips long lines so later lines still show", () => {
    const preview = buildHeadPreview("x".repeat(100_000) + "\nnext line", 10_000);
    expect(preview).toContain("[line truncated, 100000 chars]");
    expect(preview).toContain("next line");
  });

  it("never cuts inside a multi-byte character", () => {
    const preview = buildHeadPreview("é".repeat(1000), 101);
    expect(preview).toBe("é".repeat(50));
  });
});

describe("limitToolOutput", () => {
  let tmpDir: string;
  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "tool-output-limit-test-"));
  });
  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it("returns small output unchanged and writes no file", () => {
    expect(limitToolOutput("hello", { name: "t", workDir: tmpDir })).toEqual({ text: "hello" });
    expect(fs.existsSync(path.join(tmpDir, "tool_outputs"))).toBe(false);
  });

  it("saves oversized output and returns a bounded preview", () => {
    const text = "line\n".repeat(30_000);
    const out = limitToolOutput(text, { name: "execute_script", workDir: tmpDir });
    expect(out.outputFile).toMatch(/tool_outputs\/execute_script_\d+\.txt$/);
    expect(fs.readFileSync(out.outputFile!, "utf-8")).toBe(text);
    expect(Buffer.byteLength(out.text, "utf-8")).toBeLessThan(TOOL_OUTPUT_MAX_BYTES + 1024);
    expect(out.text).toContain(`Full output saved to: ${out.outputFile}`);
  });

  it("still truncates when there is no workDir", () => {
    const out = limitToolOutput("y".repeat(200_000), { name: "t" });
    expect(out.outputFile).toBeUndefined();
    expect(out.text).toContain("The rest was dropped");
    expect(out.text.length).toBeLessThan(10_000);
  });
});

describe("createToolOutputLimitExtension", () => {
  type Handler = (event: any) => any;

  function register(maxBytes: number): Handler {
    let handler: Handler | undefined;
    const pi = { on: (name: string, h: Handler) => { if (name === "tool_result") handler = h; } };
    createToolOutputLimitExtension({ maxBytes })(pi as any);
    return handler!;
  }

  const event = (text: string, extra: Record<string, unknown> = {}) => ({
    type: "tool_result",
    toolName: "subagent",
    toolCallId: "tc1",
    input: {},
    content: [{ type: "text", text }],
    isError: false,
    ...extra,
  });

  it("leaves results under the cap alone", () => {
    expect(register(1000)(event("small"))).toBeUndefined();
  });

  it("caps an oversized top-level result", () => {
    const res = register(1000)(event("z".repeat(5000)));
    expect(res.content).toHaveLength(1);
    expect(res.content[0].text).toContain("[output truncated:");
    expect(res.content[0].text.length).toBeLessThan(2000);
  });

  it("keeps image parts", () => {
    const image = { type: "image", data: "AAAA", mimeType: "image/png" };
    const res = register(1000)({ ...event("z".repeat(5000)), content: [{ type: "text", text: "z".repeat(5000) }, image] });
    expect(res.content[1]).toEqual(image);
  });

  it("skips nested calls (their result goes to a script, not the model)", () => {
    expect(register(1000)(event("z".repeat(5000), { parentToolCallId: "parent" }))).toBeUndefined();
  });
});
