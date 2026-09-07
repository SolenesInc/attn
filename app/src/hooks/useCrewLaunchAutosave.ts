import { useCallback, useEffect, useReducer, useRef } from 'react';
import type { CrewMember } from '../types/generated';
import type { CrewMutationOutcome } from './daemonCrewEvents';

export interface CrewLaunchSelection {
  agent: string;
  model: string;
  effort: string;
}

export type CrewLaunchSaveState = 'saved' | 'saving' | 'error';

interface MemberEdit {
  acknowledged: CrewMember;
  pending: Partial<CrewLaunchSelection>;
  generation: Record<keyof CrewLaunchSelection, number>;
  state: CrewLaunchSaveState;
  error?: string;
  retryOnReconnect: boolean;
  uncertainWrite: boolean;
}

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

const sameSelection = (a: CrewLaunchSelection, b: CrewLaunchSelection) => (
  a.agent === b.agent && a.model === b.model && a.effort === b.effort
);

const selectionKeys = ['agent', 'model', 'effort'] as const;

function sameMemberSnapshot(a: CrewMember, b: CrewMember): boolean {
  return a.revision === b.revision
    && a.agent === b.agent
    && a.model === b.model
    && a.effort === b.effort
    && a.resolved_agent === b.resolved_agent
    && a.resolved_model === b.resolved_model
    && a.resolved_effort === b.resolved_effort
    && a.binding_session === b.binding_session
    && a.cwd === b.cwd
    && JSON.stringify(a.restart) === JSON.stringify(b.restart);
}

function newerResultMember(current: CrewMember, candidate: CrewMember): CrewMember {
  return candidate.revision > current.revision ? candidate : current;
}

function hasPending(edit: MemberEdit, key: keyof CrewLaunchSelection): boolean {
  return Object.prototype.hasOwnProperty.call(edit.pending, key);
}

function desiredSelection(edit: MemberEdit): CrewLaunchSelection {
  const saved = selectionFromMember(edit.acknowledged);
  return {
    agent: hasPending(edit, 'agent') ? edit.pending.agent! : saved.agent,
    model: hasPending(edit, 'model') ? edit.pending.model! : saved.model,
    effort: hasPending(edit, 'effort') ? edit.pending.effort! : saved.effort,
  };
}

function settleObservedSelection(edit: MemberEdit, memberId: string, active: Set<string>): boolean {
  if (active.has(memberId) || edit.uncertainWrite || Object.keys(edit.pending).length === 0) return false;
  if (!sameSelection(desiredSelection(edit), selectionFromMember(edit.acknowledged))) return false;
  edit.pending = {};
  edit.state = 'saved';
  edit.error = undefined;
  edit.retryOnReconnect = false;
  return true;
}

