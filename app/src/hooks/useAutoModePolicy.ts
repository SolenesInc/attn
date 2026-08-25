// Design: docs/plans/2026-08-16-pi-auto-mode.md.

import { useCallback, useEffect, useRef, useState } from 'react';
import type { AutoModePatternEdit, AutoModePromotion, AutoModeState } from './daemonAutoModeEvents';
import { useAutoModePushStore } from '../store/autoMode';

export interface AutoModePolicy {
  state: AutoModeState | null;
  error: string | null;
  loading: boolean;
  resolvingID: number | null;
  pendingCount: number;
  refresh: () => Promise<void>;
  promote: (id: number) => Promise<void>;
  discard: (id: number) => Promise<void>;
  addPattern: (list: AutoModePatternList, pattern: string) => Promise<void>;
  removePattern: (list: AutoModePatternList, pattern: string) => Promise<void>;
  editingList: AutoModePatternList | null;

  setEnvironmentSlot: (id: string, values: string[]) => Promise<void>;

  savingEnvironment: boolean;
}

export type AutoModePatternList = 'allow' | 'hard_deny';

interface AutoModePolicyOptions {
  enabled: boolean;
  getState: () => Promise<AutoModeState>;
  promoteProposal: (id: number) => Promise<AutoModePromotion>;
  discardProposal: (id: number) => Promise<AutoModePromotion>;
  addPattern: (list: string, pattern: string) => Promise<AutoModePatternEdit>;
  removePattern: (list: string, pattern: string) => Promise<AutoModePatternEdit>;
  setEnvironmentSlot: (slot: string, values: string[]) => Promise<AutoModePatternEdit>;
}

const message = (err: unknown, fallback: string): string =>
  err instanceof Error ? err.message : fallback;

export function useAutoModePolicy(options: AutoModePolicyOptions): AutoModePolicy {
  const {
    enabled, getState, promoteProposal, discardProposal, addPattern, removePattern,
    setEnvironmentSlot: writeEnvironmentSlot,
  } = options;
  const [state, setState] = useState<AutoModeState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [resolvingID, setResolvingID] = useState<number | null>(null);
  const [editingList, setEditingList] = useState<AutoModePatternList | null>(null);
  const [savingEnvironment, setSavingEnvironment] = useState(false);

  const seqRef = useRef(0);
  const pushedVersion = useAutoModePushStore((store) => store.version);
  const [adoptedVersion, setAdoptedVersion] = useState(pushedVersion);

  const refresh = useCallback(async () => {
    const seq = ++seqRef.current;
    setLoading(true);
    try {
      const next = await getState();
      if (seqRef.current !== seq) return;
      setState(next);
      setError(null);
    } catch (err) {
      if (seqRef.current !== seq) return;
      setError(message(err, 'Could not read auto mode'));
    } finally {
      if (seqRef.current === seq) setLoading(false);
    }
  }, [getState]);

  const resolve = useCallback(async (
    id: number,
    action: (id: number) => Promise<AutoModePromotion>,
    fallback: string,
  ) => {
    setResolvingID(id);
    try {
      await action(id);
      await refresh();
    } catch (err) {
      setError(message(err, fallback));
    } finally {
      setResolvingID(null);
    }
  }, [refresh]);

  const promote = useCallback(
    (id: number) => resolve(id, promoteProposal, 'Could not promote the proposal'),
    [resolve, promoteProposal],
  );
  const discard = useCallback(
    (id: number) => resolve(id, discardProposal, 'Could not discard the proposal'),
    [resolve, discardProposal],
  );

  const edit = useCallback(async (
    list: AutoModePatternList,
    pattern: string,
    action: (list: string, pattern: string) => Promise<AutoModePatternEdit>,
  ) => {
    setEditingList(list);
    try {
      await action(list, pattern);
      await refresh();
    } finally {
      setEditingList(null);
    }
  }, [refresh]);

  const add = useCallback(
    (list: AutoModePatternList, pattern: string) => edit(list, pattern, addPattern),
    [edit, addPattern],
  );
  const remove = useCallback(
    (list: AutoModePatternList, pattern: string) => edit(list, pattern, removePattern),
    [edit, removePattern],
  );

  const setEnvironmentSlot = useCallback(async (id: string, values: string[]) => {
    setSavingEnvironment(true);
    try {
      await writeEnvironmentSlot(id, values);
      await refresh();
    } finally {
      setSavingEnvironment(false);
    }
  }, [writeEnvironmentSlot, refresh]);

  useEffect(() => {
    if (!enabled) {
      seqRef.current++;
      setState(null);
      setError(null);
      setLoading(false);
      setEditingList(null);
      setSavingEnvironment(false);
      return;
    }
    void refresh();
  }, [enabled, refresh]);

  useEffect(() => {
    if (!enabled || pushedVersion <= adoptedVersion) return;
    const pushed = useAutoModePushStore.getState().pushed;
    setAdoptedVersion(pushedVersion);
    if (!pushed) return;
    seqRef.current++;
    setState(pushed);
    setError(null);
    setLoading(false);
  }, [enabled, pushedVersion, adoptedVersion]);

  return {
    state,
    error,
    loading,
    resolvingID,
    pendingCount: state?.proposals.length ?? 0,
    refresh,
    promote,
    discard,
    addPattern: add,
    removePattern: remove,
    editingList,
    setEnvironmentSlot,
    savingEnvironment,
  };
}
