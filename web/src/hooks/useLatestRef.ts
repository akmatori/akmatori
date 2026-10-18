import { useEffect, useRef, type RefObject } from 'react';

// useLatestRef returns a ref that always holds the most recent value. Use it
// to read the newest callback or prop from inside an effect or timer without
// listing it as a dependency (which would restart the effect on every render).
export function useLatestRef<T>(value: T): RefObject<T> {
  const ref = useRef(value);
  useEffect(() => {
    ref.current = value;
  });
  return ref;
}
