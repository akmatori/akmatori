import { describe, it, expect } from 'vitest';
import { parseTimeline, summarizeTools, cleanResponse } from './incidentLogHelpers';

describe('parseTimeline', () => {
  it('merges text lines into notes and splits on blank lines', () => {
    expect(parseTimeline('🤔 look at alert\nmore detail\n--- Execution Log ---\n📝 next')).toEqual([
      { kind: 'note', text: '🤔 look at alert\nmore detail' },
      { kind: 'note', text: '📝 next' },
    ]);
  });

  it('parses tool calls with output and failure state', () => {
    const log = [
      '✅ Ran: zabbix.get_history',
      'item=cpu',
      'Output:',
      '42',
      '❌ Failed: ssh host uptime',
      'Output:',
      'timeout',
      '🤔 done',
    ].join('\n');
    expect(parseTimeline(log)).toEqual([
      { kind: 'tool', ok: true, command: 'zabbix.get_history\nitem=cpu', output: '42' },
      { kind: 'tool', ok: false, command: 'ssh host uptime', output: 'timeout' },
      { kind: 'note', text: '🤔 done' },
    ]);
  });

  it('keeps heredoc bodies in the command even when lines look like sections', () => {
    const log = ["✅ Ran: cat <<'EOF'", '--- not a section', 'EOF', 'Output:', 'ok'].join('\n');
    expect(parseTimeline(log)).toEqual([
      { kind: 'tool', ok: true, command: "cat <<'EOF'\n--- not a section\nEOF", output: 'ok' },
    ]);
  });

  it('groups consecutive Running lines and stops at the final response', () => {
    const log = ['🛠️ Running: ssh', '', '🛠️ Running: ssh', '--- Final Response ---', 'answer'].join('\n');
    expect(parseTimeline(log)).toEqual([{ kind: 'running', tools: ['ssh', 'ssh'] }]);
  });
});

describe('summarizeTools', () => {
  it('counts repeats', () => {
    expect(summarizeTools(['ssh', 'zabbix', 'ssh'])).toBe('2× ssh, zabbix');
  });
});

describe('cleanResponse', () => {
  it('strips FINAL_RESULT markers', () => {
    expect(cleanResponse('[FINAL_RESULT]\nhello\n[/FINAL_RESULT]')).toBe('hello');
  });
});
