import type { ComponentProps, ReactNode } from 'react';
import type { SidebarDesktop } from './sidebarTypes';
import type { TileLeaf } from '../types/desktop';
import { tileContentKey } from '../types/desktop';
import './Sidebar.css';
import { useSidebarContext } from './SidebarContext';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { runCount, runsNeedingYouCount, runNeedsYou } from '../utils/automationRuns';
import { hasNoLeaves, desktopShortcut, treeSelectionKey } from './sidebarModel';
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
  const context = useSidebarContext();
  const selectionKey = treeSelectionKey(context);
  const {
    onRenameDesktop,
    onCloseDesktop,
    selectedDesktopId,
    homeActive,
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
  } = context;
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
              <div aria-current={selectionKey === `${desktopView.id}/` ? 'true' : undefined} className={`desktop-rule${!homeActive && desktopView.id === selectedDesktopId ? ' current' : ''}${hasNoLeaves(desktopView) ? ' empty' : ''}${shortcut ? '' : ' no-shortcut'}`}>
                <button
                  type="button"
                  className="sidebar-row-select"
                  aria-label={`Open ${desktopView.title}`}
                  data-select-key={`${desktopView.id}/`}
                  title={emptyTitle}
                  onPointerDown={desktop ? (event) => handleHeaderPointerDown(desktopView, event) : undefined}
                  onClickCapture={handleHeaderClickCapture}
                  onClick={() => onSelectDesktop(desktopView.id)}
                />
                {desktop ? (
                  <DesktopChip
                    number={desktop.number ?? desktopIndex + 1}
                    current={!homeActive && desktopView.id === selectedDesktopId}
                    empty={hasNoLeaves(desktopView)}
                    title={emptyTitle}
                  />
                ) : null}
                {(desktop?.name || !desktop) && <span className="desktop-label">{desktopView.title}</span>}
                <span className="desktop-rule-line" aria-hidden="true" />
                {shortcut && (
                  <span className="session-shortcut">{shortcut}</span>
                )}
                {(onRenameDesktop || onCloseDesktop) && desktop && (
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
                    {onCloseDesktop && <button
                      type="button"
                      className="desktop-action-btn close-desktop-btn"
                      data-testid={`close-desktop-${desktopView.id}`}
                      onClick={(event) => { event.stopPropagation(); onCloseDesktop(desktopView.id); }}
                      title="Close desktop"
                      aria-label={`Close ${desktopView.title}`}
                    >×</button>}
                  </span>
                )}
              </div>
              {desktopView.children.map((child) => {
                if (child.kind === 'tile') {
                  return (
                    <DesktopTileRow key={child.id} desktopId={desktopView.id} tile={child.tile}
                      aria-current={selectionKey === `${desktopView.id}/tile:${child.tile.tileId}` ? 'true' : undefined} />
                  );
                }
                const session = child.session;
                const paneId = child.paneId;
                const draggable = Boolean(paneId && onSessionDragStart);
                return (
                  <DesktopSessionRow
                    key={session.id}
                    session={session}
                    data-select-key={`${desktopView.id}/session:${session.id}`}
                    aria-current={selectionKey === `${desktopView.id}/session:${session.id}` ? 'true' : undefined}
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

export function SidebarAutomationGroups() {
  const { expandedAutomationGroups, automationGroups, toggleAutomationGroup, onWalkRuns } =
    useSidebarContext();
  if (automationGroups.length === 0) return null;
  const runs = runCount(automationGroups);
  const needingYou = runsNeedingYouCount(automationGroups);
  return (
    <section
      className="automation-runs"
      data-testid="sidebar-automation-runs"
      data-runs={runs}
      data-needing={needingYou}
      title="Automation runs never join the queue"
    >
      <div className="automation-runs-header">
        Automations
        <span className="automation-runs-count">{plural(runs, 'run')}</span>
      </div>
      {needingYou > 0 && (
        <button
          type="button"
          className="automation-runs-batch"
          data-testid="sidebar-runs-needing-you"
          title="Open the next run needing you; press again for the one after"
          onClick={onWalkRuns}
        >
          {plural(needingYou, 'run')} {needingYou === 1 ? 'needs' : 'need'} you
          <kbd>{formatShortcut('session.nextRun')}</kbd>
        </button>
      )}
      {automationGroups.map((group) => {
        const expanded = expandedAutomationGroups.has(group.id);
        const asking = group.needingYou.length;
        return (
          <div
            className="automation-session-group"
            data-testid={`sidebar-automation-${group.id}`}
            data-automation-id={group.id}
            data-runs={group.runs.length}
            data-needing={asking}
            key={group.id}
          >
            <button
              type="button"
              className="automation-session-header"
              data-testid={`sidebar-automation-header-${group.id}`}
              aria-expanded={expanded}
              title={[
                group.name,
                plural(group.runs.length, 'run'),
                ...(asking ? [`${asking} stopped with a question`] : []),
                'runs stay out of the queue',
              ].join(' · ')}
              onClick={() => toggleAutomationGroup(group.id)}
            >
              <span className={`automation-session-chevron ${expanded ? 'expanded' : ''}`}>▸</span>
              <span className="automation-session-name">{group.name}</span>
              {asking > 0 && <span className="automation-session-asking">{asking}</span>}
              <span className="automation-session-count">{group.runs.length}</span>
            </button>
            {expanded && (
              <div className="automation-session-list">
                {group.runs.map((run) => (
                  <DesktopSessionRow key={run.id} session={run} grouped />
                ))}
              </div>
            )}
          </div>
        );
      })}
    </section>
  );
}

function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? '' : 's'}`;
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
    homeActive,
    desktopDragClass,
    canAcceptLeafDrag,
    onDesktopDragEnter,
    onDesktopDragLeave,
    onDesktopDragDrop,
  } = useSidebarContext();
  return (
    <div
      className={`desktop-group ${!homeActive && selectedDesktopId === desktopView.id ? 'selected' : ''}${reorderSource ? ' desktop-group--reorder-source' : ''}${desktopDragClass(desktopView)}`}
      data-testid={`sidebar-desktop-${desktopView.id}`}
      onPointerEnter={() => {
        if (canAcceptLeafDrag(desktopView)) onDesktopDragEnter?.(desktopView);
      }}
      onPointerLeave={() => {
        if (canAcceptLeafDrag(desktopView)) onDesktopDragLeave?.(desktopView);
      }}
      onPointerUp={(event) => {
        if (canAcceptLeafDrag(desktopView)) onDesktopDragDrop?.(desktopView, event.altKey);
      }}
    >
      {children}
    </div>
  );
}

function DesktopTileRow({
  desktopId,
  tile,
  ...revealProps
}: {
  desktopId: string;
  tile: TileLeaf;
  'aria-current'?: 'true';
}) {
  const { tileContents, selectedTile, onSelectTile, onCloseTile, onReloadTile } =
    useSidebarContext();
  return (
    <TileSidebarRow
      {...revealProps}
      data-select-key={`${desktopId}/tile:${tile.tileId}`}
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
