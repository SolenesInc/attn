import { useCallback, useEffect, useRef, useState } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { ProfileCommandError } from '../../hooks/daemonProfileEvents';
import { useProfilesStore } from '../../store/profiles';
import { ProfileErrorCode } from '../../types/generated';
import type { MergeChoice } from './MergeDialog';
import type { DraftDesktopView } from './migrationDraft';

export interface Notice {
  tone: 'alert' | 'info';
  text: string;
}

export interface MoveRequest {
  groupId: string;
  desktop: DraftDesktopView;
  choice: MergeChoice;
  expectedRevision: number;
  verb: 'moved to' | 'merged into';
}

const STALE_NOTICE = 'Another window changed the draft while you were editing. Showing the current draft; nothing was applied.';

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function failureNotice(error: unknown, finishing: boolean): Notice {
  if (error instanceof ProfileCommandError && error.code === ProfileErrorCode.StaleRevision) {
    return { tone: 'alert', text: STALE_NOTICE };
  }
  if (error instanceof ProfileCommandError) {
    return { tone: 'alert', text: `${errorText(error)}. Nothing was applied${finishing ? '. Retry when you’re ready.' : '.'}` };
  }
  return {
    tone: 'alert',
    text: `attn could not reach the daemon (${errorText(error)}). Your choices so far are saved; try again once it reconnects.`,
  };
}

export function currentRevision(): number {
  return useProfilesStore.getState().migration?.revision ?? 0;
}

function groupTitle(groupId: string): string {
  return useProfilesStore.getState().migration?.groups.find((group) => group.group_id === groupId)?.title ?? groupId;
}

export function useDraftReader(): Notice | null {
  const { connectionGeneration, sendMigrationGet } = useDaemonApi();
  const [readError, setReadError] = useState<Notice | null>(null);
  useEffect(() => {
    if (!connectionGeneration) return;
    let cancelled = false;
    sendMigrationGet().then(
      () => {
        if (!cancelled) setReadError(null);
      },
      (error: unknown) => {
        if (!cancelled) setReadError(failureNotice(error, false));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [connectionGeneration, sendMigrationGet]);
  return readError;
}

export function useMigrationActions() {
  const {
    connectionGeneration,
    sendMigrationGet,
    sendMigrationKeep,
    sendMigrationMove,
    sendMigrationSuggest,
    sendMigrationUndo,
    sendMigrationFinish,
  } = useDaemonApi();
  const [notice, setNotice] = useState<Notice | null>(null);
  const [status, setStatus] = useState('');
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);

  useEffect(() => {
    setNotice(null);
  }, [connectionGeneration]);

  const run = useCallback(async (send: () => Promise<unknown>, done?: () => void, finishing = false) => {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    setStatus('');
    try {
      await send();
      setNotice(null);
      done?.();
    } catch (error) {
      setNotice(failureNotice(error, finishing));
      if (error instanceof ProfileCommandError && error.code === ProfileErrorCode.StaleRevision) {
        sendMigrationGet().catch(() => undefined);
      }
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }, [sendMigrationGet]);

  const keep = useCallback((groupIds: string[], message: string, onDone?: () => void) => {
    if (groupIds.length === 0) return;
    void run(() => sendMigrationKeep(groupIds, currentRevision()), () => {
      setStatus(message);
      onDone?.();
    });
  }, [run, sendMigrationKeep]);

  const move = useCallback(({ groupId, desktop, choice, expectedRevision, verb }: MoveRequest, onDone?: () => void) => {
    const title = groupTitle(groupId);
    void run(
      () => sendMigrationMove({
        groupId,
        targetKey: desktop.desktop.key,
        anchorGroupId: choice.anchorGroupId ?? undefined,
        edge: choice.edge,
        share: choice.share,
        expectedRevision,
      }),
      () => {
        setStatus(`${title} ${verb} ${desktop.label}.`);
        onDone?.();
      },
    );
  }, [run, sendMigrationMove]);

  const undo = useCallback(() => {
    if (!useProfilesStore.getState().migration?.can_undo) return;
    void run(() => sendMigrationUndo(currentRevision()), () => setStatus('Undone.'));
  }, [run, sendMigrationUndo]);

  const suggest = useCallback((message: string) => {
    void run(() => sendMigrationSuggest(currentRevision()), () => setStatus(message));
  }, [run, sendMigrationSuggest]);

  const finish = useCallback(() => {
    void run(() => sendMigrationFinish(currentRevision()), undefined, true);
  }, [run, sendMigrationFinish]);

  const isBusy = useCallback(() => busyRef.current, []);

  return { notice, status, setStatus, busy, isBusy, keep, move, undo, suggest, finish };
}

export function departedNotice(titles: string[]): Notice | null {
  if (titles.length === 0) return null;
  const one = titles.length === 1;
  return {
    tone: 'info',
    text: `${titles.join(', ')} ${one ? 'has' : 'have'} no agents left to place, so ${one ? 'it no longer needs' : 'they no longer need'} confirming.`,
  };
}

export const CANCELLED_MOVE_NOTICE: Notice = {
  tone: 'info',
  text: 'The draft changed in another window, so that move was cancelled. Nothing was applied.',
};
