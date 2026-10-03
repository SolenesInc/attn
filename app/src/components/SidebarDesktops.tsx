import type { ComponentProps, ReactNode } from 'react';
import type { SidebarDesktop } from './sidebarTypes';
import type { TileLeaf } from '../types/desktop';
import { tileContentKey } from '../types/desktop';
import './Sidebar.css';
import { useSidebarContext } from './SidebarContext';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { runNeedsYou } from '../utils/automationRuns';
import { hasNoLeaves, desktopShortcut } from './sidebarModel';
import { SidebarSessionRow, TileSidebarRow } from './SidebarRows';

export function DesktopChip({ number, current, empty, title, children }: {
  number?: number;
  current: boolean;
  empty: boolean;
  title?: string;
  children?: ReactNode;
}) {
  return (
    <span className={`desktop-number${current ? ' current' : ''}${empty ? ' empty' : ''}`} title={title}>
      {number ?? '—'}
      {children}
    </span>
  );
}

export function SidebarDesktopOverview({ compact = false }: { compact?: boolean }) {
  const { onOpenOverview } = useSidebarContext();
  return (
    <button
      type="button"
      className={compact ? 'queue-desktop-chip is-extra' : 'sidebar-home-row'}
      data-testid={compact ? 'queue-desktop-overview' : 'sidebar-desktop-overview'}
      title={`Desktop overview (${formatShortcut('desktop.overview')})`}
      aria-label="Desktop overview"
      onClick={onOpenOverview}
    >
      <span aria-hidden="true">⊞</span>
      {!compact && (
        <>
          <span className="sidebar-home-label">Desktop overview</span>
          <span className="sidebar-home-shortcut">{formatShortcut('desktop.overview')}</span>
        </>
      )}
    </button>
  );
}

export function SidebarDesktopList() {
  const {
    onRenameDesktop,
    selectedDesktopId,
    onSessionDragStart,
    onSelectDesktop,
    openDesktopRename,
    visibleDesktops,
    visualIndexOfDesktop,
    reorderDrag,
    draggingSessionId,
    reorderSeamIndexByDesktopId,
    reorderTrailingSeamIndex,
    lastReorderParticipantId,
    renderReorderSeam,
    handleHeaderPointerDown,
    handleHeaderClickCapture,
    handleSessionPointerDown,
    handleSessionClickCapture,
  } = useSidebarContext();
  return (
    <>
      {visibleDesktops.map((desktopView) => {
        const desktopIndex = visualIndexOfDesktop(desktopView.id);
        const shortcut = desktopShortcut(desktopIndex);
        const seamIndex = reorderSeamIndexByDesktopId?.get(desktopView.id);
        const isReorderSource = reorderDrag?.desktopId === desktopView.id;
        const desktop = desktopView.desktop;
        const emptyTitle = hasNoLeaves(desktopView)
          ? desktopView.hasUnresolvedAgentPanes
            ? 'Desktop has a pane without an active session'
            : 'Empty desktop'
          : undefined;
        return (
          <div className="desktop-row" key={desktopView.id}>
            {seamIndex !== undefined && renderReorderSeam(seamIndex)}
            <DesktopDropGroup desktopView={desktopView} reorderSource={isReorderSource}>
              <div className={`desktop-rule${desktopView.id === selectedDesktopId ? ' current' : ''}${hasNoLeaves(desktopView) ? ' empty' : ''}${shortcut ? '' : ' no-shortcut'}`}>
                <button
                  type="button"
                  className="sidebar-row-select"
                  aria-label={`Open ${desktopView.title}`}
                  title={emptyTitle}
                  onPointerDown={desktop ? (event) => handleHeaderPointerDown(desktopView, event) : undefined}
                  onClickCapture={handleHeaderClickCapture}
                  onClick={() => onSelectDesktop(desktopView.id)}
                />
                {desktop ? (
                  <DesktopChip
                    number={desktop.number ?? desktopIndex + 1}
                    current={desktopView.id === selectedDesktopId}
                    empty={hasNoLeaves(desktopView)}
                    title={emptyTitle}
                  />
                ) : null}
                {(desktop?.name || !desktop) && <span className="desktop-label">{desktopView.title}</span>}
                <span className="desktop-rule-line" aria-hidden="true" />
                {shortcut && (
                  <span className="session-shortcut">{shortcut}</span>
                )}
                {onRenameDesktop && desktop && (
                  <span className="desktop-actions">
                    <button
                      type="button"
                      className="desktop-action-btn rename-desktop-btn"
                      data-testid={`rename-desktop-${desktopView.id}`}
                      onClick={(e) => openDesktopRename(desktopView.id, desktop, e)}
                      title="Rename desktop"
                      aria-label={`Rename ${desktopView.title}`}
                    >
                      ✎
                    </button>
                  </span>
                )}
              </div>
              {desktopView.children.map((child) => {
                if (child.kind === 'tile') {
                  return (
                    <DesktopTileRow key={child.id} desktopId={desktopView.id} tile={child.tile} />
                  );
                }
                const session = child.session;
                const paneId = child.paneId;
                const draggable = Boolean(paneId && onSessionDragStart);
                return (
                  <DesktopSessionRow
                    key={session.id}
                    session={session}
                    draggable={draggable}
                    dragging={draggingSessionId === session.id}
                    onClickCapture={draggable ? handleSessionClickCapture : undefined}
                    onPointerDown={
                      draggable && paneId
                        ? (event) =>
                            handleSessionPointerDown(
                              desktopView,
                              paneId,
                              session.id,
                              session.label,
                              event,
                            )
                        : undefined
                    }
                  />
                );
              })}
            </DesktopDropGroup>
            {desktopView.id === lastReorderParticipantId &&
              renderReorderSeam(reorderTrailingSeamIndex)}
          </div>
        );
      })}
    </>
  );
}

