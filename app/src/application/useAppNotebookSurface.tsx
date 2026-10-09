import { useCallback, useMemo } from 'react';
import { useProfilesStore } from '../store/profiles';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { fsChangeSignalKey, fsIndexToNotebookEntries } from '../utils/fsChangeSignals';
import { AppContentProps } from './appSupport';
interface Options {
  sendFsList: ReturnType<typeof useDaemonApi>['sendFsList'];
  sendFsRead: ReturnType<typeof useDaemonApi>['sendFsRead'];
  sendFsWrite: ReturnType<typeof useDaemonApi>['sendFsWrite'];
  sendFsExists: ReturnType<typeof useDaemonApi>['sendFsExists'];
  sendFsReadAsset: ReturnType<typeof useDaemonApi>['sendFsReadAsset'];
  sendNotebookBacklinks: ReturnType<typeof useDaemonApi>['sendNotebookBacklinks'];
  sendNotebookToChief: ReturnType<typeof useDaemonApi>['sendNotebookToChief'];
  sendFsIndex: ReturnType<typeof useDaemonApi>['sendFsIndex'];
  fsChangeSignals: AppContentProps['fsChangeSignals'];
  effectiveNotebookRoot: string;
  sendFsWatch: ReturnType<typeof useDaemonApi>['sendFsWatch'];
  sendFsUnwatch: ReturnType<typeof useDaemonApi>['sendFsUnwatch'];
  connectionGeneration: ReturnType<typeof useDaemonApi>['connectionGeneration'];
}
export function useAppNotebookSurface({
  sendFsList,
  sendFsRead,
  sendFsWrite,
  sendFsExists,
  sendFsReadAsset,
  sendNotebookBacklinks,
  sendNotebookToChief,
  sendFsIndex,
  fsChangeSignals,
  effectiveNotebookRoot,
  sendFsWatch,
  sendFsUnwatch,
  connectionGeneration,
}: Options) {
  const profileId = useProfilesStore((state) => state.selectedProfileId);
  const makeNotebookSurfaceDaemon = useCallback((root?: string) => {
    const notebook = !root || root === effectiveNotebookRoot;
    const requestRoot = notebook ? undefined : root;
    const scope = { profile_id: profileId, ...(notebook && effectiveNotebookRoot ? { expected_notebook_root: effectiveNotebookRoot } : {}) };
    return {
      listDir: (path: string) => sendFsList(path, requestRoot, scope),
      readFile: (path: string) => sendFsRead(path, requestRoot, scope),
      writeFile: (path: string, content: string, baseHash?: string) => sendFsWrite(path, content, baseHash, requestRoot, scope),
      existsFile: (path: string) => sendFsExists(path, requestRoot, scope),
      readAsset: (path: string) => sendFsReadAsset(path, requestRoot, scope),
      backlinksNotebook: (path: string) => sendNotebookBacklinks(path, scope),
      sendToChief: (selection: string, sourcePath?: string) => sendNotebookToChief(selection, sourcePath, scope),
      listFiles: () => sendFsIndex(requestRoot, undefined, scope).then(fsIndexToNotebookEntries),
    };
  }, [sendFsList, sendFsRead, sendFsWrite, sendFsExists, sendFsReadAsset, sendNotebookBacklinks, sendNotebookToChief, sendFsIndex, profileId, effectiveNotebookRoot]);

  const changeSignalFor = useCallback(
    (root?: string) => fsChangeSignals[fsChangeSignalKey(root || '', effectiveNotebookRoot)] || 0,
    [fsChangeSignals, effectiveNotebookRoot],
  );

  const notebookSurfaceContextValue = useMemo(
    () => ({
      makeDaemon: makeNotebookSurfaceDaemon,
      changeSignalFor,
      effectiveNotebookRoot,
      sendFsWatch,
      sendFsUnwatch,
      connectionGeneration,
    }),
    [
      makeNotebookSurfaceDaemon,
      changeSignalFor,
      effectiveNotebookRoot,
      sendFsWatch,
      sendFsUnwatch,
      connectionGeneration,
    ],
  );

  const notebookRootChangeSignal =
    fsChangeSignals[fsChangeSignalKey('', effectiveNotebookRoot)] || 0;

  const notebookBrowserListFiles = useCallback(
    () => sendFsIndex().then(fsIndexToNotebookEntries),
    [sendFsIndex],
  );

  return { notebookBrowserListFiles, notebookRootChangeSignal, notebookSurfaceContextValue };
}
