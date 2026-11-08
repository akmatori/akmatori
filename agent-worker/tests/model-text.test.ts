import { describe, it, expect } from "vitest";
import { toModelText, jsonTextToModelText } from "../src/model-text.js";

describe("toModelText", () => {
  it("shows multi-line strings as indented blocks, not escaped JSON", () => {
    const out = toModelText({
      results: [{ server: "web-01", success: true, stdout: "line one\nline two\n" }],
      summary: { total: 1, succeeded: 1 },
    });
    expect(out).toBe(
      [
        "results:",
        "  -",
        "    server: web-01",
        "    success: true",
        "    stdout: |",
        "      line one",
        "      line two",
        "summary:",
        "  total: 1",
        "  succeeded: 1",
      ].join("\n"),
    );
    expect(out).not.toContain("\\n");
  });

  it("keeps scalars, empty values and strings", () => {
    expect(toModelText("raw\ntext")).toBe("raw\ntext");
    expect(toModelText({ a: "", b: null, c: [], d: {}, e: 3 })).toBe('a: ""\nb: null\nc: []\nd: {}\ne: 3');
    expect(toModelText([1, "x"])).toBe("- 1\n- x");
  });
});

describe("jsonTextToModelText", () => {
  it("renders JSON text and leaves other text alone", () => {
    expect(jsonTextToModelText('{"out":"a\\nb"}')).toBe("out: |\n  a\n  b");
    expect(jsonTextToModelText("plain output")).toBe("plain output");
    expect(jsonTextToModelText("{not json")).toBe("{not json");
  });
});
