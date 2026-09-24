import { useCallback, useMemo, useState } from 'react';
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
}

function currentDesktop() {
  const { desktops, currentDesktopId } = useProfilesStore.getState();
  return desktops.find((desktop) => desktop.id === currentDesktopId);
}

export function useDesktopTiles({ settings, sessions, activeSessionId, focusedLeafOn }: Options) {
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
    void sendDesktopDockTile({
      desktopId: desktop.id,
      expectedRevision: desktop.revision,
      tileId: `notebook-tile-${crypto.randomUUID()}`,
      tileKind: 'notebook',
      tileParams: root ? serializeNotebookTileParams({ root }) : undefined,
      anchorId: focusedLeafOn(desktop) || undefined,
      edge: 'right',
      tileShare: 0.4,
    }).catch((error) => {
      console.warn('[App] Failed to dock notebook tile:', error);
    });
  }, [sendDesktopDockTile, settings, sessions, activeSessionId, focusedLeafOn]);

  // A fresh tile id every time: the daemon reads a duplicate id as a move.
  const dockAppViewTile = useCallback(
    (app: string, view: string, params: string) => {
      const desktop = currentDesktop();
      if (!desktop) return;
      void sendDesktopDockTile({
        desktopId: desktop.id,
        expectedRevision: desktop.revision,
        tileId: `app-view-tile-${crypto.randomUUID()}`,
        tileKind: appViewTileKind(app, view),
        tileParams: params || undefined,
        anchorId: focusedLeafOn(desktop) || undefined,
        edge: 'right',
        tileShare: 0.4,
      }).catch((error) => {
        console.warn('[App] Failed to dock app view tile:', error);
      });
    },
    [focusedLeafOn, sendDesktopDockTile],
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
