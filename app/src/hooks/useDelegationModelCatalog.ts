import { useEffect, useState } from 'react';
import type { DelegationHarness } from '../types/generated';
import type { DelegationModelCatalog } from './daemonDelegationEvents';

// Catalogs live for the app's lifetime; refresh asks the harness again for one harness.
const catalogs = new Map<string, DelegationModelCatalog>();
const inflight = new Map<string, Promise<DelegationModelCatalog>>();
const failures = new Map<string, string>();

export function clearDelegationModelCatalogs() { catalogs.clear(); inflight.clear(); failures.clear(); }
export const knownModelName = (harness: string, provider: string, id: string) => catalogs.get(harness)?.models.find(m => m.id === id && m.provider === provider)?.name || '';

export function useDelegationModelCatalog(harness: DelegationHarness | undefined, loadModels: (harness: string) => Promise<DelegationModelCatalog>) {
  const [, rerender] = useState(0);
  const id = harness?.id ?? '';
  const wake = (request: Promise<unknown>) => void request.finally(() => rerender(n => n + 1));
  const discover = (force = false) => {
    if (!id || (!force && catalogs.has(id))) return;
    // A popover reopened while discovery runs waits on the same request instead of starting one.
    const running = inflight.get(id);
    if (running && !force) { wake(running); return; }
    catalogs.delete(id);
    failures.delete(id);
    const request = loadModels(id).then(result => { catalogs.set(id, result); return result; })
      .catch((e: unknown) => { failures.set(id, e instanceof Error ? e.message : String(e)); return { models: [], detail: '' }; })
      .finally(() => { inflight.delete(id); });
    inflight.set(id, request);
    wake(request);
    rerender(n => n + 1);
  };
  useEffect(() => { if (harness?.discovery) discover(); }, [id]); // eslint-disable-line react-hooks/exhaustive-deps
  return { catalog: catalogs.get(id), loading: inflight.has(id), error: failures.get(id) ?? '', discover };
}
