import { useCallback, useMemo, useState } from 'react';
import { withFreshDesktopRevisions } from '../hooks/desktopRevisions';
import type { Desktop } from '../types/generated';
import { OPENER_EXTENSIONS } from '../components/palette/MarkdownOpener';
import { resolveMarkdownOpenerTarget } from '../components/palette/openerTarget';
import { claimPaletteFocus } from '../components/palette/paletteClaim';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { resolveEditorTileRoot, serializeNotebookTileParams } from '../types/workspace';
import { appViewTileKind } from '../utils/appBundle';
import { AppContentProps } from './appSupport';

interface Options {
  settings: AppContentProps['settings'];
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  activeSessionId: string | null;
  focusedLeafOn: (desktop: Desktop) => string;
  showError: (message: string) => void;
}

function failureMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function currentDesktop() {
  const { desktops, currentDesktopId } = useProfilesStore.getState();
  return desktops.find((desktop) => desktop.id === currentDesktopId);
}

export function useDesktopTiles({ settings, sessions, activeSessionId, focusedLeafOn, showError }: Options) {
  const { sendRecentFiles, sendFsIndex, sendDesktopDockTile } = useDaemonApi();
  const [markdownOpenerOpen, setMarkdownOpenerOpen] = useState(false);
  const [appViewParamsPrompt, setAppViewParamsPrompt] = useState<{
    app: string;
    view: string;
    viewTitle: string;
    label: string;
    placeholder?: string;
  } | null>(null);

  // A unique tile id every time: the daemon treats a duplicate id as a move.
  const handleOpenMarkdownFile = useCallback(() => {
    if (claimPaletteFocus()) return;
    setMarkdownOpenerOpen(true);
  }, []);

  const markdownOpenerTarget = useMemo(
    () =>
      resolveMarkdownOpenerTarget(
        sessions.find((session) => session.id === activeSessionId),
        settings['notebook.root.effective'],
      ),
    [sessions, activeSessionId, settings],
  );
  const loadOpenerRecents = useCallback(
    () =>
      sendRecentFiles(50, markdownOpenerTarget.root || undefined).then((files) =>
        files.map((file) => ({ path: file.path, lastAt: file.lastAt })),
      ),
    [sendRecentFiles, markdownOpenerTarget.root],
  );
  const loadOpenerIndex = useCallback(
    (root: string) => sendFsIndex(root, OPENER_EXTENSIONS),
    [sendFsIndex],
  );

  const handleOpenNotebookTile = useCallback(() => {
    const desktop = currentDesktop();
    if (!desktop) return;
    const activeSession = sessions.find((session) => session.id === activeSessionId);
    const localDirectory = activeSession && !activeSession.endpointId ? activeSession.cwd : '';
    const root = resolveEditorTileRoot(localDirectory, settings['notebook.root.effective'] || '');
    const tileId = `notebook-tile-${crypto.randomUUID()}`;
    void withFreshDesktopRevisions([desktop.id], (revisionOf) =>
      sendDesktopDockTile({
        desktopId: desktop.id,
        expectedRevision: revisionOf(desktop.id),
        tileId,
        tileKind: 'notebook',
        tileParams: root ? serializeNotebookTileParams({ root }) : undefined,
        anchorId: focusedLeafOn(desktop) || undefined,
        edge: 'right',
        tileShare: 0.4,
      }),
    ).catch((error) => showError(`Could not open the notebook: ${failureMessage(error)}`));
  }, [sendDesktopDockTile, settings, sessions, activeSessionId, focusedLeafOn, showError]);

  // A fresh tile id every time: the daemon reads a duplicate id as a move.
  const dockAppViewTile = useCallback(
    (app: string, view: string, params: string) => {
      const desktop = currentDesktop();
      if (!desktop) return;
      const tileId = `app-view-tile-${crypto.randomUUID()}`;
      void withFreshDesktopRevisions([desktop.id], (revisionOf) =>
        sendDesktopDockTile({
          desktopId: desktop.id,
          expectedRevision: revisionOf(desktop.id),
          tileId,
          tileKind: appViewTileKind(app, view),
          tileParams: params || undefined,
          anchorId: focusedLeafOn(desktop) || undefined,
          edge: 'right',
          tileShare: 0.4,
        }),
      ).catch((error) => showError(`Could not open that view: ${failureMessage(error)}`));
    },
    [focusedLeafOn, sendDesktopDockTile, showError],
  );

  return {
    markdownOpenerOpen,
    setMarkdownOpenerOpen,
    appViewParamsPrompt,
    setAppViewParamsPrompt,
    handleOpenMarkdownFile,
    markdownOpenerTarget,
    loadOpenerRecents,
    loadOpenerIndex,
    handleOpenNotebookTile,
    dockAppViewTile,
  };
}
