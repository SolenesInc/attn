import type { DaemonSession } from '../hooks/useDaemonSocket';
import type { CrewLaunchEdit, CrewLaunchSaveState } from '../hooks/useCrewLaunchAutosave';
import type { CrewRestartAttempt } from '../hooks/useCrewRestart';
import type { CrewMember, DelegationModel } from '../types/generated';

export function effectiveMember(roster: CrewMember, acknowledged?: CrewMember): CrewMember {
  if (!acknowledged || roster.revision > acknowledged.revision) return roster;
  return acknowledged;
}

export function modelLabel(model: DelegationModel): string {
  const name = model.name || model.id;
  return model.provider ? `${model.provider} / ${name}` : name;
}

export function modelIdentity(model: DelegationModel): string {
  return model.provider ? `${model.provider}/${model.id}` : model.id;
}

export function currentModel(catalog: DelegationModel[] | undefined, id: string): DelegationModel | undefined {
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

export function nextWakeLabel(edit: CrewLaunchEdit): string {
  return [
    edit.acknowledged.resolved_agent,
    edit.acknowledged.resolved_model || 'default model',
    edit.acknowledged.resolved_effort || 'default effort',
  ].join(' / ');
}

export function restartBusy(member: CrewMember, attempt?: CrewRestartAttempt): boolean {
  return Boolean(attempt?.sending) || member.restart?.state === 'queued' || member.restart?.state === 'requested';
}

export interface RestartNotice {
  tone: 'complete' | 'failed' | 'pending';
  text: string;
  action?: { label: string; kind: 'review' | 'resend' };
}

export function restartNotice(member: CrewMember, attempt?: CrewRestartAttempt): RestartNotice | undefined {
  const restart = !attempt || member.restart?.request_id === attempt.requestId ? member.restart : undefined;
  if (restart?.state === 'completed') {
    const successor = restart.successor_session_id ? ` · ${restart.successor_session_id.slice(0, 8)}` : '';
    return { tone: 'complete', text: `New day started${successor}` };
  }
  if (restart?.state === 'failed') {
    return { tone: 'failed', text: restart.error || 'The restart failed.', action: { label: 'Try again', kind: 'review' } };
  }
  if (restart) {
    const label = restart.state === 'requested' ? 'Handoff requested' : 'Queued for delivery';
    return { tone: 'pending', text: `${label}${restart.detail ? ` · ${restart.detail}` : ''}` };
  }
  if (attempt?.transportError) {
    if (attempt.conflict) {
      return { tone: 'failed', text: 'The member changed before this request landed.', action: { label: 'Review and try again', kind: 'review' } };
    }
    return { tone: 'failed', text: attempt.transportError, action: { label: 'Retry delivery', kind: 'resend' } };
  }
  return undefined;
}
