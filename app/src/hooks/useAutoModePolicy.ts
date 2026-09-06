import { useCallback, useEffect, useRef, useState } from 'react';
import type { AutoModeConfigEdit, AutoModePromotion, AutoModeState } from './daemonAutoModeEvents';
import { useAutoModePushStore } from '../store/autoMode';

export type AutoModeEditKind = 'rule' | 'host' | 'policy';

export interface AutoModeRuleDraft {
  pattern: string[];
  decision: string;
  justification: string;
}

export interface AutoModePolicy {
  state: AutoModeState | null;
  error: string | null;
  loading: boolean;
  resolvingID: number | null;
  pendingCount: number;
  refresh: () => Promise<void>;
  promote: (id: number) => Promise<void>;
  discard: (id: number) => Promise<void>;

  addRule: (draft: AutoModeRuleDraft) => Promise<void>;
  removeRule: (pattern: string[]) => Promise<void>;
  addHost: (host: string, decision: string) => Promise<void>;
  removeHost: (host: string, decision: string) => Promise<void>;
  setPolicy: (approvalPolicy: string | null, sandboxMode: string | null) => Promise<void>;
  editing: AutoModeEditKind | null;

  setEnvironmentSlot: (id: string, values: string[]) => Promise<void>;

  savingEnvironment: boolean;
}

interface AutoModePolicyOptions {
  enabled: boolean;
  getState: () => Promise<AutoModeState>;
  promoteProposal: (id: number) => Promise<AutoModePromotion>;
  discardProposal: (id: number) => Promise<AutoModePromotion>;
  addRule: (pattern: string[], decision: string, justification: string) => Promise<AutoModeConfigEdit>;
  removeRule: (pattern: string[]) => Promise<AutoModeConfigEdit>;
  addHost: (host: string, decision: string) => Promise<AutoModeConfigEdit>;
  removeHost: (host: string, decision: string) => Promise<AutoModeConfigEdit>;
  setPolicy: (approvalPolicy: string | null, sandboxMode: string | null) => Promise<AutoModeConfigEdit>;
  setEnvironmentSlot: (slot: string, values: string[]) => Promise<AutoModeConfigEdit>;
}

const message = (err: unknown, fallback: string): string =>
  err instanceof Error ? err.message : fallback;

export function useAutoModePolicy(options: AutoModePolicyOptions): AutoModePolicy {
  const {
    enabled, getState, promoteProposal, discardProposal,
    addRule: writeRule, removeRule: dropRule,
    addHost: writeHost, removeHost: dropHost, setPolicy: writePolicy,
    setEnvironmentSlot: writeEnvironmentSlot,
  } = options;
  const [state, setState] = useState<AutoModeState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [resolvingID, setResolvingID] = useState<number | null>(null);
  const [editing, setEditing] = useState<AutoModeEditKind | null>(null);
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

  // The caller sees the failure so it can show it beside the field it came from;
  // the flag here is only what disables the other editors while one write is out.
  const edit = useCallback(async (kind: AutoModeEditKind, write: () => Promise<AutoModeConfigEdit>) => {
    setEditing(kind);
    try {
      await write();
      await refresh();
    } finally {
      setEditing(null);
    }
  }, [refresh]);

  const addRule = useCallback(
    (draft: AutoModeRuleDraft) =>
      edit('rule', () => writeRule(draft.pattern, draft.decision, draft.justification)),
    [edit, writeRule],
  );
  const removeRule = useCallback(
    (pattern: string[]) => edit('rule', () => dropRule(pattern)),
    [edit, dropRule],
  );
  const addHost = useCallback(
    (host: string, decision: string) => edit('host', () => writeHost(host, decision)),
    [edit, writeHost],
  );
  const removeHost = useCallback(
    (host: string, decision: string) => edit('host', () => dropHost(host, decision)),
    [edit, dropHost],
  );
  const setPolicy = useCallback(
    (approvalPolicy: string | null, sandboxMode: string | null) =>
      edit('policy', () => writePolicy(approvalPolicy, sandboxMode)),
    [edit, writePolicy],
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
      setEditing(null);
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
    addRule,
    removeRule,
    addHost,
    removeHost,
    setPolicy,
    editing,
    setEnvironmentSlot,
    savingEnvironment,
  };
}
