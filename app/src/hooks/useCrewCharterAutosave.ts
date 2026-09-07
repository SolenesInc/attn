import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react';
import type { CrewCharterDocument } from '../types/generated';
import type { CrewCharterGetOutcome, CrewCharterSetOutcome } from './daemonCrewEvents';

const AUTOSAVE_DELAY_MS = 700;

export type CrewCharterSaveState = 'idle' | 'loading' | 'saved' | 'dirty' | 'saving' | 'error' | 'conflict';

export interface CrewCharterEdit {
  member: string;
  draft: string;
  acknowledged?: CrewCharterDocument;
  external?: CrewCharterDocument;
  state: CrewCharterSaveState;
  error?: string;
  generation: number;
  loadGeneration: number;
  documentGeneration: number;
}

type CharterGet = (member: string) => Promise<CrewCharterGetOutcome>;
type CharterSet = (member: string, content: string, expectedToken: string) => Promise<CrewCharterSetOutcome>;

export interface CrewCharterAutosave {
  read(member: string): CrewCharterEdit | undefined;
  load(member: string, force?: boolean): Promise<void>;
  update(member: string, content: string): void;
  flush(member: string): Promise<boolean>;
  retry(member: string): Promise<boolean>;
  useExternal(member: string): void;
  keepMine(member: string): Promise<boolean>;
}

