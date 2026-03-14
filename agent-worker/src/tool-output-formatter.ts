/**
 * Tool output formatting utilities.
 *
 * Extracts text from pi-mono tool execution results and formats tool
 * arguments and output for human-readable streaming to the UI.
 */

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ToolExecutionTrace {
  toolName: string;
  args: unknown;
  updates: string[];
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/**
 * Format tool arguments for display. Returns empty string for empty/nil args.
 */
export function formatToolArgs(args: unknown): string {
  if (args === null || args === undefined) return "";
  if (typeof args === "string") return args.trim();

  if (Array.isArray(args)) {
    if (args.length === 0) return "";
    return safeJSONStringify(args);
  }

  if (typeof args === "object") {
    if (Object.keys(args as Record<string, unknown>).length === 0) return "";
    return safeJSONStringify(args);
  }

  return String(args);
}

/**
 * Format tool output by combining streaming updates with the final result.
 * Deduplicates consecutive identical entries.
 */
export function formatToolOutput(updates: string[], result: unknown): string {
  const parts: string[] = [];

  for (const update of updates) {
    const trimmed = update.trim();
    if (trimmed) parts.push(trimmed);
  }

  const resultText = extractToolText(result).trim();
  if (resultText) {
    parts.push(resultText);
  } else if (result && typeof result === "object") {
    if (Array.isArray(result)) {
      if (result.length > 0) parts.push(safeJSONStringify(result));
    } else if (Object.keys(result as Record<string, unknown>).length > 0) {
      parts.push(safeJSONStringify(result));
    }
  }

  const deduped: string[] = [];
  for (const part of parts) {
    if (deduped[deduped.length - 1] !== part) {
      deduped.push(part);
    }
  }

  return deduped.join("\n");
}

/**
 * Extract human-readable text from a tool result value.
 * Recursively walks content arrays, text/output/error fields, etc.
 */
export function extractToolText(value: unknown): string {
  const parts = collectTextParts(value).map((part) => part.trim()).filter(Boolean);
  return parts.join("\n");
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

function collectTextParts(value: unknown): string[] {
  if (value === null || value === undefined) return [];
  if (typeof value === "string") return [value];
  if (Array.isArray(value)) return value.flatMap((entry) => collectTextParts(entry));
  if (typeof value !== "object") return [];

  const obj = value as Record<string, unknown>;
  const parts: string[] = [];

  if (typeof obj.text === "string") parts.push(obj.text);
  if (typeof obj.output === "string") parts.push(obj.output);
  if (typeof obj.message === "string") parts.push(obj.message);
  if (typeof obj.error === "string") parts.push(obj.error);
  if (typeof obj.stderr === "string") parts.push(obj.stderr);

  if (obj.content !== undefined) {
    parts.push(...collectContentText(obj.content));
  }
  if (obj.partialResult !== undefined) {
    parts.push(...collectTextParts(obj.partialResult));
  }
  if (obj.result !== undefined) {
    parts.push(...collectTextParts(obj.result));
  }
  if (obj.details !== undefined) {
    parts.push(...collectTextParts(obj.details));
  }

  return parts;
}

function collectContentText(content: unknown): string[] {
  if (!Array.isArray(content)) {
    return collectTextParts(content);
  }

  const parts: string[] = [];
  for (const item of content) {
    if (!item || typeof item !== "object") {
      if (typeof item === "string") parts.push(item);
      continue;
    }

    const contentItem = item as Record<string, unknown>;
    if (contentItem.type === "text" && typeof contentItem.text === "string") {
      parts.push(contentItem.text);
      continue;
    }
    if (contentItem.type === "thinking" && typeof contentItem.thinking === "string") {
      parts.push(contentItem.thinking);
      continue;
    }

    parts.push(...collectTextParts(contentItem));
  }

  return parts;
}

function safeJSONStringify(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}
