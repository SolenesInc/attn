import { SuspendedDesktopPane } from './SuspendedDesktopPane';
import type { CSSProperties } from 'react';
import type { NormalizedPaneBounds, TileLeaf } from '../../types/desktop';
import { tileContentKey } from '../../types/desktop';
import { useDesktopContext } from './DesktopContext';
import { DesktopDockTile } from './DesktopDockTile';

const noRequestContent = () => {};

function suspendedTileTitle(tileLeaf: TileLeaf): string {
  return (tileLeaf.tileParams ?? '').split('/').filter(Boolean).pop() || tileLeaf.tileKind || 'Tile';
}

export function DesktopTilePane({
  tileLeaf,
  bounds,
  path,
  frameStyle,
}: {
  tileLeaf: TileLeaf;
  bounds: NormalizedPaneBounds;
  path: string;
  frameStyle: CSSProperties;
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
    allowLocalTileTargets,
    onRequestTileContent,
    renamePane,
    tileSessionOptions,
    activeLeafId,
    effectivePaneId,
    suspendedLeafIds,
    tileBodyRefFor,
    focusDocument,
    focusLeaf,
    beginLeafDrag,
    effectiveDraggingLeafId,
  } = useDesktopContext();

  if (suspendedLeafIds.has(tileLeaf.tileId) && !effectivePaneId) {
    return (
      <SuspendedDesktopPane
        leafId={tileLeaf.tileId}
        title={suspendedTileTitle(tileLeaf)}
        kind="tile"
        tileKind={tileLeaf.tileKind}
        bounds={bounds}
        path={path}
        frameStyle={frameStyle}
      >
        <span
          className={`desktop-suspended-state desktop-suspended-state--${tileLeaf.tileKind}`}
        />
      </SuspendedDesktopPane>
    );
  }
  return (
    <div
      key={`tile:${tileLeaf.tileId}`}
      className={`desktop-pane desktop-pane--tile ${activeLeafId === tileLeaf.tileId ? 'active' : ''}`.trim()}
      role="group"
      aria-label={`Desktop ${tileLeaf.tileKind}`}
      onMouseDown={() => focusLeaf(tileLeaf.tileId)}
      data-pane-id={tileLeaf.tileId}
      data-pane-kind="tile"
      data-tile-kind={tileLeaf.tileKind}
      data-pane-path={path}
      style={frameStyle}
    >
      <DesktopDockTile
        tile={tileLeaf}
        desktopId={desktopId}
        content={tileContents?.[tileContentKey(desktopId, tileLeaf.tileId)]}
        allowLocalTargets={allowLocalTileTargets}
        dragging={effectiveDraggingLeafId === tileLeaf.tileId}
        visible={
          isActiveSession &&
          isSessionViewVisible &&
          enabled &&
          renamePane === null &&
          effectiveDraggingLeafId === null
        }
        desktopSessions={tileLeaf.tileKind === 'seed' ? seedTargetSessions : tileSessionOptions}
        gardenSeeds={gardenSeeds}
        desktopDirectory={desktopDirectory}
        onClose={() => onUndockTile?.(tileLeaf.tileId)}
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
        onHeaderPointerDown={(event) => beginLeafDrag(tileLeaf.tileId, event)}
        onRequestContent={onRequestTileContent ?? noRequestContent}
        bodyRef={tileBodyRefFor(tileLeaf.tileId)}
      />
    </div>
  );
}
