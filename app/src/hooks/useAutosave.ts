import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react';

export type AutosaveState = 'loading' | 'saved' | 'dirty' | 'saving' | 'error' | 'conflict';

export interface AutosaveEdit<Draft, Ack> {
  draft: Draft;
  acknowledged?: Ack;
  conflict?: Ack;
  state: AutosaveState;
  error?: string;
  uncertain: boolean;
}

interface Entry<Draft, Ack> extends AutosaveEdit<Draft, Ack> {
  generation: number;
  retryOnReconnect: boolean;
}

export type AutosaveResult<Ack> = { ack: Ack } | { conflict: Ack } | { error: string; ack?: Ack };

export interface AutosaveSpec<Draft, Ack> {
  send(member: string, draft: Draft, acknowledged: Ack): Promise<AutosaveResult<Ack>>;
  equal(draft: Draft, acknowledged: Ack): boolean;
  settled(acknowledged: Ack): Draft;
  stale?(candidate: Ack, current: Ack): boolean;
}

const transportFailure = /not connected|connection|socket/i;

export function useAutosave<Draft, Ack>(spec: AutosaveSpec<Draft, Ack>, connectionGeneration: number) {
  const entries = useRef(new Map<string, Entry<Draft, Ack>>());
  const active = useRef(new Map<string, Promise<boolean>>());
  const specRef = useRef(spec);
  const [, redraw] = useReducer((value: number) => value + 1, 0);

  useEffect(() => {
    specRef.current = spec;
  }, [spec]);

  const patch = useCallback((member: string, changes: Partial<Entry<Draft, Ack>>) => {
    const entry = entries.current.get(member);
    if (entry) entries.current.set(member, { ...entry, ...changes });
    redraw();
  }, []);

  const settled = useCallback((acknowledged: Ack): Partial<Entry<Draft, Ack>> => ({
    draft: specRef.current.settled(acknowledged),
    acknowledged,
    conflict: undefined,
    state: 'saved',
    error: undefined,
    retryOnReconnect: false,
  }), []);

  const read = useCallback((member: string): AutosaveEdit<Draft, Ack> | undefined => entries.current.get(member), []);

  const keys = useCallback(() => [...entries.current.keys()], []);

  const loading = useCallback((member: string, draft: Draft) => {
    const entry = entries.current.get(member);
    if (entry?.acknowledged) return;
    entries.current.set(member, {
      ...(entry ?? { draft, uncertain: false, generation: 0, retryOnReconnect: false }),
      state: 'loading',
      error: undefined,
    });
    redraw();
  }, []);

  const fail = useCallback((member: string, error: string) => {
    if (!entries.current.get(member)?.acknowledged) patch(member, { state: 'error', error });
  }, [patch]);

  const fence = useCallback((member: string) => {
    const entry = entries.current.get(member);
    return () => {
      const latest = entries.current.get(member);
      return latest !== undefined
        && entry !== undefined
        && latest.acknowledged === entry.acknowledged
        && latest.generation === entry.generation
        && !active.current.has(member);
    };
  }, []);

  const adopt = useCallback((member: string, acknowledged: Ack) => {
    const entry = entries.current.get(member);
    if (!entry) {
      entries.current.set(member, {
        draft: specRef.current.settled(acknowledged),
        acknowledged,
        state: 'saved',
        uncertain: false,
        generation: 0,
        retryOnReconnect: false,
      });
      redraw();
      return;
    }
    if (entry.acknowledged && specRef.current.stale?.(acknowledged, entry.acknowledged)) return;
    const idle = !active.current.has(member) && !entry.uncertain;
    const clean = entry.state === 'saved' || entry.state === 'loading' || (idle && specRef.current.equal(entry.draft, acknowledged));
    patch(member, clean ? settled(acknowledged) : { acknowledged });
  }, [patch, settled]);

  const conflict = useCallback((member: string, external: Ack) => {
    patch(member, { conflict: external, state: 'conflict', error: undefined, uncertain: false, retryOnReconnect: false });
  }, [patch]);

  const update = useCallback((member: string, draft: Draft): boolean => {
    const entry = entries.current.get(member);
    if (!entry?.acknowledged) return false;
    const busy = active.current.has(member);
    const dirty = entry.uncertain || !specRef.current.equal(draft, entry.acknowledged);
    patch(member, {
      draft,
      conflict: undefined,
      state: busy ? 'saving' : dirty ? 'dirty' : 'saved',
      error: undefined,
      generation: entry.generation + 1,
      retryOnReconnect: false,
    });
    return !busy && dirty;
  }, [patch]);

  const pump = useCallback((member: string): Promise<boolean> => {
    const running = active.current.get(member);
    if (running) return running;
    const run = async () => {
      while (true) {
        const entry = entries.current.get(member);
        if (!entry?.acknowledged || entry.conflict) return false;
        if (!entry.uncertain && specRef.current.equal(entry.draft, entry.acknowledged)) {
          patch(member, settled(entry.acknowledged));
          return true;
        }
        const { draft, acknowledged, generation } = entry;
        patch(member, { state: 'saving', error: undefined });
        let result: AutosaveResult<Ack>;
        try {
          result = await specRef.current.send(member, draft, acknowledged);
        } catch (error) {
          const message = error instanceof Error ? error.message : String(error);
          patch(member, { state: 'error', error: message, uncertain: true, retryOnReconnect: transportFailure.test(message) });
          return false;
        }
        const latest = entries.current.get(member);
        if (!latest) return false;
        if ('conflict' in result) {
          conflict(member, result.conflict);
          return false;
        }
        const held = latest.acknowledged ?? acknowledged;
        const fresh = result.ack !== undefined && !specRef.current.stale?.(result.ack, held);
        const next = fresh && result.ack !== undefined ? result.ack : held;
        const uncertain = fresh ? false : latest.uncertain;
        if ('error' in result) {
          patch(member, { acknowledged: next, uncertain, state: 'error', error: result.error, retryOnReconnect: false });
          return false;
        }
        if (latest.generation === generation) {
          patch(member, { ...settled(next), uncertain });
          return true;
        }
        patch(member, { acknowledged: next, uncertain });
      }
    };
    const request = run().finally(() => {
      if (active.current.get(member) === request) active.current.delete(member);
    });
    active.current.set(member, request);
    return request;
  }, [conflict, patch, settled]);

  const useExternal = useCallback((member: string) => {
    const entry = entries.current.get(member);
    if (entry?.conflict) patch(member, { ...settled(entry.conflict), uncertain: false });
  }, [patch, settled]);

  const keepMine = useCallback((member: string) => {
    const entry = entries.current.get(member);
    if (!entry?.conflict) return Promise.resolve(false);
    patch(member, { acknowledged: entry.conflict, conflict: undefined, state: 'dirty', error: undefined });
    return pump(member);
  }, [patch, pump]);

  useEffect(() => {
    if (connectionGeneration === 0) return;
    for (const [member, entry] of entries.current) {
      if (entry.retryOnReconnect) void pump(member);
    }
  }, [connectionGeneration, pump]);

  return useMemo(
    () => ({ read, keys, loading, fail, fence, adopt, conflict, update, pump, useExternal, keepMine }),
    [read, keys, loading, fail, fence, adopt, conflict, update, pump, useExternal, keepMine],
  );
}
