import { useCallback, useMemo, useState } from 'react';
import { OPENER_EXTENSIONS } from '../components/palette/MarkdownOpener';
import { claimPaletteFocus } from '../components/palette/paletteClaim';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { resolveEditorTileRoot, serializeNotebookTileParams } from '../types/desktop';
import { localDirectory } from '../utils/outpostPaths';
import { AppContentProps } from './appSupport';

interface Options {
  settings: AppContentProps['settings'];
  sessions: Array<ReturnType<typeof useSessionStore.getState>['sessions'][number] & { host?: string }>;
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

  const contextSession = sessions.find((entry) => entry.id === contextSessionId);
  const openerRoot = localDirectory(contextSession) || settings['notebook.root.effective'] || null;
  const openerSessionId = contextSession?.id ?? '';
  const editorTileRoot = resolveEditorTileRoot(localDirectory(contextSession), settings['notebook.root.effective'] || '');
  const markdownOpenerTarget = useMemo(
    () => ({ root: openerRoot, sessionId: openerSessionId }),
    [openerRoot, openerSessionId],
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
    const tileId = `notebook-tile-${crypto.randomUUID()}`;
    const intent = useSessionStore.getState().beginIntent({ kind: 'leaf', desktopId: desktop.id, leafId: tileId });
    void sendDesktopDockTile({
      desktopId: desktop.id,
      tileId,
      tileKind: 'notebook',
      tileParams: editorTileRoot ? serializeNotebookTileParams({ root: editorTileRoot }) : undefined,
      edge: 'right',
      tileShare: 0.4,
    }).catch((error) => {
      useSessionStore.getState().intentFailed(intent);
      showError(`Could not open the notebook: ${failureMessage(error)}`);
    });
  }, [editorTileRoot, sendDesktopDockTile, showError]);

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
