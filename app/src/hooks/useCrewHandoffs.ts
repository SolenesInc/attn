import { useCallback, useMemo, useRef, useState } from 'react';
import type { CrewHandoffDocument, CrewHandoffSummary } from '../types/generated';
import type { CrewHandoffGetOutcome, CrewHandoffsGetOutcome } from './daemonCrewEvents';

export interface CrewHandoffLetter {
  state: 'loading' | 'ready' | 'error';
  filename: string;
  document?: CrewHandoffDocument;
  error?: string;
  request: number;
}

export interface CrewHandoffLoad {
  state: 'loading' | 'ready' | 'error';
  handoffs: CrewHandoffSummary[];
  selected?: string;
  letter?: CrewHandoffLetter;
  error?: string;
  request: number;
  connectionGeneration: number;
}

export interface CrewHandoffHistory {
  read(member: string): CrewHandoffLoad | undefined;
  load(member: string, force?: boolean): void;
  loadLetter(member: string, filename: string): void;
  select(member: string, filename: string): void;
}

type HandoffsGet = (member: string) => Promise<CrewHandoffsGetOutcome>;
type HandoffGet = (member: string, filename: string) => Promise<CrewHandoffGetOutcome>;

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function useCrewHandoffs(
  connectionGeneration: number,
  getHandoffs: HandoffsGet,
  getHandoff: HandoffGet,
): CrewHandoffHistory {
  const loadsRef = useRef<Record<string, CrewHandoffLoad>>({});
  const [loads, setLoads] = useState(loadsRef.current);
  const listRequest = useRef(0);
  const letterRequest = useRef(0);

  const store = useCallback((member: string, load: CrewHandoffLoad) => {
    loadsRef.current = { ...loadsRef.current, [member]: load };
    setLoads(loadsRef.current);
  }, []);

  const loadLetter = useCallback((member: string, filename: string) => {
    const existing = loadsRef.current[member];
    if (!existing) return;
    const request = ++letterRequest.current;
    store(member, { ...existing, letter: { state: 'loading', filename, request } });
    const settle = (letter: CrewHandoffLetter) => {
      const current = loadsRef.current[member];
      if (current?.letter?.request === request) store(member, { ...current, letter });
    };
    void getHandoff(member, filename).then((result) => {
      if (result.member !== member || result.handoff.filename !== filename) {
        throw new Error(`Handoff response named ${result.member}/${result.handoff.filename}, expected ${member}/${filename}`);
      }
      settle({ state: 'ready', filename, document: result.handoff, request });
    }).catch((error) => {
      settle({ state: 'error', filename, error: describe(error), request });
    });
  }, [getHandoff, store]);

  const load = useCallback((member: string, force = false) => {
    const existing = loadsRef.current[member];
    if (!force && existing && existing.state !== 'error' && existing.connectionGeneration === connectionGeneration) return;
    const request = ++listRequest.current;
    store(member, {
      state: 'loading',
      handoffs: existing?.handoffs ?? [],
      selected: existing?.selected,
      letter: force ? undefined : existing?.letter,
      request,
      connectionGeneration,
    });
    void getHandoffs(member).then((result) => {
      if (result.member !== member) {
        throw new Error(`Handoff response named ${result.member}, expected ${member}`);
      }
      const current = loadsRef.current[member];
      if (current?.request !== request) return;
      const selected = current.selected && result.handoffs.some((handoff) => handoff.filename === current.selected)
        ? current.selected
        : result.handoffs[0]?.filename;
      const letter = current.letter?.filename === selected ? current.letter : undefined;
      store(member, { state: 'ready', handoffs: result.handoffs, selected, letter, request, connectionGeneration });
      if (selected && !letter) loadLetter(member, selected);
    }).catch((error) => {
      const current = loadsRef.current[member];
      if (current?.request !== request) return;
      store(member, { ...current, state: 'error', error: describe(error) });
    });
  }, [connectionGeneration, getHandoffs, loadLetter, store]);

  const select = useCallback((member: string, filename: string) => {
    const existing = loadsRef.current[member];
    if (!existing || existing.selected === filename) return;
    store(member, { ...existing, selected: filename });
    if (existing.letter?.filename !== filename) loadLetter(member, filename);
  }, [loadLetter, store]);

  const read = useCallback((member: string) => loads[member], [loads]);

  return useMemo(() => ({ read, load, loadLetter, select }), [load, loadLetter, read, select]);
}
