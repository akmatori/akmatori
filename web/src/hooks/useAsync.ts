import { useState, useCallback } from 'react';

interface AsyncState<T> {
  data: T | null;
  loading: boolean;
  error: string | null;
  success: boolean;
}

interface UseAsyncReturn<T> extends AsyncState<T> {
  execute: (asyncFn: () => Promise<T>) => Promise<T | undefined>;
  setData: (data: T | null) => void;
  setError: (error: string | null) => void;
  reset: () => void;
  clearSuccess: () => void;
}

export function useAsync<T>(
  initialLoading = false,
): UseAsyncReturn<T> {
  const [state, setState] = useState<AsyncState<T>>({
    data: null,
    loading: initialLoading,
    error: null,
    success: false,
  });

  const execute = useCallback(async (asyncFn: () => Promise<T>): Promise<T | undefined> => {
    setState(prev => ({ ...prev, loading: true, error: null, success: false }));
    try {
      const data = await asyncFn();
      setState({ data, loading: false, error: null, success: true });
      return data;
    } catch (err) {
      const error = err instanceof Error ? err.message : 'An error occurred';
      setState(prev => ({ ...prev, loading: false, error, success: false }));
      return undefined;
    }
  }, []);

  const setData = useCallback((data: T | null) => {
    setState(prev => ({ ...prev, data }));
  }, []);

  const setError = useCallback((error: string | null) => {
    setState(prev => ({ ...prev, error }));
  }, []);

  const reset = useCallback(() => {
    setState({ data: null, loading: false, error: null, success: false });
  }, []);

  const clearSuccess = useCallback(() => {
    setState(prev => ({ ...prev, success: false }));
  }, []);

  return {
    ...state,
    execute,
    setData,
    setError,
    reset,
    clearSuccess,
  };
}
