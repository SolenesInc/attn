import { useLayoutEffect, useRef, useState } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { storedRouteModel, type HarnessRoute } from '../hooks/useHarnessRoute';
import type { CrewMember } from '../types/generated';

export type ChiefPhase = 'unconfigured' | 'configuring' | 'waking' | 'awake' | 'sleeping' | 'asleep';
export interface ChiefView {
  key: string;
  name: string;
  agent?: string;
  model?: string;
  effort?: string;
  revision: number;
  phase: ChiefPhase;
  requestPending: boolean;
  sessionId?: string;
  setupError?: string;
}
export interface ChiefActions {
  setUp(route: HarnessRoute, notebookRoot?: string): Promise<boolean>;
  wake(): void;
  sleep(): void;
  edit(): void;
  dismiss(): void;
}
type Pending = { key: string; phase: 'configuring' | 'waking' | 'sleeping' };

function chiefPhase(member: CrewMember, live: ReadonlySet<string>, pending: Pending | null): ChiefPhase {
  if (pending?.key === member.id) return pending.phase;
  if (!member.agent) return 'unconfigured';
  if (!member.binding_session) return 'asleep';
  return live.has(member.binding_session) ? 'awake' : 'waking';
}

export function useChief(input: { liveSessionIds: ReadonlySet<string>; selectSession(id: string): void; showError(message: string): void }) {
  const crew = useDaemonStore(state => state.crew);
  const profileId = useProfilesStore(state => state.selectedProfileId);
  const { settings, sendCrewSet, sendCrewWake, sendCrewSleep } = useDaemonApi();
  const member = settings.crew_v2_enabled === 'true'
    ? crew.find(candidate => candidate.chief && candidate.profile_id === profileId)
    : undefined;
  const [pending, setPending] = useState<Pending | null>(null);
  const [error, setError] = useState<{ key: string; message: string } | null>(null);
  const [editor, setEditor] = useState<{ key: string; open: boolean } | null>(null);
  const currentKey = useRef(member?.id);
  useLayoutEffect(() => { currentKey.current = member?.id; }, [member?.id]);
  const isCurrent = (key: string) => currentKey.current === key;
  const fail = (cause: unknown) => cause instanceof Error ? cause.message : String(cause);
  const wakeChief = (key: string) => {
    setPending({ key, phase: 'waking' });
    return sendCrewWake(key).then(({ sessionId }) => { if (isCurrent(key)) input.selectSession(sessionId); })
      .catch(cause => { if (isCurrent(key)) input.showError(fail(cause)); })
      .finally(() => setPending(value => value?.key === key ? null : value));
  };
  const actions: ChiefActions = {
    async setUp(route, notebookRoot) {
      if (!member || pending?.key === member.id) return false;
      const key = member.id;
      const firstRun = !member.agent;
      setError(null);
      setEditor({ key, open: true });
      setPending({ key, phase: 'configuring' });
      try {
        const outcome = await sendCrewSet({ member: key, expectedRevision: member.revision, agent: route.harness, model: storedRouteModel(route), effort: route.effort, notebookRoot });
        if (!outcome.success) throw new Error(outcome.error || 'The launch settings were not saved.');
      } catch (cause) {
        if (isCurrent(key)) setError({ key, message: fail(cause) });
        setPending(value => value?.key === key ? null : value);
        return false;
      }
      if (!isCurrent(key)) return false;
      setEditor({ key, open: false });
      if (firstRun) void wakeChief(key);
      else setPending(value => value?.key === key ? null : value);
      return true;
    },
    wake() {
      if (!member?.agent || pending?.key === member.id) return;
      void wakeChief(member.id);
    },
    sleep() {
      if (!member?.binding_session || pending?.key === member.id) return;
      const key = member.id;
      setPending({ key, phase: 'sleeping' });
      void sendCrewSleep(key).catch(cause => {
        if (isCurrent(key)) input.showError(fail(cause));
      }).finally(() => setPending(value => value?.key === key ? null : value));
    },
    edit() { if (member) { setError(null); setEditor({ key: member.id, open: true }); } },
    dismiss() { if (member) setEditor({ key: member.id, open: false }); },
  };
  const view: ChiefView | null = member ? {
    key: member.id, name: member.name, agent: member.agent,
    model: member.model, effort: member.effort, revision: member.revision,
    phase: chiefPhase(member, input.liveSessionIds, pending), sessionId: member.binding_session,
    requestPending: pending?.key === member.id,
    setupError: error?.key === member.id ? error.message : undefined,
  } : null;
  const setupOpen = Boolean(member && (editor?.key === member.id ? editor.open : !member.agent));
  const setupOpenedByUser = Boolean(member && editor?.key === member.id && editor.open);
  return { view, actions, setupOpen, setupOpenedByUser };
}
