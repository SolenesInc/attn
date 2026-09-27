import { open } from '@tauri-apps/plugin-dialog';
import type { ChangeEvent, RefObject } from 'react';
import { useCallback } from 'react';
import { useNotebookSurfaceContext } from '../../contexts/NotebookSurfaceContext';
import { parseNotebookTileParams, serializeNotebookTileParams } from '../../types/desktop';
import { tilePathBasename } from '../../utils/tilePresentation';
import type { NotebookSurfaceHandle } from '../NotebookSurface';

interface Props {
  tileParams?: string;
  desktopDirectory?: string;
  notebookSurfaceRef: RefObject<NotebookSurfaceHandle | null>;
  onUpdateParams?: (params: string) => Promise<unknown> | void;
}
export function NotebookRootPicker({
  tileParams,
  desktopDirectory,
  notebookSurfaceRef,
  onUpdateParams,
}: Props) {
  const { effectiveNotebookRoot } = useNotebookSurfaceContext();
  const currentRoot = parseNotebookTileParams(tileParams).root;
  const ROOT_BROWSE_VALUE = '__browse__';
  const desktopDirIsRoot = !!desktopDirectory && desktopDirectory !== effectiveNotebookRoot;
  const currentRootIsOther =
    !!currentRoot && currentRoot !== effectiveNotebookRoot && currentRoot !== desktopDirectory;

  const handleRootChange = useCallback(
    (event: ChangeEvent<HTMLSelectElement>) => {
      const value = event.target.value;
      if (value === ROOT_BROWSE_VALUE) {
        void open({ directory: true, multiple: false, title: 'Choose editor root' })
          .then(async (selected) => {
            if (!selected || typeof selected !== 'string') {
              return;
            }
            // Flush the outgoing root's dirty buffer BEFORE the param swap: it remounts NotebookSurface
            // onto the new root, and only this instance can still persist to the old one.
            const outcome = notebookSurfaceRef.current
              ? await notebookSurfaceRef.current.flushPendingSave()
              : 'noop';
            if (outcome === 'conflict' || outcome === 'error') {
              return;
            }
            void Promise.resolve(
              onUpdateParams?.(serializeNotebookTileParams({ root: selected })),
            ).catch((error) => {
              console.warn('[DesktopDockTile] Failed to persist browsed notebook root:', error);
            });
          })
          .catch((error) => {
            console.warn('[DesktopDockTile] Failed to open root browse dialog:', error);
          });
        return;
      }
      void (async () => {
        const outcome = notebookSurfaceRef.current
          ? await notebookSurfaceRef.current.flushPendingSave()
          : 'noop';
        if (outcome === 'conflict' || outcome === 'error') {
          return;
        }
        void Promise.resolve(
          onUpdateParams?.(serializeNotebookTileParams({ root: value || undefined })),
        ).catch((error) => {
          console.warn('[DesktopDockTile] Failed to persist notebook root:', error);
        });
      })();
    },
    [onUpdateParams, notebookSurfaceRef],
  );

  return (
    <select
      className="desktop-dock-tile-root-picker"
      aria-label="Editor root"
      value={currentRoot ?? ''}
      // The header is the drag handle; interacting with the picker must not start a re-dock drag.
      onPointerDown={(event) => event.stopPropagation()}
      onChange={handleRootChange}
    >
      <option value="">Notebook</option>
      {desktopDirIsRoot && (
        <option value={desktopDirectory}>
          Desktop — {tilePathBasename(desktopDirectory as string)}
        </option>
      )}
      {currentRootIsOther && (
        <option value={currentRoot}>{tilePathBasename(currentRoot as string)}</option>
      )}
      <option value={ROOT_BROWSE_VALUE}>Browse…</option>
    </select>
  );
}
