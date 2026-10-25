/**
 * Size limits for tool output that goes back to the model.
 *
 * One oversized tool result stays in the context: compaction keeps the
 * newest messages, so the next provider request fails (Moonshot rejects
 * requests over 8 MB; others overflow the context window). Two layers:
 *
 * - `limitToolOutput()` — used by our own tools: saves the full text to
 *   `<workDir>/tool_outputs/` and returns a head preview plus the path.
 * - `createToolOutputLimitExtension()` — a pi `tool_result` hook that caps
 *   every top-level tool result, so tools we do not own (subagent, codemode,
 *   future MCP bridges) cannot flood the context either.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import type { ExtensionFactory } from "@earendil-works/pi-coding-agent";

/** Preview budget for our own tools (pi's bash/read use the same 50 KB). */
export const TOOL_OUTPUT_MAX_BYTES = 50 * 1024;

/** Hard cap for any tool result, enforced by the extension. */
export const TOOL_RESULT_HARD_MAX_BYTES = 256 * 1024;

/** Longest line kept in a preview; minified files are often one huge line. */
const PREVIEW_MAX_LINE_CHARS = 2000;

export interface LimitedOutput {
  text: string;
  /** Set when the output was over the limit and saved in full. */
  outputFile?: string;
}

/**
 * Cut `text` to at most `maxBytes` UTF-8 bytes. Long lines are clipped first,
 * so one minified line cannot use the whole budget, then the result is cut on
 * a character boundary.
 */
export function buildHeadPreview(text: string, maxBytes: number): string {
  const lines = text.split("\n").map((line) =>
    line.length > PREVIEW_MAX_LINE_CHARS
      ? `${line.slice(0, PREVIEW_MAX_LINE_CHARS)}… [line truncated, ${line.length} chars]`
      : line,
  );
  const clipped = lines.join("\n");
  const buf = Buffer.from(clipped, "utf-8");
  if (buf.length <= maxBytes) return clipped;
  // toString() drops a partial multi-byte sequence at the cut as U+FFFD; strip it.
  return buf.subarray(0, maxBytes).toString("utf-8").replace(/�$/, "");
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function writeFullOutput(workDir: string, name: string, text: string): string | undefined {
  try {
    const dir = path.join(workDir, "tool_outputs");
    fs.mkdirSync(dir, { recursive: true });
    const safeName = name.replace(/[^a-zA-Z0-9._-]/g, "_");
    const filePath = path.join(dir, `${safeName}_${Date.now()}.txt`);
    fs.writeFileSync(filePath, text, "utf-8");
    return filePath;
  } catch {
    // The preview still protects the context; losing the full copy is acceptable.
    return undefined;
  }
}

/**
 * Return `text` unchanged when it fits in `maxBytes`. Otherwise save the full
 * text under `<workDir>/tool_outputs/` (when `workDir` is set) and return a
 * head preview with a note that tells the model how to reach the rest.
 */
export function limitToolOutput(
  text: string,
  opts: { name: string; workDir?: string; maxBytes?: number },
): LimitedOutput {
  const maxBytes = opts.maxBytes ?? TOOL_OUTPUT_MAX_BYTES;
  const totalBytes = Buffer.byteLength(text, "utf-8");
  if (totalBytes <= maxBytes) return { text };

  const outputFile = opts.workDir ? writeFullOutput(opts.workDir, opts.name, text) : undefined;
  const where = outputFile
    ? `Full output saved to: ${outputFile}\n` +
      `Tip: read it with execute_script and filter or summarize inside the script — ` +
      `do not print raw data back.`
    : "The rest was dropped. Narrow the command or filter the data before printing it.";
  const notice =
    `\n\n... [output truncated: ${formatBytes(totalBytes)} total, ` +
    `showing the first ${formatBytes(maxBytes)}]\n${where}`;
  return { text: buildHeadPreview(text, maxBytes) + notice, outputFile };
}

/**
 * pi extension that caps every top-level tool result at `maxBytes` of text.
 * Nested calls (a codemode script calling a tool) are left alone: their
 * result goes to the script, not to the model, and the script's own result
 * passes through this hook.
 */
export function createToolOutputLimitExtension(opts: {
  workDir?: string;
  maxBytes?: number;
}): ExtensionFactory {
  const maxBytes = opts.maxBytes ?? TOOL_RESULT_HARD_MAX_BYTES;
  return (pi) => {
    pi.on("tool_result", (event) => {
      if (event.parentToolCallId) return undefined;
      let total = 0;
      for (const part of event.content) {
        if (part.type === "text") total += Buffer.byteLength(part.text, "utf-8");
      }
      if (total <= maxBytes) return undefined;

      const joined = event.content
        .filter((p) => p.type === "text")
        .map((p) => (p as { text: string }).text)
        .join("\n");
      const limited = limitToolOutput(joined, {
        name: event.toolName,
        workDir: opts.workDir,
        maxBytes,
      });
      const images = event.content.filter((p) => p.type !== "text");
      return { content: [{ type: "text" as const, text: limited.text }, ...images] };
    });
  };
}
