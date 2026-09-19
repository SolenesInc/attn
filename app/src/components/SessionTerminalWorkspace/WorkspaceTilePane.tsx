import { SuspendedWorkspacePane } from './SuspendedWorkspacePane';
import type { CSSProperties } from 'react';
import type { NormalizedPaneBounds, TileLeaf } from '../../types/workspace';
import { tileContentKey } from '../../types/workspace';
import { useWorkspaceContext } from './WorkspaceContext';
import { WorkspaceDockTile } from './WorkspaceDockTile';

const noRequestContent = () => {};

export function WorkspaceTilePane({
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
    workspaceId,
    workspaceDirectory,
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
    activePaneSessionId,
    activeLeafId,
    effectivePaneId,
    suspendedLeafIds,
    tileBodyRefFor,
    focusDocument,
    focusLeaf,
    beginLeafDrag,
    effectiveDraggingLeafId,
  } = useWorkspaceContext();

  if (suspendedLeafIds.has(tileLeaf.tileId) && !effectivePaneId) {
    const suspendedTitle =
      (tileLeaf.tileParams ?? '').split('/').filter(Boolean).pop() || tileLeaf.tileKind || 'Tile';
    return (
      <SuspendedWorkspacePane
        leafId={tileLeaf.tileId}
        title={suspendedTitle}
        kind="tile"
        tileKind={tileLeaf.tileKind}
        bounds={bounds}
        path={path}
        frameStyle={frameStyle}
      >
        <span
          className={`workspace-suspended-state workspace-suspended-state--${tileLeaf.tileKind}`}
        />
      </SuspendedWorkspacePane>
    );
  }
  return (
    <div
      key={`tile:${tileLeaf.tileId}`}
      className={`workspace-pane workspace-pane--tile ${activeLeafId === tileLeaf.tileId ? 'active' : ''}`.trim()}
      role="group"
      aria-label={`Workspace ${tileLeaf.tileKind}`}
      onMouseDown={() => focusLeaf(tileLeaf.tileId)}
      data-pane-id={tileLeaf.tileId}
      data-pane-kind="tile"
      data-tile-kind={tileLeaf.tileKind}
      data-pane-path={path}
      style={frameStyle}
    >
      <WorkspaceDockTile
        tile={tileLeaf}
        workspaceId={workspaceId}
        content={tileContents?.[tileContentKey(workspaceId, tileLeaf.tileId)]}
        allowLocalTargets={allowLocalTileTargets}
        dragging={effectiveDraggingLeafId === tileLeaf.tileId}
        visible={
          isActiveSession &&
          isSessionViewVisible &&
          enabled &&
          renamePane === null &&
          effectiveDraggingLeafId === null
        }
        workspaceSessions={tileLeaf.tileKind === 'seed' ? seedTargetSessions : tileSessionOptions}
        gardenSeeds={gardenSeeds}
        workspaceSessionId={activePaneSessionId}
        workspaceDirectory={workspaceDirectory}
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
