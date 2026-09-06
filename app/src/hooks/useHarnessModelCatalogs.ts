import { useCallback, useEffect, useRef, useState } from 'react';
import type { DelegationModelCatalog } from './daemonDelegationEvents';

export function useHarnessModelCatalogs(
  enabled: boolean,
  loadModels: (harness: string) => Promise<DelegationModelCatalog>,
) {
  const [catalogs, setCatalogs] = useState<Record<string, DelegationModelCatalog>>({});
  const [loading, setLoading] = useState<Record<string, boolean>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const mounted = useRef(true);
  const loadingRef = useRef(new Set<string>());

  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);

  const discover = useCallback(async (harness: string) => {
    if (!enabled || !harness || loadingRef.current.has(harness)) return;
    loadingRef.current.add(harness);
    setLoading((current) => ({ ...current, [harness]: true }));
    setErrors((current) => ({ ...current, [harness]: '' }));
    try {
      const result = await loadModels(harness);
      if (mounted.current) setCatalogs((current) => ({ ...current, [harness]: result }));
    } catch (error) {
      if (mounted.current) {
        setErrors((current) => ({
          ...current,
          [harness]: error instanceof Error ? error.message : String(error),
        }));
      }
    } finally {
      loadingRef.current.delete(harness);
      if (mounted.current) setLoading((current) => ({ ...current, [harness]: false }));
    }
  }, [enabled, loadModels]);

  return { catalogs, loading, errors, discover };
}