function DesktopDropGroup({
  desktopView,
  reorderSource = false,
  children,
}: {
  desktopView: SidebarDesktop;
  reorderSource?: boolean;
  children: ReactNode;
}) {
  const {
    selectedDesktopId,
    desktopDragClass,
    canAcceptLeafDrag,
    onDesktopDragEnter,
    onDesktopDragLeave,
    onDesktopDragDrop,
  } = useSidebarContext();
  return (
    <div
      className={`desktop-group ${selectedDesktopId === desktopView.id ? 'selected' : ''}${reorderSource ? ' desktop-group--reorder-source' : ''}${desktopDragClass(desktopView)}`}
      data-testid={`sidebar-desktop-${desktopView.id}`}
      onPointerEnter={() => {
        if (canAcceptLeafDrag(desktopView)) onDesktopDragEnter?.(desktopView);
      }}
      onPointerLeave={() => {
        if (canAcceptLeafDrag(desktopView)) onDesktopDragLeave?.(desktopView);
      }}
      onPointerUp={() => {
        if (canAcceptLeafDrag(desktopView)) onDesktopDragDrop?.(desktopView);
      }}
    >
      {children}
    </div>
  );
}

function DesktopTileRow({
  desktopId,
  tile,
}: {
  desktopId: string;
  tile: TileLeaf;
}) {
  const { tileContents, selectedTile, onSelectTile, onCloseTile, onReloadTile } =
    useSidebarContext();
  return (
    <TileSidebarRow
      desktopId={desktopId}
      tile={tile}
      content={tileContents[tileContentKey(desktopId, tile.tileId)]}
      selected={selectedTile?.desktopId === desktopId && selectedTile.tileId === tile.tileId}
      onSelect={() => onSelectTile?.(desktopId, tile.tileId)}
      onClose={() => onCloseTile?.(desktopId, tile.tileId)}
      onReload={() => onReloadTile?.(desktopId, tile.tileId)}
    />
  );
}

function DesktopSessionRow(
  props: Omit<
    ComponentProps<typeof SidebarSessionRow>,
    'selected' | 'onSelect' | 'onOpenActions' | 'showSettling' | 'delegates'
  >,
) {
  const {
    selectedId,
    onSelectSession,
    openSessionActions,
    onScreenSessionIds,
    rowDelegation,
    onSettleTurn,
  } = useSidebarContext();
  const { session } = props;
  return (
    <SidebarSessionRow
      {...props}
      onSettle={onSettleTurn && session.automation && runNeedsYou(session, Date.now())
        ? () => onSettleTurn(session.id) : undefined}
      selected={selectedId === session.id}
      onSelect={() => onSelectSession(session.id)}
      onOpenActions={(event) => openSessionActions(session, event)}
      showSettling={!onScreenSessionIds?.has(session.id)}
      {...rowDelegation(session)}
    />
  );
}
