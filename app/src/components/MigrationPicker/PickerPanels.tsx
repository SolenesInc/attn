import { keyCombo } from '../../shortcuts/formatShortcut';
import { slotShortcut } from '../../utils/desktops';
import { DraftPreview } from './DraftPreview';
import { dropOverlayStyle } from './dropTarget';
import { plural, summarize, type DraftDesktopView, type DraftView, type GroupView } from './migrationDraft';
import type { GroupDragView } from './useGroupDrag';

export interface BoardState {
  view: DraftView;
  selected: string | null;
  pulseId: string | null;
  draggingId: string | null;
  dropTargetKey: string | null;
  busy: boolean;
}

function groupTitles(view: DraftView, ids: string[]): string[] {
  return ids.map((id) => view.groupById.get(id)?.group.title ?? id);
}

function unconfirmedOn(view: DraftView, desktop: DraftDesktopView): string[] {
  return desktop.groupIds.filter((id) => !view.groupById.get(id)?.group.confirmed);
}

interface DesktopFootProps {
  desktop: DraftDesktopView;
  board: BoardState;
  onKeep: (desktop: DraftDesktopView, groupIds: string[]) => void;
}

function DesktopFoot({ desktop, board, onKeep }: DesktopFootProps) {
  const todo = unconfirmedOn(board.view, desktop);
  const slot = desktop.desktop.shortcut_slot;
  if (todo.length === 0) {
    return desktop.groupIds.length > 0 ? <span className="mp-badge ok">{slot ? 'Confirmed' : 'Kept as extra'}</span> : null;
  }
  const label = slot
    ? `Keep ${plural(todo.length, 'group')} on ${desktop.label}`
    : `Keep ${groupTitles(board.view, todo).join(', ')} as an extra desktop`;
  return (
    <button
      type="button"
      data-no-drag
      disabled={board.busy}
      className={`mp-button small${todo.includes(board.pulseId ?? '') ? ' pulse' : ''}`}
      aria-label={label}
      onClick={(event) => {
        event.stopPropagation();
        onKeep(desktop, todo);
      }}
    >
      {slot ? 'Keep here ✓' : 'Keep as extra desktop ✓'}
    </button>
  );
}

function desktopClasses(desktop: DraftDesktopView, board: BoardState): string {
  const todo = unconfirmedOn(board.view, desktop);
  return [
    'mp-desktop',
    desktop.tree ? '' : 'empty',
    desktop.desktop.shortcut_slot ? '' : 'extra',
    desktop.groupIds.length > 0 && todo.length === 0 ? 'all-confirmed' : '',
    board.selected !== null && desktop.groupIds.includes(board.selected) ? 'targeted' : '',
    board.dropTargetKey === desktop.desktop.key ? 'dragover' : '',
  ].filter(Boolean).join(' ');
}

interface DesktopCardProps {
  desktop: DraftDesktopView;
  board: BoardState;
  onActivate: (desktop: DraftDesktopView) => void;
  onKeep: (desktop: DraftDesktopView, groupIds: string[]) => void;
}

function DesktopCard({ desktop, board, onActivate, onKeep }: DesktopCardProps) {
  const titles = groupTitles(board.view, desktop.groupIds);
  const leaves = desktop.groupIds.reduce((sum, id) => sum + (board.view.groupById.get(id)?.leaves.length ?? 0), 0);
  const slot = desktop.desktop.shortcut_slot;
  return (
    <div
      role="button"
      tabIndex={0}
      className={desktopClasses(desktop, board)}
      data-migration-desktop={desktop.desktop.key}
      aria-label={`${desktop.label}${desktop.tree ? `, ${titles.join(', ')}` : ', empty'}`}
      onClick={() => onActivate(desktop)}
      onKeyDown={(event) => {
        if ((event.key === 'Enter' || event.key === ' ') && event.target === event.currentTarget) {
          event.preventDefault();
          onActivate(desktop);
        }
      }}
    >
      <div className="mp-deskhead">
        <span className="mp-desknumber">
          {slot ? <kbd>{slot}</kbd> : <span className="mp-extra-mark" aria-hidden="true">◌</span>}
          {desktop.label}
        </span>
        <span className="mp-deskmeta">{leaves ? plural(leaves, 'tile') : ''}</span>
      </div>
      <div className="mp-desk-preview">
        <DraftPreview
          tree={desktop.tree}
          groupById={board.view.groupById}
          selectedId={board.selected}
          draggingId={board.draggingId}
          draggable
        />
      </div>
      <div className="mp-deskfoot">
        <span className="mp-names">{desktop.tree ? titles.join(' · ') : 'Stays empty'}</span>
        <DesktopFoot desktop={desktop} board={board} onKeep={onKeep} />
      </div>
    </div>
  );
}

