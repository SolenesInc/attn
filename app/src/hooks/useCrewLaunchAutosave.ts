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
  draft: CrewLaunchSelection;
  dirty: Set<keyof CrewLaunchSelection>;
  generation: Record<keyof CrewLaunchSelection, number>;
  state: CrewLaunchSaveState;
  error?: string;
  retryOnReconnect: boolean;
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

function newerMember(current: CrewMember, candidate: CrewMember): CrewMember {
  return candidate.revision >= current.revision ? candidate : current;
}

export function useCrewLaunchAutosave(
  members: CrewMember[],
  isConnected: boolean,
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
        draft: selectionFromMember(member),
        dirty: new Set(),
        generation: { agent: 0, model: 0, effort: 0 },
        state: 'saved',
        retryOnReconnect: false,
      });
      return true;
    }
    if (member.revision < edit.acknowledged.revision || sameMemberSnapshot(member, edit.acknowledged)) return false;
    const idle = !active.current.has(member.id) && edit.state === 'saved';
    edit.acknowledged = member;
    if (idle) {
      edit.draft = selectionFromMember(member);
      edit.dirty.clear();
    } else {
      const saved = selectionFromMember(member);
      for (const key of selectionKeys) {
        if (!edit.dirty.has(key)) {
          edit.draft[key] = saved[key];
        } else if (edit.draft[key] === saved[key]) {
          edit.dirty.delete(key);
        }
      }
      if (!active.current.has(member.id) && edit.dirty.size === 0) {
        edit.state = 'saved';
        edit.error = undefined;
        edit.retryOnReconnect = false;
      }
    }
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
    const acknowledged = selectionFromMember(edit.acknowledged);
    if (sameSelection(edit.draft, acknowledged)) {
      edit.dirty.clear();
      edit.state = 'saved';
      edit.error = undefined;
      edit.retryOnReconnect = false;
      redraw();
      return;
    }

    const submitted = { ...edit.draft };
    const submittedDirty = new Set(edit.dirty);
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
      if (outcome.member) current.acknowledged = newerMember(current.acknowledged, outcome.member);

      if (!outcome.success) {
        if (outcome.conflict && outcome.member) {
          const saved = selectionFromMember(current.acknowledged);
          for (const key of selectionKeys) {
            if (!current.dirty.has(key)) {
              current.draft[key] = saved[key];
            } else if (current.draft[key] === saved[key]) {
              current.dirty.delete(key);
            }
          }
          current.state = current.dirty.size === 0 ? 'saved' : 'error';
          current.error = current.dirty.size === 0
            ? undefined
            : outcome.error || 'Launch settings changed elsewhere. Review the saved values and retry.';
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
        current.state = 'error';
        current.error = 'The daemon did not return the saved launch settings.';
        current.retryOnReconnect = false;
        redraw();
        return;
      }

      current.retryOnReconnect = false;
      const saved = selectionFromMember(current.acknowledged);
      for (const key of selectionKeys) {
        if (current.generation[key] !== submittedGeneration[key]) continue;
        current.draft[key] = saved[key];
        if (submittedDirty.has(key)) current.dirty.delete(key);
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
      current.retryOnReconnect = /not connected|connection|socket/i.test(message);
      redraw();
    });
  };

  useEffect(() => {
    if (!isConnected) return;
    for (const [memberId, edit] of edits.current) {
      if (!edit.retryOnReconnect) continue;
      edit.retryOnReconnect = false;
      edit.state = 'saving';
      pumpRef.current(memberId);
    }
  }, [isConnected]);

  const update = useCallback((memberId: string, patch: Partial<CrewLaunchSelection>) => {
    const edit = edits.current.get(memberId);
    if (!edit) return;
    edit.draft = { ...edit.draft, ...patch };
    for (const key of selectionKeys) {
      if (!(key in patch)) continue;
      edit.dirty.add(key);
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
      draft: edit.draft,
      state: edit.state,
      ...(edit.error ? { error: edit.error } : {}),
    };
  }, []);

  const observeMember = useCallback((member: CrewMember) => {
    if (observe(member)) redraw();
  }, [observe]);

  return { read, update, retry, observe: observeMember };
}
