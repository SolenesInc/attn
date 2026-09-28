import { useCallback, useMemo, useState } from 'react';
import { withFreshDesktopRevisions } from '../hooks/desktopRevisions';
import { OPENER_EXTENSIONS } from '../components/palette/MarkdownOpener';
import { resolveMarkdownOpenerTarget } from '../components/palette/openerTarget';
import { claimPaletteFocus } from '../components/palette/paletteClaim';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { resolveEditorTileRoot, serializeNotebookTileParams } from '../types/desktop';
import { AppContentProps } from './appSupport';

interface Options {
  settings: AppContentProps['settings'];
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  contextSessionId: string | null;
  showError: (message: string) => void;
}

function failureMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function currentDesktop() {
  const { desktops, currentDesktopId } = useProfilesStore.getState();
  return desktops.find((desktop) => desktop.id === currentDesktopId);
}

export function useDesktopTiles({ settings, sessions, contextSessionId, showError }: Options) {
  const { sendRecentFiles, sendFsIndex, sendDesktopDockTile } = useDaemonApi();
  const [markdownOpenerOpen, setMarkdownOpenerOpen] = useState(false);

  // A unique tile id every time: the daemon treats a duplicate id as a move.
  const handleOpenMarkdownFile = useCallback(() => {
    if (claimPaletteFocus()) return;
    setMarkdownOpenerOpen(true);
  }, []);

  const markdownOpenerTarget = useMemo(
    () =>
      resolveMarkdownOpenerTarget(
        sessions.find((session) => session.id === contextSessionId),
        settings['notebook.root.effective'],
      ),
    [contextSessionId, sessions, settings],
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
    const activeSession = sessions.find((session) => session.id === contextSessionId);
    const localDirectory = activeSession && !activeSession.endpointId ? activeSession.cwd : '';
    const root = resolveEditorTileRoot(localDirectory, settings['notebook.root.effective'] || '');
    const tileId = `notebook-tile-${crypto.randomUUID()}`;
    const intent = useSessionStore.getState().beginIntent({ kind: 'leaf', desktopId: desktop.id, leafId: tileId });
    void withFreshDesktopRevisions([desktop.id], (revisionOf) =>
      sendDesktopDockTile({
        desktopId: desktop.id,
        expectedRevision: revisionOf(desktop.id),
        tileId,
        tileKind: 'notebook',
        tileParams: root ? serializeNotebookTileParams({ root }) : undefined,
        edge: 'right',
        tileShare: 0.4,
      }),
    ).catch((error) => {
      useSessionStore.getState().intentFailed(intent);
      showError(`Could not open the notebook: ${failureMessage(error)}`);
    });
  }, [contextSessionId, sendDesktopDockTile, settings, sessions, showError]);

  return {
    markdownOpenerOpen,
    setMarkdownOpenerOpen,
    handleOpenMarkdownFile,
    markdownOpenerTarget,
    loadOpenerRecents,
    loadOpenerIndex,
    handleOpenNotebookTile,
  };
}