interface SourceRowProps {
  row: GroupView;
  board: BoardState;
  onSelect: () => void;
  onKeep: () => void;
}

function SourceRow({ row, board, onSelect, onKeep }: SourceRowProps) {
  const { group } = row;
  const location = board.view.desktopOfGroup.get(group.group_id);
  const selected = group.group_id === board.selected;
  const dot = row.failed ? 'failed' : row.launching ? 'pending' : '';
  const classes = ['mp-source', selected ? 'selected' : '', group.confirmed ? 'confirmed' : '', board.draggingId === group.group_id ? 'drag-source' : '']
    .filter(Boolean)
    .join(' ');
  return (
    <div className={classes} data-drag-group={group.group_id}>
      <span className="mp-grip" aria-hidden="true">⠿</span>
      <button
        type="button"
        className="mp-source-main"
        role="option"
        aria-selected={selected}
        data-select-group={group.group_id}
        onClick={onSelect}
      >
        <div className="mp-source-title">
          <span className={`mp-dot ${dot}`} />
          <span className="mp-source-name">{group.title}</span>
        </div>
        <div className="mp-source-meta">{summarize(row)}</div>
        {group.directory && <div className="mp-source-meta mp-source-directory" title={group.directory}>{group.directory}</div>}
        <div className="mp-source-badges">
          <span className="mp-badge where">{location?.label ?? 'Unplaced'}</span>
          {group.confirmed ? <span className="mp-badge ok">✓ Confirmed</span> : <span className="mp-badge todo">Confirm</span>}
          {row.launching && <span className="mp-badge state">Launching</span>}
          {row.failed && <span className="mp-badge state">Failed</span>}
        </div>
      </button>
      {!group.confirmed && (
        <button
          type="button"
          data-no-drag
          disabled={board.busy}
          className={`mp-keep-inline${group.group_id === board.pulseId ? ' pulse' : ''}`}
          aria-label={`Keep ${group.title} on ${location?.label ?? 'its desktop'}`}
          title="Keep here"
          onClick={onKeep}
        >
          ✓
        </button>
      )}
    </div>
  );
}

interface SourcePanelProps {
  board: BoardState;
  onSelect: (groupId: string) => void;
  onKeep: (row: GroupView) => void;
}

export function SourcePanel({ board, onSelect, onKeep }: SourcePanelProps) {
  const left = board.view.unconfirmed.length;
  return (
    <aside className="mp-source-panel" aria-label="Imported workspaces">
      <div className="mp-panel-title">
        <h2>Your workspaces</h2>
        <span className="mp-counter">{left ? `${left} to confirm` : 'All confirmed'}</span>
      </div>
      <div className="mp-source-hint">Drag onto a desktop to merge. <kbd>K</kbd> keeps it where it is.</div>
      <div className="mp-source-list" role="listbox" aria-label="Workspaces">
        {board.view.groups.length === 0 && <div className="mp-empty-list">No workspaces left to confirm.</div>}
        {board.view.groups.map((row) => (
          <SourceRow
            key={row.group.group_id}
            row={row}
            board={board}
            onSelect={() => onSelect(row.group.group_id)}
            onKeep={() => onKeep(row)}
          />
        ))}
      </div>
    </aside>
  );
}

interface DestinationPanelProps {
  board: BoardState;
  profileName: string;
  suggestionAvailable: boolean;
  onSuggest: () => void;
  onActivate: (desktop: DraftDesktopView) => void;
  onKeep: (desktop: DraftDesktopView, groupIds: string[]) => void;
}

