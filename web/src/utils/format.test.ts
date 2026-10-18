import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  formatRelativeShort,
  formatRelativeAgo,
  formatCountdown,
  formatExecutionTime,
  formatTokens,
  formatFileSize,
} from './format';

const NOW = new Date('2026-10-08T12:00:00Z').getTime();
const isoBefore = (ms: number) => new Date(NOW - ms).toISOString();
const isoAfter = (ms: number) => new Date(NOW + ms).toISOString();

const SEC = 1000;
const MIN = 60 * SEC;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('formatRelativeShort', () => {
  it('picks the largest whole unit', () => {
    expect(formatRelativeShort(isoBefore(5 * SEC))).toBe('5s');
    expect(formatRelativeShort(isoBefore(3 * MIN + 10 * SEC))).toBe('3m');
    expect(formatRelativeShort(isoBefore(2 * HOUR))).toBe('2h');
    expect(formatRelativeShort(isoBefore(4 * DAY))).toBe('4d');
    expect(formatRelativeShort(isoBefore(15 * DAY))).toBe('2w');
  });

  it('clamps future timestamps to zero seconds', () => {
    expect(formatRelativeShort(isoAfter(10 * SEC))).toBe('0s');
  });
});

describe('formatRelativeAgo', () => {
  it('appends " ago"', () => {
    expect(formatRelativeAgo(isoBefore(3 * MIN))).toBe('3m ago');
  });
});

describe('formatCountdown', () => {
  it('renders minutes, hours with minutes, and days', () => {
    expect(formatCountdown(isoAfter(45 * MIN))).toBe('45m left');
    expect(formatCountdown(isoAfter(2 * HOUR + 5 * MIN))).toBe('2h 5m left');
    expect(formatCountdown(isoAfter(3 * DAY))).toBe('3d left');
  });

  it('renders "expired" when the deadline has passed', () => {
    expect(formatCountdown(isoBefore(1))).toBe('expired');
  });
});

describe('formatExecutionTime', () => {
  it('renders "-" for zero or missing', () => {
    expect(formatExecutionTime(0)).toBe('-');
    expect(formatExecutionTime(-5)).toBe('-');
  });

  it('scales from ms to hours', () => {
    expect(formatExecutionTime(850)).toBe('850ms');
    expect(formatExecutionTime(12_340)).toBe('12.3s');
    expect(formatExecutionTime(4 * MIN + 5 * SEC)).toBe('4m 5s');
    expect(formatExecutionTime(2 * HOUR + 7 * MIN)).toBe('2h 7m');
  });
});

describe('formatTokens', () => {
  it('renders "-" for zero and a localized number otherwise', () => {
    expect(formatTokens(0)).toBe('-');
    expect(formatTokens(1234)).toBe((1234).toLocaleString());
  });
});

describe('formatFileSize', () => {
  it('renders B, KB and MB', () => {
    expect(formatFileSize(512)).toBe('512 B');
    expect(formatFileSize(2048)).toBe('2.0 KB');
    expect(formatFileSize(3 * 1024 * 1024)).toBe('3.0 MB');
  });
});