export function useCrewCharterAutosave(
  connectionGeneration: number,
  getCharter: CharterGet,
  setCharter: CharterSet,
): CrewCharterAutosave {
  const editsRef = useRef(new Map<string, CrewCharterEdit>());
  const timersRef = useRef(new Map<string, number>());
  const activeRef = useRef(new Map<string, Promise<boolean>>());
  const [, redraw] = useReducer((value) => value + 1, 0);

  const store = useCallback((member: string, edit: CrewCharterEdit) => {
    editsRef.current.set(member, edit);
    redraw();
  }, []);

  const load = useCallback(async (member: string, force = false) => {
    const current = editsRef.current.get(member);
    if (!force && current && current.state !== 'idle' && current.state !== 'error') return;
    const loadGeneration = (current?.loadGeneration ?? 0) + 1;
    const documentGeneration = current?.documentGeneration ?? 0;
    store(member, current
      ? { ...current, state: current.acknowledged ? current.state : 'loading', error: undefined, loadGeneration }
      : { member, draft: '', state: 'loading', generation: 0, loadGeneration, documentGeneration });
    try {
      const result = await getCharter(member);
      if (result.member !== member) {
        throw new Error(`Charter response named ${result.member}, expected ${member}`);
      }
      const latest = editsRef.current.get(member);
      if (!latest
        || latest.loadGeneration !== loadGeneration
        || latest.documentGeneration !== documentGeneration
        || activeRef.current.has(member)) return;
      if (!latest.acknowledged || latest.state === 'loading' || latest.state === 'saved') {
        store(member, {
          ...latest,
          draft: result.charter.content,
          acknowledged: result.charter,
          external: undefined,
          state: 'saved',
          error: undefined,
        });
        return;
      }
      if (latest.acknowledged.token !== result.charter.token) {
        store(member, { ...latest, external: result.charter, state: 'conflict', error: undefined });
        return;
      }
      store(member, {
        ...latest,
        state: latest.draft === latest.acknowledged.content ? 'saved' : 'dirty',
        error: undefined,
      });
    } catch (error) {
      const latest = editsRef.current.get(member);
      if (!latest
        || latest.loadGeneration !== loadGeneration
        || latest.documentGeneration !== documentGeneration
        || activeRef.current.has(member)) return;
      store(member, {
        ...latest,
        state: latest.acknowledged ? latest.state : 'error',
        error: error instanceof Error ? error.message : String(error),
      });
    }
  }, [getCharter, store]);

  const pump = useCallback((member: string): Promise<boolean> => {
    const active = activeRef.current.get(member);
    if (active) return active;

    const run = async () => {
      while (true) {
        const current = editsRef.current.get(member);
        if (!current?.acknowledged || current.state === 'conflict') return false;
        if (current.draft === current.acknowledged.content) {
          store(member, { ...current, state: 'saved', error: undefined });
          return true;
        }

        const submittedGeneration = current.generation;
        const submittedContent = current.draft;
        const submittedToken = current.acknowledged.token;
        store(member, {
          ...current,
          state: 'saving',
          error: undefined,
          documentGeneration: current.documentGeneration + 1,
        });
        try {
          const result = await setCharter(member, submittedContent, submittedToken);
          const latest = editsRef.current.get(member);
          if (!latest) return false;
          if (result.member !== member) {
            store(member, {
              ...latest,
              state: 'error',
              error: `Charter response named ${result.member}, expected ${member}`,
              documentGeneration: latest.documentGeneration + 1,
            });
            return false;
          }
          if (result.conflict) {
            store(member, {
              ...latest,
              external: result.charter,
              state: 'conflict',
              error: undefined,
              documentGeneration: latest.documentGeneration + 1,
            });
            return false;
          }
          const settled = latest.generation === submittedGeneration && latest.draft === submittedContent;
          store(member, {
            ...latest,
            acknowledged: result.charter,
            external: undefined,
            state: settled ? 'saved' : 'dirty',
            error: undefined,
            documentGeneration: latest.documentGeneration + 1,
          });
          if (settled) return true;
        } catch (error) {
          const latest = editsRef.current.get(member);
          if (latest) {
            store(member, {
              ...latest,
              state: 'error',
              error: error instanceof Error ? error.message : String(error),
              documentGeneration: latest.documentGeneration + 1,
            });
          }
          return false;
        }
      }
    };

    const request = Promise.resolve()
      .then(run)
      .finally(() => {
        if (activeRef.current.get(member) === request) activeRef.current.delete(member);
      });
    activeRef.current.set(member, request);
    return request;
  }, [setCharter, store]);

  const flush = useCallback((member: string) => {
    const timer = timersRef.current.get(member);
    if (timer !== undefined) {
      window.clearTimeout(timer);
      timersRef.current.delete(member);
    }
    return pump(member);
  }, [pump]);

  const update = useCallback((member: string, content: string) => {
    const current = editsRef.current.get(member);
    if (!current?.acknowledged) return;
    const timer = timersRef.current.get(member);
    if (timer !== undefined) window.clearTimeout(timer);
    const writeActive = activeRef.current.has(member);
    store(member, {
      ...current,
      draft: content,
      external: undefined,
      state: writeActive ? 'saving' : content === current.acknowledged.content ? 'saved' : 'dirty',
      error: undefined,
      generation: current.generation + 1,
    });
    if (writeActive || content === current.acknowledged.content) {
      timersRef.current.delete(member);
      return;
    }
    timersRef.current.set(member, window.setTimeout(() => {
      timersRef.current.delete(member);
      void pump(member);
    }, AUTOSAVE_DELAY_MS));
  }, [pump, store]);

  const useExternal = useCallback((member: string) => {
    const current = editsRef.current.get(member);
    if (!current?.external) return;
    store(member, {
      ...current,
      draft: current.external.content,
      acknowledged: current.external,
      external: undefined,
      state: 'saved',
      error: undefined,
      generation: current.generation + 1,
    });
  }, [store]);

  const keepMine = useCallback((member: string) => {
    const current = editsRef.current.get(member);
    if (!current?.external) return Promise.resolve(false);
    store(member, {
      ...current,
      acknowledged: current.external,
      external: undefined,
      state: 'dirty',
      error: undefined,
    });
    return pump(member);
  }, [pump, store]);

  useEffect(() => {
    if (connectionGeneration === 0) return;
    for (const member of editsRef.current.keys()) {
      void load(member, true).then(() => {
        const latest = editsRef.current.get(member);
        if (latest?.acknowledged && (latest.state === 'dirty' || latest.state === 'error')) void pump(member);
      });
    }
  }, [connectionGeneration, load, pump]);

  useEffect(() => () => {
    for (const timer of timersRef.current.values()) window.clearTimeout(timer);
    timersRef.current.clear();
  }, []);

  return useMemo(() => ({
    read: (member) => editsRef.current.get(member),
    load,
    update,
    flush,
    retry: flush,
    useExternal,
    keepMine,
  }), [flush, keepMine, load, update, useExternal]);
}
