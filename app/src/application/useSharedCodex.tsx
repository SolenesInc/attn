import { useCallback, useMemo } from 'react';
import type { PaletteCommand } from '../components/palette/paletteCommands';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { CodexInstallUntracked } from '../hooks/daemonSharedCodexEvents';
import { useDaemonStore } from '../store/daemonSessions';
import { useProfilesStore } from '../store/profiles';
import { canRestartNow, staleSessionsOf, useSharedCodexStore } from '../store/sharedCodex';
import {
  SharedCodexBlockerReason,
  SharedCodexRestart,
  type SharedCodexBlocker,
  type SharedCodexState,
} from '../types/generated';
import type { OldTilesDismissal } from '../utils/codexBannerDismissals';
import { sessionDisplayName } from '../utils/sessionDisplayName';
import { useSessionLifecycleContext } from './AppContexts';
import { ContextActionIcon } from './AppIcons';
import { isNewerVersion } from './appSupport';

export type SharedCodexStage =
  | 'failed'
  | 'draining'
  | 'restarting'
  | 'pending'
  | 'installing'
  | 'available'
  | 'oldTiles';

export interface SharedCodexDismissals {
  update: string | null;
  oldTiles: OldTilesDismissal | null;
}

interface InstallProgress {
  installing: boolean;
  error: string | null;
}

export function codexUpdateAvailable(state: SharedCodexState): boolean {
  return Boolean(state.installed_version && state.latest_version && isNewerVersion(state.installed_version, state.latest_version));
}

export function oldTilesDismissed(state: SharedCodexState, dismissal: OldTilesDismissal | null): boolean {
  if (!dismissal || dismissal.running !== (state.running_version ?? '')) return false;
  const dismissed = new Set(dismissal.sessions);
  return staleSessionsOf(state).every((session) => dismissed.has(session));
}

export function sharedCodexStage(
  state: SharedCodexState,
  install: InstallProgress,
  dismissals: SharedCodexDismissals,
): SharedCodexStage | null {
  switch (state.restart) {
    case SharedCodexRestart.Failed:
      return 'failed';
    case SharedCodexRestart.Restarting:
      return state.force_at ? 'draining' : 'restarting';
    case SharedCodexRestart.Pending:
      return 'pending';
  }
  if (install.installing) return 'installing';
  if (codexUpdateAvailable(state) && (install.error || dismissals.update !== state.latest_version)) return 'available';
  if (staleSessionsOf(state).length > 0 && !oldTilesDismissed(state, dismissals.oldTiles)) return 'oldTiles';
  return null;
}

export function useSharedCodexState(): SharedCodexState | null {
  const selectedProfileId = useProfilesStore((store) => store.selectedProfileId);
  const profileId = useSharedCodexStore((store) => store.profileId);
  const state = useSharedCodexStore((store) => store.state);
  return profileId === selectedProfileId ? state : null;
}

export function useSharedCodexConfirm() {
  const state = useSharedCodexState();
  const confirm = useSharedCodexStore((store) => store.confirm);
  return state ? confirm : null;
}

export const BLOCKER_REASON: Record<SharedCodexBlockerReason, string> = {
  [SharedCodexBlockerReason.Turn]: 'working',
  [SharedCodexBlockerReason.Approval]: 'waiting on an approval',
  [SharedCodexBlockerReason.Question]: 'asking a question',
  [SharedCodexBlockerReason.Starting]: 'still starting or asking whether to trust its folder',
};

export const BLOCKER_LOSS: Record<SharedCodexBlockerReason, string> = {
  [SharedCodexBlockerReason.Turn]: 'Turn running. Gets up to 60s to finish, then it is stopped.',
  [SharedCodexBlockerReason.Approval]: 'Waiting for your approval. The request is lost.',
  [SharedCodexBlockerReason.Question]: 'Asking you a question. The question is lost.',
  [SharedCodexBlockerReason.Starting]: 'Still starting. Its tile relaunches.',
};

export interface NamedSession {
  id: string;
  name: string;
  hidden: boolean;
}

