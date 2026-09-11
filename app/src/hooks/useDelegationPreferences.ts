import { useCallback, useEffect, useRef, useState } from 'react';
import type { DelegationPreferences } from '../types/generated';
import type { DelegationSettingsState } from './daemonDelegationEvents';
import { useDelegationPreferencesPush } from '../store/delegationPreferences';

type Pending = { value: DelegationPreferences; installWorkflowSkill: boolean };
const message = (e: unknown) => String(e instanceof Error ? e.message : e);

// Every edit saves at once. One request flies at a time; edits made meanwhile collapse into a
// single pending value that is sent with the revision the daemon returned.
export function useDelegationPreferences(active: boolean, load: () => Promise<DelegationSettingsState>, save: (value: DelegationPreferences, installWorkflowSkill?: boolean) => Promise<DelegationSettingsState>) {
  const [state, setState] = useState<DelegationSettingsState | null>(null);
  const [preferences, setPreferences] = useState<DelegationPreferences | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [generation, setGeneration] = useState(0);
  const pushed = useDelegationPreferencesPush(s => s.version);
  const revision = useRef(0);
  const flight = useRef<Promise<void> | null>(null);
  const pending = useRef<Pending | null>(null);
  const request = useRef(0);

  const apply = useCallback((next: DelegationSettingsState) => {
    revision.current = next.preferences.revision;
    setState(next);
    setPreferences(structuredClone(next.preferences));
  }, []);

  const fetch = useCallback(async () => {
    const id = ++request.current;
    try {
      const next = await load();
      if (id !== request.current) return;
      // A new revision is a change; the same table with fresh harness state is not.
      if (next.preferences.revision !== revision.current) setGeneration(n => n + 1);
      apply(next);
    } catch (e) {
      if (id === request.current) setError(message(e));
    }
  }, [load, apply]);

  const reload = useCallback(async () => { setError(''); await fetch(); }, [fetch]);

  useEffect(() => { if (active && !flight.current && !pending.current) void reload(); }, [active, pushed, reload]);
  useEffect(() => () => { request.current++; }, []);

  const drain = useCallback(async () => {
    while (pending.current) {
      const { value, installWorkflowSkill } = pending.current;
      pending.current = null;
      try {
        const next = await save({ ...value, revision: revision.current }, installWorkflowSkill);
        revision.current = next.preferences.revision;
        if (pending.current) setState(next); else apply(next);
      } catch (e) {
        pending.current = null;
        setError(message(e));
        await fetch();
      }
    }
  }, [save, apply, fetch]);

  const persist = useCallback((value: DelegationPreferences, installWorkflowSkill = false) => {
    setPreferences(value);
    setError('');
    setGeneration(n => n + 1);
    // An install queued behind a running save must survive the edits that collapse into it.
    pending.current = { value, installWorkflowSkill: installWorkflowSkill || (pending.current?.installWorkflowSkill ?? false) };
    if (flight.current) return flight.current;
    request.current++;
    setBusy(true);
    flight.current = drain().finally(() => {
      flight.current = null;
      setBusy(false);
      // A push that landed during the flight was not acted on; a newer revision than ours needs a load.
      const announced = useDelegationPreferencesPush.getState().revision;
      if (announced !== null && announced > revision.current) void fetch();
    });
    return flight.current;
  }, [drain, fetch]);

  // generation moves on every save request and every load, so an undo taken at one is stale at the next.
  return { state, preferences, busy, error, generation, reload, save: persist };
}
export type DelegationPreferencesPolicy = ReturnType<typeof useDelegationPreferences>;