export function useCrewLaunchAutosave(
  members: CrewMember[],
  connectionGeneration: number,
  send: (write: CrewLaunchWrite) => Promise<CrewMutationOutcome>,
) {
  const edits = useRef(new Map<string, MemberEdit>());
  const active = useRef(new Set<string>());
  const nextGeneration = useRef(0);
  const sendRef = useRef(send);
  sendRef.current = send;
  const [, redraw] = useReducer((value) => value + 1, 0);
  const pumpRef = useRef<(memberId: string) => void>(() => {});

  const observe = useCallback((member: CrewMember) => {
    const edit = edits.current.get(member.id);
    if (!edit) {
      edits.current.set(member.id, {
        acknowledged: member,
        pending: {},
        generation: { agent: 0, model: 0, effort: 0 },
        state: 'saved',
        retryOnReconnect: false,
        uncertainWrite: false,
      });
      return true;
    }
    if (member.revision < edit.acknowledged.revision || sameMemberSnapshot(member, edit.acknowledged)) return false;
    edit.acknowledged = member;
    settleObservedSelection(edit, member.id, active.current);
    return true;
  }, []);

  useEffect(() => {
    let changed = false;
    for (const member of members) changed = observe(member) || changed;
    if (changed) redraw();
  }, [members, observe]);

  pumpRef.current = (memberId: string) => {
    const edit = edits.current.get(memberId);
    if (!edit || active.current.has(memberId)) return;
    const pendingKeys = selectionKeys.filter((key) => hasPending(edit, key));
    const acknowledged = selectionFromMember(edit.acknowledged);
    const desired = desiredSelection(edit);
    if (pendingKeys.length === 0 || (sameSelection(desired, acknowledged) && !edit.uncertainWrite)) {
      edit.pending = {};
      edit.state = 'saved';
      edit.error = undefined;
      edit.retryOnReconnect = false;
      redraw();
      return;
    }

    const submitted = desired;
    const submittedPending = new Set(pendingKeys);
    const submittedGeneration = { ...edit.generation };
    const expectedRevision = edit.acknowledged.revision;
    active.current.add(memberId);
    edit.state = 'saving';
    edit.error = undefined;
    redraw();

    void sendRef.current({ member: memberId, expectedRevision, ...submitted }).then((outcome) => {
      active.current.delete(memberId);
      const current = edits.current.get(memberId);
      if (!current) return;
      const fencedUncertainWrite = Boolean(outcome.member && outcome.member.revision > expectedRevision);
      if (outcome.member) current.acknowledged = newerResultMember(current.acknowledged, outcome.member);

      if (!outcome.success) {
        if (fencedUncertainWrite) current.uncertainWrite = false;
        if (outcome.conflict && outcome.member) {
          const settled = settleObservedSelection(current, memberId, active.current);
          current.state = settled ? 'saved' : 'error';
          current.error = settled ? undefined : outcome.error || 'Launch settings changed elsewhere. Review the saved values and retry.';
          redraw();
          return;
        }
        current.state = 'error';
        current.error = outcome.error || 'The launch settings were not saved.';
        current.retryOnReconnect = false;
        redraw();
        return;
      }

      if (!outcome.member) {
        current.uncertainWrite = true;
        current.state = 'error';
        current.error = 'The daemon did not return the saved launch settings.';
        current.retryOnReconnect = false;
        redraw();
        return;
      }

      current.retryOnReconnect = false;
      current.uncertainWrite = false;
      for (const key of submittedPending) {
        if (current.generation[key] !== submittedGeneration[key]) continue;
        delete current.pending[key];
      }
      redraw();
      pumpRef.current(memberId);
    }).catch((error) => {
      active.current.delete(memberId);
      const current = edits.current.get(memberId);
      if (!current) return;
      const message = error instanceof Error ? error.message : String(error);
      current.state = 'error';
      current.error = message;
      current.uncertainWrite = true;
      current.retryOnReconnect = /not connected|connection|socket/i.test(message);
      redraw();
    });
  };

  useEffect(() => {
    if (connectionGeneration === 0) return;
    for (const [memberId, edit] of edits.current) {
      if (!edit.retryOnReconnect) continue;
      edit.retryOnReconnect = false;
      edit.state = 'saving';
      pumpRef.current(memberId);
    }
  }, [connectionGeneration]);

  const update = useCallback((memberId: string, patch: Partial<CrewLaunchSelection>) => {
    const edit = edits.current.get(memberId);
    if (!edit) return;
    for (const key of selectionKeys) {
      if (!(key in patch)) continue;
      edit.pending[key] = patch[key];
      edit.generation[key] = ++nextGeneration.current;
    }
    edit.state = 'saving';
    edit.error = undefined;
    edit.retryOnReconnect = false;
    redraw();
    pumpRef.current(memberId);
  }, []);

  const retry = useCallback((memberId: string) => {
    const edit = edits.current.get(memberId);
    if (!edit) return;
    edit.state = 'saving';
    edit.error = undefined;
    edit.retryOnReconnect = false;
    redraw();
    pumpRef.current(memberId);
  }, []);

  const read = useCallback((memberId: string): CrewLaunchEdit | undefined => {
    const edit = edits.current.get(memberId);
    if (!edit) return undefined;
    return {
      acknowledged: edit.acknowledged,
      draft: desiredSelection(edit),
      state: edit.state,
      ...(edit.error ? { error: edit.error } : {}),
    };
  }, []);

  const observeMember = useCallback((member: CrewMember) => {
    if (observe(member)) redraw();
  }, [observe]);

  return { read, update, retry, observe: observeMember };
}
