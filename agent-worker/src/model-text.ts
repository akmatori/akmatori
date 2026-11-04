/**
 * Render structured tool results as plain text for the model.
 *
 * Tool results (SSH stdout, logs, configs) are mostly multi-line strings. As
 * JSON they reach the model with every line break escaped ("\n"), so a 200-line
 * journal becomes one unreadable line. This renders a YAML-like view instead:
 * multi-line strings become indented literal blocks, the rest stays compact.
 */

const INDENT = "  ";

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === "object" && !Array.isArray(v);
}

function scalar(v: unknown): string {
  if (v === null || v === undefined) return "null";
  if (typeof v === "string") return v === "" || /^\s|\s$/.test(v) ? JSON.stringify(v) : v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return JSON.stringify(v);
}

function block(text: string, pad: string): string {
  const body = text.replace(/\n$/, "");
  return "|\n" + body.split("\n").map((l) => pad + l).join("\n");
}

function render(v: unknown, depth: number): string {
  const pad = INDENT.repeat(depth);
  if (typeof v === "string" && v.includes("\n")) return block(v, pad);
  if (Array.isArray(v)) {
    if (v.length === 0) return "[]";
    return "\n" + v.map((item) => {
      const r = render(item, depth + 1);
      return isPlainObject(item) || Array.isArray(item)
        ? `${pad}-${r.startsWith("\n") ? r : " " + r}`
        : `${pad}- ${r}`;
    }).join("\n");
  }
  if (isPlainObject(v)) {
    const keys = Object.keys(v);
    if (keys.length === 0) return "{}";
    return "\n" + keys.map((k) => {
      const r = render(v[k], depth + 1);
      return `${pad}${k}:${r.startsWith("\n") ? r : " " + r}`;
    }).join("\n");
  }
  return scalar(v);
}

/** Plain-text view of a structured value. Strings are returned unchanged. */
export function toModelText(value: unknown): string {
  if (typeof value === "string") return value;
  const out = render(value, 0);
  return out.startsWith("\n") ? out.slice(1) : out;
}

/**
 * Plain-text view of a string that may hold JSON (a script's return value).
 * Non-JSON text is returned unchanged.
 */
export function jsonTextToModelText(text: string): string {
  const t = text.trimStart();
  if (!(t.startsWith("{") || t.startsWith("["))) return text;
  try {
    return toModelText(JSON.parse(text));
  } catch {
    return text;
  }
}
