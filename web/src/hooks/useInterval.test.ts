import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useInterval } from './useInterval';

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useInterval', () => {
  it('invokes the callback on every tick', () => {
    const cb = vi.fn();
    renderHook(() => useInterval(cb, 1000));
    vi.advanceTimersByTime(3000);
    expect(cb).toHaveBeenCalledTimes(3);
  });

  it('does nothing when paused with null or zero', () => {
    const cb = vi.fn();
    renderHook(() => useInterval(cb, null));
    renderHook(() => useInterval(cb, 0));
    vi.advanceTimersByTime(5000);
    expect(cb).not.toHaveBeenCalled();
  });

  it('calls the latest callback without restarting the timer', () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderHook(({ cb }) => useInterval(cb, 1000), {
      initialProps: { cb: first },
    });
    vi.advanceTimersByTime(500);
    rerender({ cb: second });
    vi.advanceTimersByTime(500);
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
  });

  it('clears the timer on unmount', () => {
    const cb = vi.fn();
    const { unmount } = renderHook(() => useInterval(cb, 1000));
    unmount();
    vi.advanceTimersByTime(3000);
    expect(cb).not.toHaveBeenCalled();
  });
});