export function DestinationPanel({ board, profileName, suggestionAvailable, onSuggest, onActivate, onKeep }: DestinationPanelProps) {
  const { view } = board;
  const total = view.slots.length + view.extras.length;
  const cards = (desktops: DraftDesktopView[]) => desktops.map((desktop) => (
    <DesktopCard key={desktop.desktop.key} desktop={desktop} board={board} onActivate={onActivate} onKeep={onKeep} />
  ));
  return (
    <section className="mp-dest-panel" aria-label="Desktops">
      <div className="mp-dest-heading">
        <div>
          <h2>{profileName} · {plural(total, 'desktop')}</h2>
          <p>
            {view.unconfirmed.length
              ? <>Drag a workspace onto another desktop to merge it, or select it and press <kbd>1</kbd>–<kbd>9</kbd>. <kbd>K</kbd> keeps it where it is.</>
              : 'Everything is confirmed. Finish when you’re ready.'}
          </p>
        </div>
        <div className="mp-suggest">
          <button type="button" className="mp-button quiet" disabled={board.busy || !suggestionAvailable} onClick={onSuggest}>
            ✧ Suggest merging the extras
          </button>
          <span>Moves unconfirmed extras into the emptiest slots and confirms them.</span>
        </div>
      </div>
      <div className="mp-section-title"><h3>Shortcut slots</h3><span>{slotShortcut(1)}–{slotShortcut(9)}</span></div>
      <div className="mp-desktop-grid">{cards(view.slots)}</div>
      {view.extras.length > 0 && (
        <>
          <div className="mp-section-title"><h3>Extra desktops</h3><span>reached from the overview · {view.extras.length}</span></div>
          <div className="mp-desktop-grid extras">{cards(view.extras)}</div>
        </>
      )}
      <div className="mp-dest-tip">
        <strong>Drop at an edge to choose a split.</strong> Drag groups between desktops. Splits inside each group stay together.
      </div>
    </section>
  );
}

interface BottomBarProps {
  board: BoardState;
  canUndo: boolean;
  status: string;
  onUndo: () => void;
  onKeepRemaining: () => void;
  onFinish: () => void;
}

export function BottomBar({ board, canUndo, status, onUndo, onKeepRemaining, onFinish }: BottomBarProps) {
  const left = board.view.unconfirmed.length;
  const total = board.view.groups.length;
  return (
    <>
      <div className="mp-bottom">
        <div className="mp-bottom-left">
          <button type="button" className="mp-button quiet" disabled={board.busy || !canUndo} onClick={onUndo}>↶ Undo</button>
          <span className="mp-status">
            <strong>{total - left} of {total}</strong> confirmed{left ? '' : '. Ready to finish.'}
          </span>
          <span className="mp-status-message" role="status">{status}</span>
        </div>
        <div className="mp-bottom-right">
          {left > 0 && (
            <button type="button" className="mp-button quiet" disabled={board.busy} onClick={onKeepRemaining}>
              Keep the remaining {left} where they are
            </button>
          )}
          <button type="button" className="mp-button primary" disabled={board.busy || left > 0} onClick={onFinish}>
            Finish →
          </button>
        </div>
      </div>
      <div className="mp-keyboard-help">
        <span>
          <kbd>↑</kbd> <kbd>↓</kbd> choose · <kbd>K</kbd> keep here · <kbd>1</kbd>–<kbd>9</kbd> move to slot · <kbd>M</kbd> move to any desktop · <kbd>{keyCombo('accel', 'z')}</kbd> undo
        </span>
        <span>Every attn window sees this draft.</span>
      </div>
    </>
  );
}

export function DragOverlay({ drag, view }: { drag: GroupDragView | null; view: DraftView }) {
  if (!drag) return null;
  const target = drag.target;
  return (
    <>
      <div className="mp-drag-label" style={{ left: drag.x + 12, top: drag.y + 14 }}>
        {view.groupById.get(drag.groupId)?.group.title}
      </div>
      {target && (
        <>
          <div className="mp-drop-preview" style={dropOverlayStyle(target)} />
          <div className="mp-drop-label" style={{ left: Math.max(target.box.left, 8), top: Math.max(8, target.box.top - 30) }}>
            {target.label}
          </div>
        </>
      )}
    </>
  );
}
