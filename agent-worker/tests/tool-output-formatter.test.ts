import { describe, it, expect } from "vitest";
import {
  formatToolArgs,
  formatToolOutput,
  extractToolText,
} from "../src/tool-output-formatter.js";

// ---------------------------------------------------------------------------
// formatToolArgs
// ---------------------------------------------------------------------------

describe("formatToolArgs", () => {
  it("returns empty string for null", () => {
    expect(formatToolArgs(null)).toBe("");
  });

  it("returns empty string for undefined", () => {
    expect(formatToolArgs(undefined)).toBe("");
  });

  it("returns trimmed string for string args", () => {
    expect(formatToolArgs("  uptime  ")).toBe("uptime");
  });

  it("returns empty string for empty string", () => {
    expect(formatToolArgs("")).toBe("");
  });

  it("returns empty string for empty array", () => {
    expect(formatToolArgs([])).toBe("");
  });

  it("returns JSON for non-empty array", () => {
    const result = formatToolArgs(["a", "b"]);
    expect(JSON.parse(result)).toEqual(["a", "b"]);
  });

  it("returns empty string for empty object", () => {
    expect(formatToolArgs({})).toBe("");
  });

  it("returns JSON for non-empty object", () => {
    const result = formatToolArgs({ command: "uptime" });
    expect(result).toContain('"command"');
    expect(result).toContain('"uptime"');
  });

  it("returns String() for other types", () => {
    expect(formatToolArgs(42)).toBe("42");
    expect(formatToolArgs(true)).toBe("true");
  });
});

// ---------------------------------------------------------------------------
// extractToolText
// ---------------------------------------------------------------------------

describe("extractToolText", () => {
  it("returns empty string for null/undefined", () => {
    expect(extractToolText(null)).toBe("");
    expect(extractToolText(undefined)).toBe("");
  });

  it("extracts plain string", () => {
    expect(extractToolText("hello")).toBe("hello");
  });

  it("extracts text from content array with text items", () => {
    const value = {
      content: [
        { type: "text", text: "line 1" },
        { type: "text", text: "line 2" },
      ],
    };
    expect(extractToolText(value)).toBe("line 1\nline 2");
  });

  it("extracts thinking content", () => {
    const value = {
      content: [{ type: "thinking", thinking: "I am reasoning" }],
    };
    expect(extractToolText(value)).toBe("I am reasoning");
  });

  it("extracts from text field", () => {
    expect(extractToolText({ text: "hello" })).toBe("hello");
  });

  it("extracts from output field", () => {
    expect(extractToolText({ output: "stdout" })).toBe("stdout");
  });

  it("extracts from error field", () => {
    expect(extractToolText({ error: "fail" })).toBe("fail");
  });

  it("extracts from stderr field", () => {
    expect(extractToolText({ stderr: "warning" })).toBe("warning");
  });

  it("extracts from message field", () => {
    expect(extractToolText({ message: "info" })).toBe("info");
  });

  it("recurses into partialResult", () => {
    const value = { partialResult: { text: "partial" } };
    expect(extractToolText(value)).toBe("partial");
  });

  it("recurses into result", () => {
    const value = { result: { output: "done" } };
    expect(extractToolText(value)).toBe("done");
  });

  it("recurses into details", () => {
    const value = { details: { text: "detail" } };
    expect(extractToolText(value)).toBe("detail");
  });

  it("handles arrays by flattening", () => {
    const value = ["a", "b", "c"];
    expect(extractToolText(value)).toBe("a\nb\nc");
  });

  it("handles non-text content items via fallback", () => {
    const value = {
      content: [{ type: "image", url: "http://example.com" }],
    };
    // No text extracted from image items
    expect(extractToolText(value)).toBe("");
  });

  it("handles string items in content array", () => {
    const value = { content: ["raw string"] };
    expect(extractToolText(value)).toBe("raw string");
  });

  it("skips empty/whitespace parts", () => {
    const value = { content: [{ type: "text", text: "  " }, { type: "text", text: "real" }] };
    expect(extractToolText(value)).toBe("real");
  });

  it("handles non-array content by treating as nested value", () => {
    const value = { content: { text: "nested" } };
    expect(extractToolText(value)).toBe("nested");
  });
});

// ---------------------------------------------------------------------------
// formatToolOutput
// ---------------------------------------------------------------------------

describe("formatToolOutput", () => {
  it("returns empty string with no updates and null result", () => {
    expect(formatToolOutput([], null)).toBe("");
  });

  it("includes trimmed updates", () => {
    const result = formatToolOutput(["  partial  ", "  data  "], null);
    expect(result).toBe("partial\ndata");
  });

  it("skips empty updates", () => {
    const result = formatToolOutput(["", "  ", "real"], null);
    expect(result).toBe("real");
  });

  it("appends extracted text from result", () => {
    const result = formatToolOutput([], { text: "final" });
    expect(result).toBe("final");
  });

  it("falls back to JSON for non-text object result", () => {
    const result = formatToolOutput([], { status: "ok", code: 200 });
    expect(result).toContain('"status"');
    expect(result).toContain('"ok"');
  });

  it("falls back to JSON for non-empty array result", () => {
    const result = formatToolOutput([], [1, 2, 3]);
    expect(JSON.parse(result)).toEqual([1, 2, 3]);
  });

  it("returns empty for empty object result", () => {
    expect(formatToolOutput([], {})).toBe("");
  });

  it("returns empty for empty array result", () => {
    expect(formatToolOutput([], [])).toBe("");
  });

  it("deduplicates consecutive identical entries", () => {
    const result = formatToolOutput(["same", "same", "different", "different"], null);
    expect(result).toBe("same\ndifferent");
  });

  it("combines updates and result text", () => {
    const result = formatToolOutput(
      ["partial output"],
      { content: [{ type: "text", text: "final output" }] },
    );
    expect(result).toBe("partial output\nfinal output");
  });

  it("deduplicates update and identical result", () => {
    const result = formatToolOutput(
      ["same text"],
      { text: "same text" },
    );
    expect(result).toBe("same text");
  });
});
