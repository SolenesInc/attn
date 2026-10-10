import type { DaemonSession } from '../hooks/useDaemonSocket';
import type { CrewLaunchSaveState } from '../hooks/useCrewLaunchAutosave';
import type { CrewMember, HarnessModel } from '../types/generated';

export function effectiveMember(roster: CrewMember, acknowledged?: CrewMember): CrewMember {
  if (!acknowledged || roster.revision > acknowledged.revision) return roster;
  return acknowledged;
}

export function modelLabel(model: HarnessModel): string {
  const name = model.name || model.id;
  return model.provider ? `${model.provider} / ${name}` : name;
}

export function modelIdentity(model: HarnessModel): string {
  return model.provider ? `${model.provider}/${model.id}` : model.id;
}

export function currentModel(catalog: HarnessModel[] | undefined, id: string): HarnessModel | undefined {
  return catalog?.find((model) => modelIdentity(model) === id);
}

export function runningSessionFor(member: CrewMember, sessions: DaemonSession[]): DaemonSession | undefined {
  if (!member.binding_session) return undefined;
  return sessions.find((session) => session.id === member.binding_session);
}

export function launchSaveCopy(state: CrewLaunchSaveState): string {
  if (state === 'saving') return 'Saving…';
  if (state === 'error') return 'Not saved';
  return 'Saved';
}
