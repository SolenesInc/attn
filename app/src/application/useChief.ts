import { restartBusy, restartNotice } from '../components/crewLaunchPresentation';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { storedRouteModel, type HarnessRoute } from '../hooks/useHarnessRoute';
import type { CrewMember } from '../types/generated';

export type ChiefPhase = 'unconfigured' | 'configuring' | 'waking' | 'awake' | 'sleeping' | 'asleep';
export interface ChiefView {
  ref: string;
  key: string;
  name: string;
  agent?: string;
  model?: string;
  effort?: string;
  revision: number;
  phase: ChiefPhase;
  sessionId?: string;
  setupError?: string;
  restartError?: string;
}
export interface ChiefActions {
  setUp(route: HarnessRoute, restart?: boolean): void;
  wake(): void;
  sleep(): void;
  edit(): void;
  dismiss(): void;
}
type Pending = { key: string; phase: 'configuring' | 'waking' | 'sleeping'; sessionId?: string };

function chiefPhase(member: CrewMember, live: ReadonlySet<string>, pending: Pending | null): ChiefPhase {
  if (pending?.key === member.key && pending.phase !== 'sleeping') return pending.phase;
  if (!member.agent) return 'unconfigured';
  if (!member.binding_session) return 'asleep';
  if (pending?.key === member.key && pending.phase === 'sleeping' && pending.sessionId === member.binding_session) return 'sleeping';
  if (member.restart?.session_id === member.binding_session && restartBusy(member)) return 'sleeping';
  return live.has(member.binding_session) ? 'awake' : 'waking';
}

export function useChief(input: { liveSessionIds: ReadonlySet<string>; selectSession(id: string): void; showError(message: string): void }) {
  const crew = useDaemonStore(state => state.crew);
  const profileId = useProfilesStore(state => state.selectedProfileId);
  const member = crew.find(candidate => candidate.chief && candidate.profile_id === profileId);
  const { sendCrewSet, sendCrewWake, sendCrewSleep, sendCrewRestart } = useDaemonApi();
  const [pending, setPending] = useState<Pending | null>(null);
  const [error, setError] = useState<{ key: string; message: string } | null>(null);
  const [editor, setEditor] = useState<{ key: string; open: boolean } | null>(null);
  const currentKey = useRef(member?.key);
  useLayoutEffect(() => { currentKey.current = member?.key; }, [member?.key]);
  const isCurrent = (key: string) => currentKey.current === key;
  useEffect(() => {
    if (pending?.phase === 'sleeping' && (pending.key !== member?.key || pending.sessionId !== member.binding_session)) setPending(null);
  }, [member, pending]);
  const fail = (cause: unknown) => cause instanceof Error ? cause.message : String(cause);
  const actions: ChiefActions = {
    setUp(route, restart = false) {
      if (!member || pending?.key === member.key) return;
      const key = member.key;
      setError(null);
      setPending({ key, phase: 'configuring' });
      void (async () => {
        try {
          const outcome = await sendCrewSet({ member: key, expectedRevision: member.revision, agent: route.harness, model: storedRouteModel(route), effort: route.effort });
          if (!outcome.success) throw new Error(outcome.error || 'The launch settings were not saved.');
          if (restart && outcome.member?.binding_session) {
            const result = await sendCrewRestart({ member: key, requestId: crypto.randomUUID(), expectedSessionId: outcome.member.binding_session, expectedRevision: outcome.member.revision });
            if (!result.success) throw new Error(result.error || 'The new day could not start.');
          }
          if (!isCurrent(key)) return;
          if (outcome.wake_error) input.showError(outcome.wake_error);
          if (outcome.woke_session_id) input.selectSession(outcome.woke_session_id);
          setEditor({ key, open: false });
        } catch (cause) {
          if (isCurrent(key)) setError({ key, message: fail(cause) });
        } finally { setPending(value => value?.key === key ? null : value); }
      })();
    },
    wake() {
      if (!member?.agent || pending?.key === member.key) return;
      const key = member.key;
      setPending({ key, phase: 'waking' });
      void sendCrewWake(key).then(({ sessionId }) => { if (isCurrent(key)) input.selectSession(sessionId); })
        .catch(cause => { if (isCurrent(key)) input.showError(fail(cause)); })
        .finally(() => setPending(value => value?.key === key ? null : value));
    },
    sleep() {
      if (!member?.binding_session || pending?.key === member.key) return;
      const key = member.key;
      setPending({ key, phase: 'sleeping', sessionId: member.binding_session });
      void sendCrewSleep(key).catch(cause => {
        if (isCurrent(key)) input.showError(fail(cause));
        setPending(value => value?.key === key ? null : value);
      });
    },
    edit() { if (member) { setError(null); setEditor({ key: member.key, open: true }); } },
    dismiss() { if (member) setEditor({ key: member.key, open: false }); },
  };
  const notice = member ? restartNotice(member) : undefined;
  const restartFailed = member?.restart?.state === 'failed' && (!member.binding_session || member.restart.session_id === member.binding_session);
  const view: ChiefView | null = member ? {
    ref: `member:${member.key}`, key: member.key, name: member.name, agent: member.agent,
    model: member.model, effort: member.effort, revision: member.revision,
    phase: chiefPhase(member, input.liveSessionIds, pending), sessionId: member.binding_session,
    setupError: error?.key === member.key ? error.message : undefined,
    restartError: restartFailed && notice?.tone === 'failed' ? notice.text : undefined,
  } : null;
  const setupOpen = Boolean(member && (editor?.key === member.key ? editor.open : !member.agent));
  const setupOpenedByUser = Boolean(member && editor?.key === member.key && editor.open);
  return { view, actions, setupOpen, setupOpenedByUser };
}
