import type { CSSProperties } from 'react';
import type { NormalizedPaneBounds, TileLeaf } from '../../types/desktop';
import { tileContentKey } from '../../types/desktop';
import { useDesktopContext } from './DesktopContext';
import { DesktopDockTile } from './DesktopDockTile';

const noRequestContent = () => {};

const OFF_SCREEN_STYLE: CSSProperties = { position: 'absolute', inset: 0, visibility: 'hidden' };

export function DesktopTilePane({
  tileLeaf,
  placement,
}: {
  tileLeaf: TileLeaf;
  placement?: { bounds: NormalizedPaneBounds; path: string; frameStyle: CSSProperties; popped?: boolean };
}) {
  const {
    desktopId,
    desktopDirectory,
    seedTargetSessions,
    gardenSeeds,
    onRevealSeedInGarden,
    backToCrewTileId,
    onBackToCrew,
    enabled,
    isActiveSession,
    isSessionViewVisible,
    onUndockTile,
    onUpdateTile,
    tileContents,
    onRequestTileContent,
    renamePane,
    tileSessionOptions,
    activeLeafId,
    tileBodyRefFor,
    focusDocument,
    focusLeaf,
    beginLeafDrag,
    effectiveDraggingLeafId,
    paneGeometry,
    effectivePaneId,
    setMaximizedLeafId,
    tileWindowsEnabled,
    toggleTileWindow,
  } = useDesktopContext();

  const paneAttributes = placement
    ? {
        className: `desktop-pane desktop-pane--tile${placement.bounds.left === 0 ? ' desktop-pane--flush-left' : ''}${activeLeafId === tileLeaf.tileId ? ' active' : ''}`,
        onMouseDown: () => focusLeaf(tileLeaf.tileId),
        'data-pane-id': tileLeaf.tileId,
        'data-pane-kind': 'tile',
        'data-tile-kind': tileLeaf.tileKind,
        'data-pane-path': placement.path,
        style: placement.frameStyle,
      }
    : { className: 'desktop-tile-off-screen', inert: true, style: OFF_SCREEN_STYLE };
  return (
    <div
      key={`tile:${tileLeaf.tileId}`}
      role="group"
      aria-label={`Desktop ${tileLeaf.tileKind}`}
      {...paneAttributes}
    >
      <DesktopDockTile
        tile={tileLeaf}
        desktopId={desktopId}
        content={tileContents?.[tileContentKey(desktopId, tileLeaf.tileId)]}
        dragging={effectiveDraggingLeafId === tileLeaf.tileId}
        paneGeometry={paneGeometry.get(tileLeaf.tileId)}
        visible={
          placement !== undefined &&
          (placement.popped ||
            (isActiveSession &&
              isSessionViewVisible &&
              enabled &&
              renamePane === null &&
              effectiveDraggingLeafId === null))
        }
        desktopSessions={tileLeaf.tileKind === 'seed' ? seedTargetSessions : tileSessionOptions}
        gardenSeeds={gardenSeeds}
        desktopDirectory={desktopDirectory}
        onClose={() => onUndockTile?.(tileLeaf.tileId)}
        onPopOut={tileWindowsEnabled ? () => toggleTileWindow(tileLeaf.tileId) : undefined}
        popped={placement?.popped ?? false}
        onReturnToRow={effectivePaneId === tileLeaf.tileId ? () => setMaximizedLeafId(null) : undefined}
        onFocusDocument={
          tileLeaf.tileKind === 'markdown' || tileLeaf.tileKind === 'seed'
            ? () => focusDocument(tileLeaf.tileId)
            : undefined
        }
        onUpdateParams={(tileParams) => onUpdateTile?.(tileLeaf.tileId, tileParams)}
        onRetargetTile={(sessionId) =>
          onUpdateTile?.(tileLeaf.tileId, tileLeaf.tileParams ?? '', sessionId)
        }
        onRevealSeedInGarden={onRevealSeedInGarden}
        onBackToCrew={tileLeaf.tileId === backToCrewTileId ? onBackToCrew : undefined}
        onHeaderPointerDown={(event) => { if (!placement?.popped) beginLeafDrag(tileLeaf.tileId, event); }}
        onRequestContent={onRequestTileContent ?? noRequestContent}
        bodyRef={tileBodyRefFor(tileLeaf.tileId)}
      />
    </div>
  );
}
