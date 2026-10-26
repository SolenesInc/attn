import { useCallback, useMemo, useRef, useState } from 'react';
import type { PaneInputOutcomes } from './useGhosttyPaneRuntime';

export interface PaneInputRefusal {
  flash: number;
  flashing: boolean;
  keysNotSent: number;
}

export function usePaneInputRefusals() {
  const [refusals, setRefusals] = useState<ReadonlyMap<string, PaneInputRefusal>>(() => new Map());
  const panesWithKeysNotSent = useRef(new Set<string>());
  const update = useCallback((paneId: string, change: (refusal: PaneInputRefusal) => PaneInputRefusal) => {
    setRefusals((current) => new Map(current).set(paneId, change(current.get(paneId) ?? { flash: 0, flashing: false, keysNotSent: 0 })));
  }, []);
  const inputOutcomes = useMemo<PaneInputOutcomes>(() => ({
    refused: (paneId, keysNotSent) => {
      if (keysNotSent > 0) panesWithKeysNotSent.current.add(paneId);
      update(paneId, (refusal) => ({ flash: refusal.flash + 1, flashing: true, keysNotSent: refusal.keysNotSent + keysNotSent }));
    },
    sent: (paneId) => {
      if (!panesWithKeysNotSent.current.delete(paneId)) return;
      update(paneId, (refusal) => ({ ...refusal, keysNotSent: 0 }));
    },
  }), [update]);
  const endRefusalFlash = useCallback((paneId: string) => update(paneId, (refusal) => ({ ...refusal, flashing: false })), [update]);
  return { inputRefusals: refusals, inputOutcomes, endRefusalFlash };
}