export function useNamedSessions(ids: readonly string[]): NamedSession[] {
  const daemonSessions = useDaemonStore((state) => state.daemonSessions);
  return useMemo(() => ids.map((id) => {
    const session = daemonSessions.find((candidate) => candidate.id === id);
    return { id, name: session ? sessionDisplayName(session) : id, hidden: session?.hidden === true };
  }), [daemonSessions, ids]);
}

export function blockerSessionIds(blockers: readonly SharedCodexBlocker[] | undefined): string[] {
  return (blockers ?? []).map((blocker) => blocker.session_id);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function useSharedCodexActions() {
  const { sendInstallCodexUpdate, sendRestartSharedCodex } = useDaemonApi();
  const { handleReloadSession } = useSessionLifecycleContext();
  const { showError } = useToast();

  const install = useCallback(() => {
    const store = useSharedCodexStore.getState();
    const desktopId = useProfilesStore.getState().currentDesktopId;
    if (store.installing || !desktopId) return;
    store.installStarted();
    sendInstallCodexUpdate(desktopId).then(
      () => useSharedCodexStore.getState().installFinished(null),
      (error: unknown) => useSharedCodexStore.getState().installFinished(
        error instanceof CodexInstallUntracked ? null : errorMessage(error),
      ),
    );
  }, [sendInstallCodexUpdate]);

  const restart = useCallback((now: boolean) => {
    const { profileId, closeConfirm } = useSharedCodexStore.getState();
    closeConfirm();
    if (!profileId) return;
    sendRestartSharedCodex(profileId, now).catch((error: unknown) => {
      showError(`Couldn't restart shared Codex: ${errorMessage(error)}`);
    });
  }, [sendRestartSharedCodex, showError]);

  const reloadOldTiles = useCallback(() => {
    const { state, closeConfirm } = useSharedCodexStore.getState();
    closeConfirm();
    for (const sessionId of staleSessionsOf(state)) handleReloadSession(sessionId);
  }, [handleReloadSession]);

  return { install, restart, reloadOldTiles, relaunch: handleReloadSession };
}

export function useSharedCodexCommands(): PaletteCommand[] {
  const state = useSharedCodexState();
  const installing = useSharedCodexStore((store) => store.installing);
  const openConfirm = useSharedCodexStore((store) => store.openConfirm);
  const { install, restart } = useSharedCodexActions();
  return useMemo(() => {
    if (!state) return [];
    const commands: PaletteCommand[] = [];
    if (state.restart === SharedCodexRestart.Failed) {
      commands.push({
        id: 'retry-shared-codex-restart',
        pinned: true,
        title: 'Retry shared Codex restart',
        description: `Try starting Codex ${state.installed_version} again`,
        keywords: ['codex', 'shared', 'restart', 'retry', 'update', 'app-server'],
        icon: <ContextActionIcon />,
        run: () => restart(false),
      });
    }
    if (codexUpdateAvailable(state) && !installing) {
      commands.push({
        id: 'install-codex-update',
        pinned: true,
        title: 'Install Codex update',
        description: `Install Codex ${state.latest_version} in a terminal tile`,
        keywords: ['codex', 'update', 'upgrade', 'install', 'version'],
        icon: <ContextActionIcon />,
        run: install,
      });
    }
    if (canRestartNow(state)) {
      commands.push({
        id: 'restart-shared-codex-now',
        pinned: true,
        title: 'Restart shared Codex now',
        description: `Restart on ${state.installed_version} without waiting for idle agents`,
        keywords: ['codex', 'shared', 'restart', 'update', 'now', 'app-server'],
        icon: <ContextActionIcon />,
        run: () => openConfirm('restart'),
      });
    }
    const stale = staleSessionsOf(state).length;
    if (stale > 0) {
      commands.push({
        id: 'reload-old-codex-tiles',
        pinned: true,
        title: 'Reload old Codex tiles',
        description: stale === 1 ? '1 tile still runs an older Codex' : `${stale} tiles still run an older Codex`,
        keywords: ['codex', 'reload', 'tiles', 'old', 'update', 'restart'],
        icon: <ContextActionIcon />,
        run: () => openConfirm('reload'),
      });
    }
    return commands;
  }, [install, installing, openConfirm, restart, state]);
}
