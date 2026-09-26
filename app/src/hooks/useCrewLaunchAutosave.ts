import { useCallback, useEffect, useMemo } from 'react';
import type { CrewMember } from '../types/generated';
import type { CrewMutationOutcome } from './daemonCrewEvents';
import { useAutosave, type AutosaveSpec, type AutosaveState } from './useAutosave';

export interface CrewLaunchSelection {
  agent: string;
  model: string;
  effort: string;
}

export type CrewLaunchSaveState = 'saved' | 'saving' | 'error';

export interface CrewLaunchEdit {
  acknowledged: CrewMember;
  draft: CrewLaunchSelection;
  state: CrewLaunchSaveState;
  error?: string;
}

export interface CrewLaunchWrite {
  member: string;
  expectedRevision: number;
  agent: string;
  model: string;
  effort: string;
}

const selectionFromMember = (member: CrewMember): CrewLaunchSelection => ({
  agent: member.agent ?? '',
  model: member.model ?? '',
  effort: member.effort ?? '',
});

const merge = (pending: Partial<CrewLaunchSelection>, member: CrewMember): CrewLaunchSelection => ({
  ...selectionFromMember(member),
  ...pending,
});

const sameSelection = (a: CrewLaunchSelection, b: CrewLaunchSelection) => (
  a.agent === b.agent && a.model === b.model && a.effort === b.effort
);

const launchState = (state: AutosaveState): CrewLaunchSaveState => (
  state === 'saved' || state === 'error' ? state : 'saving'
);

export function useCrewLaunchAutosave(
  members: CrewMember[],
  connectionGeneration: number,
  send: (write: CrewLaunchWrite) => Promise<CrewMutationOutcome>,
) {
  const spec = useMemo<AutosaveSpec<Partial<CrewLaunchSelection>, CrewMember>>(() => ({
    equal: (pending, member) => sameSelection(merge(pending, member), selectionFromMember(member)),
    settled: () => ({}),
    stale: (candidate, current) => candidate.revision < current.revision
      || (candidate.revision === current.revision && JSON.stringify(candidate) === JSON.stringify(current)),
    send: async (memberId, pending, acknowledged) => {
      const outcome = await send({ member: memberId, expectedRevision: acknowledged.revision, ...merge(pending, acknowledged) });
      if (outcome.success && outcome.member) return { ack: outcome.member };
      if (outcome.success) throw new Error('The daemon did not return the saved launch settings.');
      return { ack: outcome.member, error: outcome.error || 'The launch settings were not saved.' };
    },
  }), [send]);
  const autosave = useAutosave(spec, connectionGeneration);

  const observe = useCallback((member: CrewMember) => autosave.adopt(member.id, member), [autosave]);

  useEffect(() => {
    for (const member of members) observe(member);
  }, [members, observe]);

  const update = useCallback((memberId: string, patch: Partial<CrewLaunchSelection>) => {
    const edit = autosave.read(memberId);
    if (!edit) return;
    autosave.update(memberId, { ...edit.draft, ...patch });
    void autosave.pump(memberId);
  }, [autosave]);

  const read = useCallback((memberId: string): CrewLaunchEdit | undefined => {
    const edit = autosave.read(memberId);
    if (!edit?.acknowledged) return undefined;
    return {
      acknowledged: edit.acknowledged,
      draft: merge(edit.draft, edit.acknowledged),
      state: launchState(edit.state),
      ...(edit.error ? { error: edit.error } : {}),
    };
  }, [autosave]);

  return useMemo(() => ({ read, update, retry: autosave.pump, observe }), [autosave.pump, observe, read, update]);
}
