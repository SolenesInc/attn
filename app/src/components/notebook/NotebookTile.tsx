import { forwardRef, useEffect, useMemo, useRef, useState } from 'react';
import { useNotebookSurfaceContext } from '../../contexts/NotebookSurfaceContext';
import { NotebookSurface, type NotebookSurfaceHandle } from '../NotebookSurface';

// The daemon watches the notebook root unconditionally and nothing else, so a
// `root`-bound tile owns its own fs_watch subscription.
export const NotebookTile = forwardRef<NotebookSurfaceHandle, {
  initialPath: string | null;
  root?: string;
  onOpenFile: (path: string) => void;
}>(function NotebookTile({
  initialPath,
  root,
  onOpenFile,
}, ref) {
  const { makeDaemon, effectiveNotebookRoot, sendFsWatch, sendFsUnwatch, connectionGeneration } = useNotebookSurfaceContext();

  const offRoot = !!root && root !== effectiveNotebookRoot;

  // fs_changed carries the resolved root (/tmp -> /private/tmp); using the raw one
  // leaves the live refresh silently dead.
  const [resolvedRoot, setResolvedRoot] = useState<string | null>(null);
  const effectiveRoot = root ? (resolvedRoot ?? root) : undefined;
  const daemon = useMemo(() => makeDaemon(effectiveRoot), [makeDaemon, effectiveRoot]);

  // Cleanup must unwatch the resolution the daemon echoed back, not the raw prop.
  const watchedRootRef = useRef<string | null>(null);

  useEffect(() => {
    setResolvedRoot(null);
    if (!root || root === effectiveNotebookRoot) {
      return;
    }
    // connectionGeneration is a dep, unread, purely to re-run on every fresh
    // connect: the daemon drops the fs_watch ref on disconnect.
    let cancelled = false;
    sendFsWatch(root)
      .then((result) => {
        if (cancelled) {
          sendFsUnwatch(result.root).catch((error) => {
            console.warn('[NotebookTile] fs_unwatch failed for root', result.root, error);
          });
          return;
        }
        watchedRootRef.current = result.root;
        setResolvedRoot(result.root);
      })
      .catch((error) => {
        console.warn('[NotebookTile] fs_watch failed for root', root, error);
      });
    return () => {
      cancelled = true;
      const watchedRoot = watchedRootRef.current;
      watchedRootRef.current = null;
      if (!watchedRoot) {
        return;
      }
      sendFsUnwatch(watchedRoot).catch((error) => {
        console.warn('[NotebookTile] fs_unwatch failed for root', watchedRoot, error);
      });
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- sendFsWatch/sendFsUnwatch are stable daemon callbacks
  }, [root, effectiveNotebookRoot, connectionGeneration]);

  return (
    <NotebookSurface
      // Remount on root change or the old draft autosaves under the NEW root. Keys on
      // the raw prop: a normalization must not remount.
      key={root ?? ''}
      ref={ref}
      variant="tile"
      active
      initialPath={initialPath}
      onOpenFile={onOpenFile}
      listDir={daemon.listDir}
      readFile={daemon.readFile}
      writeFile={daemon.writeFile}
      existsFile={daemon.existsFile}
      readAsset={daemon.readAsset}
      backlinksNotebook={offRoot ? undefined : daemon.backlinksNotebook}
      sendToChief={offRoot ? undefined : daemon.sendToChief}
      changeSignal={daemon.changeSignal}
      listFiles={daemon.listFiles}
    />
  );
});
