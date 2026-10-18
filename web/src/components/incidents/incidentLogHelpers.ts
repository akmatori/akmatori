// Parsing for the agent's full_log. The worker writes a plain-text stream:
// "🤔 " reasoning, "📝 " notes, "🛠️ Running: <tool>" when a call starts,
// "✅ Ran: <command>" / "❌ Failed: <command>" followed by the command body
// and an optional "Output:" block, and "--- Final Response ---" at the end.

export type TimelineStep =
  | { kind: 'note'; text: string }
  | { kind: 'running'; tools: string[] }
  | { kind: 'tool'; ok: boolean; command: string; output?: string };

const RAN = '✅ Ran:';
const FAILED = '❌ Failed:';
const RUNNING = '🛠️ Running:';
const FINAL = '--- Final Response ---';

const isSectionStart = (line: string) =>
  line.startsWith(RAN) ||
  line.startsWith(FAILED) ||
  line.startsWith('🤔 ') ||
  line.startsWith('📝 ') ||
  line.startsWith(RUNNING) ||
  line.startsWith('--- ');

// parseTimeline turns full_log into ordered steps. Consecutive text lines
// merge into one note (blank lines split notes), consecutive "Running" lines
// merge into one step, and the log stops at the final response, which the
// Summary tab shows on its own.
export function parseTimeline(log: string): TimelineStep[] {
  const steps: TimelineStep[] = [];
  let note: string[] = [];
  let tool: { ok: boolean; command: string[]; output: string[] | null } | null = null;
  let heredoc: string | null = null;

  const flushNote = () => {
    const text = note.join('\n').trim();
    if (text) steps.push({ kind: 'note', text });
    note = [];
  };
  const flushTool = () => {
    if (!tool) return;
    const output = tool.output?.join('\n').trim();
    steps.push({ kind: 'tool', ok: tool.ok, command: tool.command.join('\n').trim(), output: output || undefined });
    tool = null;
    heredoc = null;
  };

  for (const line of log.split('\n')) {
    if (line.startsWith(FINAL)) break;

    if (tool) {
      if (heredoc) {
        tool.command.push(line);
        if (line.replace(/["')]+$/, '') === heredoc) heredoc = null;
        continue;
      }
      if (!isSectionStart(line)) {
        if (tool.output) tool.output.push(line);
        else if (line === 'Output:') tool.output = [];
        else tool.command.push(line);
        continue;
      }
      flushTool();
    }

    if (line.startsWith(RAN) || line.startsWith(FAILED)) {
      flushNote();
      const ok = line.startsWith(RAN);
      const first = line.slice((ok ? RAN : FAILED).length).trim();
      heredoc = first.match(/<<[-'"\\]*(\w+)/)?.[1] ?? null;
      tool = { ok, command: [first], output: null };
    } else if (line.startsWith(RUNNING)) {
      flushNote();
      const name = line.slice(RUNNING.length).trim();
      const last = steps[steps.length - 1];
      if (last?.kind === 'running') last.tools.push(name);
      else steps.push({ kind: 'running', tools: [name] });
    } else if (line.trim() === '' || /^--- .+ ---$/.test(line)) {
      // Blank lines and "--- Section ---" headers only separate notes.
      flushNote();
    } else {
      note.push(line);
    }
  }
  flushTool();
  flushNote();
  return steps;
}

// summarizeTools renders ["a", "b", "a"] as "2× a, b".
export function summarizeTools(tools: string[]): string {
  const counts = new Map<string, number>();
  for (const t of tools) counts.set(t, (counts.get(t) ?? 0) + 1);
  return Array.from(counts, ([name, n]) => (n > 1 ? `${n}× ${name}` : name)).join(', ');
}

// cleanResponse strips the worker's [FINAL_RESULT] markers.
export function cleanResponse(response: string): string {
  return response.replace(/\[\/?FINAL_RESULT\]\n?/g, '').trim();
}
