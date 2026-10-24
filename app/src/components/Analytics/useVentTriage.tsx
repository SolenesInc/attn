import { useMemo } from 'react';
import { openPath } from '@tauri-apps/plugin-opener';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { useDaemonStore } from '../../store/daemonSessions';
import { VentSend, type Vent } from '../../types/generated';
import { nextNewVent, type ListItem } from './analyticsItems';
import type { UndoToast } from './useUndoToast';

function doIf<T>(subject: T | null | undefined | false, action: (subject: T) => void): boolean {
  if (!subject) return false;
  action(subject);
  return true;
}

export function useVentTriage({ items, index, sharing, select, feedback, onOpenSession, onOpenSeed }: {
  items: ListItem[];
  index: number;
  sharing: boolean;
  select: (key: string) => void;
  feedback: UndoToast;
  onOpenSession: (sessionId: string) => void;
  onOpenSeed: (seedId: string) => void;
}) {
  const { sendSetVentDismissed, sendPlantVentSeed, sendUnplantVentSeed, sendSetVentSend } = useDaemonApi();
  const sessions = useDaemonStore((state) => state.daemonSessions);
  const live = useMemo(() => new Set(sessions.map((session) => session.id)), [sessions]);
  const { say, run, fail } = feedback;
  const moveOn = (vent: Vent) => select(nextNewVent(items, index)?.key ?? vent.id);

  const setDismissed = (vent: Vent, dismissed: boolean) => run(async () => {
    if (dismissed) moveOn(vent);
    await sendSetVentDismissed(vent.id, dismissed);
    const name = `“${vent.context.session_label}”`;
    say(dismissed ? `Dismissed ${name}.` : `Restored ${name}.`, () => sendSetVentDismissed(vent.id, !dismissed));
  });

  const plant = (vent: Vent) => run(async () => {
    const seed = await sendPlantVentSeed(vent.id);
    moveOn(vent);
    say(<>Planted <b>{seed.step_slug}</b> from “{vent.context.session_label}”.</>, () => sendUnplantVentSeed(vent.id));
  });

  const setSend = (vent: Vent, send: boolean) => run(async () => {
    await sendSetVentSend(vent.id, send);
    const name = `“${vent.context.session_label}”`;
    say(send ? `Sending ${name} with the next batch.` : `Keeping ${name} here.`, () => sendSetVentSend(vent.id, !send));
  });

  const actions: Record<string, (vent: Vent) => boolean> = {
    o: (vent) => doIf(live.has(vent.session_id), () => onOpenSession(vent.session_id)),
    t: ({ context: { transcript } }) => doIf(transcript, (found) => void openPath(found.path).catch(fail)),
    g: ({ context: { seed_id } }) => doIf(seed_id, onOpenSeed),
    p: (vent) => doIf(!vent.withdrawn_at && !vent.planted_seed_id, () => plant(vent)),
    d: (vent) => doIf(!vent.withdrawn_at, () => setDismissed(vent, !vent.dismissed_at)),
    x: (vent) => doIf(!vent.withdrawn_at && vent.send !== VentSend.Sent && (sharing || Boolean(vent.send)), () => setSend(vent, !vent.send)),
  };

  return {
    isLive: (vent: Vent) => live.has(vent.session_id),
    act: (key: string, vent: Vent): boolean => actions[key]?.(vent) ?? false,
  };
}
