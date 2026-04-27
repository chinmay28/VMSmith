import { useState, useEffect, useCallback } from 'react';

/**
 * Hook for fetching data with automatic polling and manual refresh.
 */
export function useFetch(fetchFn, deps = [], pollInterval = null) {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const load = useCallback(async () => {
    try {
      const result = await fetchFn();
      setData(result);
      setError(null);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, deps); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    load();
    if (pollInterval) {
      const id = setInterval(load, pollInterval);
      return () => clearInterval(id);
    }
  }, [load, pollInterval]);

  return { data, loading, error, refresh: load };
}

/**
 * Hook for executing async mutations with loading/error state.
 */
export function useMutation(mutationFn) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  const reset = useCallback(() => {
    setError(null);
  }, []);

  const execute = useCallback(async (...args) => {
    setLoading(true);
    setError(null);
    try {
      const result = await mutationFn(...args);
      return result;
    } catch (err) {
      setError(err.message);
      throw err;
    } finally {
      setLoading(false);
    }
  }, [mutationFn]);

  return { execute, loading, error, reset };
}
