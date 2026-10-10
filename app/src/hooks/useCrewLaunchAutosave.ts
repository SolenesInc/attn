import { useCallback, useEffect, useMemo } from 'react';
import type { CrewMember, LaunchDesktopSetting } from '../types/generated';
import type { CrewMutationOutcome } from './daemonCrewEvents';
import { useAutosave, type AutosaveSpec, type AutosaveState } from './useAutosave';
import { desktopIdSlot } from '../utils/desktops';

export interface CrewLaunchSelection {
  agent: string;
  model: string;
  effort: string;
  launchDesktop?: LaunchDesktopSetting;
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
  launchDesktop?: LaunchDesktopSetting;
}

const selectionFromMember = (member: CrewMember): CrewLaunchSelection => ({
  agent: member.agent ?? '',
  model: member.model ?? '',
  effort: member.effort ?? '',
  launchDesktop: member.launch_desktop,
});

// A new desktop choice is saved once the member starts on a desktop of that name and number.
const savedLabel = (wanted: LaunchDesktopSetting) => {
  const slot = desktopIdSlot(wanted.desktop_id);
  return slot ? `${slot} · ${wanted.desktop_name}` : `${wanted.desktop_name} (no ⌘ number)`;
};

const merge = (pending: Partial<CrewLaunchSelection>, member: CrewMember): CrewLaunchSelection => {
  const wanted = pending.launchDesktop;
  const saved = member.launch_desktop;
  const sameDesktop = wanted && saved?.desktop_id && (
    wanted.desktop_name ? saved.label === savedLabel(wanted) : wanted.desktop_id === saved.desktop_id
  );
  return { ...selectionFromMember(member), ...pending, ...(sameDesktop ? { launchDesktop: saved } : {}) };
};

const sameSelection = (a: CrewLaunchSelection, b: CrewLaunchSelection) => (
  a.agent === b.agent && a.model === b.model && a.effort === b.effort && JSON.stringify(a.launchDesktop) === JSON.stringify(b.launchDesktop)
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

  const observe = useCallback((member: CrewMember) => autosave.adopt(member.key, member), [autosave]);

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
