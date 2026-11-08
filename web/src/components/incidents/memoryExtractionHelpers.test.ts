import { describe, expect, it } from 'vitest';
import { canRerunExtraction, extractionState, isExtractionInProgress, showsMemorySection } from './memoryExtractionHelpers';

describe('memory extraction helpers', () => {
  it('shows the section for closed incidents and for any touched incident', () => {
    expect(showsMemorySection({ status: 'closed' })).toBe(true);
    expect(showsMemorySection({ status: 'merged', memory_extraction: 'skipped' })).toBe(true);
    expect(showsMemorySection({ status: 'monitor' })).toBe(false);
  });

  it('polls and blocks the rerun while the pass is pending or running', () => {
    expect(isExtractionInProgress({ memory_extraction: 'pending' })).toBe(true);
    expect(isExtractionInProgress({ memory_extraction: 'succeeded' })).toBe(false);
    expect(canRerunExtraction({ status: 'closed', memory_extraction: 'running' })).toBe(false);
    expect(canRerunExtraction({ status: 'closed', memory_extraction: 'failed' })).toBe(true);
    expect(canRerunExtraction({ status: 'closed' })).toBe(true);
    expect(canRerunExtraction({ status: 'merged', memory_extraction: 'skipped' })).toBe(false);
  });

  it('describes each state', () => {
    expect(extractionState({ status: 'closed' })).toMatchObject({ label: 'Memory pass not run', tone: 'closed' });
    expect(extractionState({ status: 'monitor' }).label).toBe('Memory pass runs when the incident closes');
    expect(extractionState({ status: 'closed', memory_extraction: 'succeeded', memory_extraction_note: 'redacted 1 secrets' })).toEqual({
      label: 'Memories recorded',
      tone: 'ok',
      detail: 'redacted 1 secrets',
    });
    expect(
      extractionState({ status: 'closed', memory_extraction: 'pending', memory_extraction_attempts: 1, memory_extraction_error: 'llm call: timeout' }),
    ).toMatchObject({ tone: 'work', detail: 'Attempt 1 failed: llm call: timeout. Retrying.' });
    expect(extractionState({ status: 'closed', memory_extraction: 'failed', memory_extraction_error: 'agent worker not connected' })).toMatchObject({
      tone: 'fail',
      detail: 'agent worker not connected',
    });
  });
});
