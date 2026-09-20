import { useCallback, useEffect, useMemo, useRef } from 'react';
import type { CrewCharterDocument } from '../types/generated';
import type { CrewCharterGetOutcome, CrewCharterSetOutcome } from './daemonCrewEvents';
import { useAutosave, type AutosaveEdit, type AutosaveSpec } from './useAutosave';

const AUTOSAVE_DELAY_MS = 700;

export type CrewCharterEdit = AutosaveEdit<string, CrewCharterDocument>;

type CharterGet = (member: string) => Promise<CrewCharterGetOutcome>;
type CharterSet = (member: string, content: string, expectedToken: string) => Promise<CrewCharterSetOutcome>;

export interface CrewCharterAutosave {
  read(member: string): CrewCharterEdit | undefined;
  load(member: string): Promise<void>;
  update(member: string, content: string): void;
  flush(member: string): Promise<boolean>;
  retry(member: string): Promise<boolean>;
  useExternal(member: string): void;
  keepMine(member: string): Promise<boolean>;
}

function namedFor(member: string, result: { member: string }) {
  if (result.member !== member) throw new Error(`Charter response named ${result.member}, expected ${member}`);
}

export function useCrewCharterAutosave(
  connectionGeneration: number,
  getCharter: CharterGet,
  setCharter: CharterSet,
): CrewCharterAutosave {
  const spec = useMemo<AutosaveSpec<string, CrewCharterDocument>>(() => ({
    equal: (draft, charter) => draft === charter.content,
    settled: (charter) => charter.content,
    send: async (member, draft, charter) => {
      const result = await setCharter(member, draft, charter.token);
      namedFor(member, result);
      return result.conflict ? { conflict: result.charter } : { ack: result.charter };
    },
  }), [setCharter]);
  const autosave = useAutosave(spec, connectionGeneration);
  const timers = useRef(new Map<string, number>());

  const load = useCallback(async (member: string) => {
    autosave.loading(member, '');
    const current = autosave.fence(member);
    try {
      const result = await getCharter(member);
      namedFor(member, result);
      if (!current()) return;
      const edit = autosave.read(member);
      const edited = edit?.acknowledged && edit.state !== 'saved' && edit.acknowledged.token !== result.charter.token;
      if (edited) autosave.conflict(member, result.charter);
      else autosave.adopt(member, result.charter);
    } catch (error) {
      if (current()) autosave.fail(member, error instanceof Error ? error.message : String(error));
    }
  }, [autosave, getCharter]);

  const clearTimer = useCallback((member: string) => {
    const timer = timers.current.get(member);
    if (timer !== undefined) window.clearTimeout(timer);
    timers.current.delete(member);
  }, []);

  const flush = useCallback((member: string) => {
    clearTimer(member);
    return autosave.pump(member);
  }, [autosave, clearTimer]);

  const update = useCallback((member: string, content: string) => {
    clearTimer(member);
    if (!autosave.update(member, content)) return;
    timers.current.set(member, window.setTimeout(() => {
      timers.current.delete(member);
      void autosave.pump(member);
    }, AUTOSAVE_DELAY_MS));
  }, [autosave, clearTimer]);

  useEffect(() => {
    if (connectionGeneration === 0) return;
    for (const member of autosave.keys()) void load(member);
  }, [autosave, connectionGeneration, load]);

  useEffect(() => () => {
    for (const timer of timers.current.values()) window.clearTimeout(timer);
  }, []);

  return useMemo(() => ({
    read: autosave.read,
    load,
    update,
    flush,
    retry: flush,
    useExternal: autosave.useExternal,
    keepMine: autosave.keepMine,
  }), [autosave, flush, load, update]);
}
