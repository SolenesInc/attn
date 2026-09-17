import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { OPENER_EXTENSIONS } from '../components/palette/MarkdownOpener';
import { resolveMarkdownOpenerTarget } from '../components/palette/openerTarget';
import { claimPaletteFocus } from '../components/palette/paletteClaim';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { DaemonWorkspace } from '../hooks/useDaemonSocket';
import { useSessionStore } from '../store/sessions';
import {
  localWorkspaceDirectory,
  resolveEditorTileRoot,
  serializeNotebookTileParams,
  soleWorkspaceForId,
} from '../types/workspace';
import { appViewTileKind } from '../utils/appBundle';
import { AppContentProps } from './appSupport';

interface Options {
  settings: AppContentProps['settings'];
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  daemonWorkspaces: AppContentProps['daemonWorkspaces'];
  activeSessionId: string | null;
  activeWorkspaceIdRef: React.RefObject<string | null>;
}
export function useWorkspaceTiles({
  settings,
  sessions,
  daemonWorkspaces,
  activeSessionId,
  activeWorkspaceIdRef,
}: Options) {
  const { sendRecentFiles, sendFsIndex, sendWorkspaceDockTile } = useDaemonApi();
  const [markdownOpenerOpen, setMarkdownOpenerOpen] = useState(false);
  const [appViewParamsPrompt, setAppViewParamsPrompt] = useState<{
    app: string;
    view: string;
    viewTitle: string;
    label: string;
    placeholder?: string;
  } | null>(null);

  const daemonWorkspacesRef = useRef<DaemonWorkspace[]>([]);
  useEffect(() => {
    daemonWorkspacesRef.current = daemonWorkspaces;
  }, [daemonWorkspaces]);

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
    const workspaceId = activeWorkspaceIdRef.current;
    if (!workspaceId) return;
    const tileId = `notebook-tile-${crypto.randomUUID()}`;
    // A twin across endpoints forfeits the workspace-dir default rather than adopt a remote directory: the active id carries no endpoint identity.
    const workspace = soleWorkspaceForId(daemonWorkspacesRef.current, workspaceId);
    const localDirectory = localWorkspaceDirectory(workspace);

    const effectiveNotebookRoot = settings['notebook.root.effective'] || '';
    const root = resolveEditorTileRoot(localDirectory, effectiveNotebookRoot);
    void sendWorkspaceDockTile(workspaceId, tileId, 'notebook', {
      edge: 'right',
      ratio: 0.4,
      tileParams: root ? serializeNotebookTileParams({ root }) : undefined,
    }).catch((error) => {
      console.warn('[App] Failed to dock notebook tile:', error);
    });
  }, [sendWorkspaceDockTile, settings, activeWorkspaceIdRef]);

  // A fresh tile id every time: the daemon reads a duplicate id as a move.
  const dockAppViewTile = useCallback(
    (app: string, view: string, params: string) => {
      const workspaceId = activeWorkspaceIdRef.current;
      if (!workspaceId) return;
      void sendWorkspaceDockTile(
        workspaceId,
        `app-view-tile-${crypto.randomUUID()}`,
        appViewTileKind(app, view),
        { edge: 'right', ratio: 0.4, ...(params ? { tileParams: params } : {}) },
      ).catch((error) => {
        console.warn('[App] Failed to dock app view tile:', error);
      });
    },
    [sendWorkspaceDockTile, activeWorkspaceIdRef],
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
