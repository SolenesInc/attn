import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { Desktop } from '../types/generated';
import { collectLayoutLeaves, getNormalizedPaneBounds, leafSlotId, parseLayoutJSON } from '../types/workspace';
import { desktopLabel, extraDesktops, isEmptyDesktop, slotShortcut, slottedDesktops } from '../utils/desktops';
import './DesktopOverview.css';

const GRID_COLUMNS = 3;

interface DesktopOverviewProps {
  setupName: string;
  desktops: Desktop[];
  currentDesktopId: string | null;
  canSendActivePane: boolean;
  onSwitch: (desktopId: string) => void;
  onSendActivePane: (desktopId: string) => void;
  onDelete: (desktopId: string) => void;
  onGiveShortcutSlot: (desktopId: string) => void;
  onCreate: () => void;
  onClose: () => void;
}

interface MiniLeaf {
  id: string;
  label: string;
  left: number;
  top: number;
  width: number;
  height: number;
}

function miniLayout(desktop: Desktop): MiniLeaf[] {
  const tree = parseLayoutJSON(desktop.tree_json);
  if (!tree) return [];
  const bounds = getNormalizedPaneBounds(tree);
  const titleByPaneId = new Map(desktop.panes.map((pane) => [pane.pane_id, pane.title]));
  return collectLayoutLeaves(tree).flatMap((leaf) => {
    const id = leafSlotId(leaf);
    const rect = bounds.get(id);
    if (!rect) return [];
    const label = leaf.type === 'pane' ? titleByPaneId.get(leaf.paneId) || 'Agent' : leaf.tileKind;
    return [{ id, label, left: rect.left, top: rect.top, width: rect.width, height: rect.height }];
  });
}

export function DesktopOverview({
  setupName,
  desktops,
  currentDesktopId,
  canSendActivePane,
  onSwitch,
  onSendActivePane,
  onDelete,
  onGiveShortcutSlot,
  onCreate,
  onClose,
}: DesktopOverviewProps) {
  const slotted = useMemo(() => slottedDesktops(desktops), [desktops]);
  const extras = useMemo(() => extraDesktops(desktops), [desktops]);
  const ordered = useMemo(() => [...slotted, ...extras], [slotted, extras]);
  const [focusedId, setFocusedId] = useState<string | null>(currentDesktopId);
  const focusedIndex = Math.max(0, ordered.findIndex((desktop) => desktop.id === focusedId));
  const focused = ordered[focusedIndex];
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEscapeStack(onClose, true);
  useEffect(() => {
    dialogRef.current?.focus({ preventScroll: true });
  }, []);

  const act = (run: () => void) => {
    run();
    onClose();
  };

  const canSendTo = (desktop: Desktop) => canSendActivePane && desktop.id !== currentDesktopId;
  const canDelete = (desktop: Desktop) => isEmptyDesktop(desktop) && desktop.id !== currentDesktopId;

  const moveFocus = (delta: number) => {
    if (ordered.length === 0) return;
    const next = Math.min(ordered.length - 1, Math.max(0, focusedIndex + delta));
    setFocusedId(ordered[next].id);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLDialogElement>) => {
    if (event.target !== dialogRef.current) return;
    const steps: Record<string, number> = {
      ArrowRight: 1,
      ArrowLeft: -1,
      ArrowDown: GRID_COLUMNS,
      ArrowUp: -GRID_COLUMNS,
    };
    if (event.key in steps) {
      event.preventDefault();
      moveFocus(steps[event.key]);
      return;
    }
    if (event.key === 'Enter' && focused) {
      event.preventDefault();
      if (event.shiftKey) {
        if (canSendTo(focused)) act(() => onSendActivePane(focused.id));
        return;
      }
      act(() => onSwitch(focused.id));
      return;
    }
    if ((event.key === 'Delete' || event.key === 'Backspace') && focused && canDelete(focused)) {
      event.preventDefault();
      onDelete(focused.id);
      return;
    }
    const digit = /^Digit([1-9])$/.exec(event.code);
    if (digit && !event.metaKey && !event.ctrlKey && !event.altKey) {
      const target = slotted.find((desktop) => desktop.shortcut_slot === Number(digit[1]));
      if (target) {
        event.preventDefault();
        act(() => onSwitch(target.id));
      }
    }
  };

  const card = (desktop: Desktop) => {
    const leaves = miniLayout(desktop);
    const label = desktopLabel(desktop, desktops);
    return (
      <div
        key={desktop.id}
        className={[
          'desktop-overview-card',
          desktop.id === currentDesktopId ? 'current' : '',
          desktop.id === focused?.id ? 'focused' : '',
        ].join(' ')}
        data-desktop-id={desktop.id}
      >
        <button type="button" className="desktop-overview-open" onClick={() => act(() => onSwitch(desktop.id))}>
          <span className="desktop-overview-card-head">
            <span className={`desktop-overview-slot ${desktop.shortcut_slot ? '' : 'none'}`}>
              {desktop.shortcut_slot ? slotShortcut(desktop.shortcut_slot) : 'no shortcut'}
            </span>
            <span className="desktop-overview-name">{label}</span>
            {leaves.length === 0 && <span className="desktop-overview-empty">empty</span>}
          </span>
          <span className="desktop-overview-mini" aria-hidden="true">
            {leaves.map((leaf) => (
              <span
                key={leaf.id}
                className={leaf.id === desktop.active_pane_id ? 'active' : ''}
                style={{
                  left: `${leaf.left * 100}%`,
                  top: `${leaf.top * 100}%`,
                  width: `${leaf.width * 100}%`,
                  height: `${leaf.height * 100}%`,
                }}
              >
                {leaf.label}
              </span>
            ))}
          </span>
        </button>
        <div className="desktop-overview-actions">
          {canSendTo(desktop) && (
            <button type="button" onClick={() => act(() => onSendActivePane(desktop.id))}>
              Send focused pane here ⇧↵
            </button>
          )}
          {canDelete(desktop) && (
            <button type="button" onClick={() => onDelete(desktop.id)}>
              Delete
            </button>
          )}
          {!desktop.shortcut_slot && (
            <button type="button" onClick={() => onGiveShortcutSlot(desktop.id)}>
              Give a shortcut
            </button>
          )}
        </div>
      </div>
    );
  };

  return (
    <div className="desktop-overview-scrim">
      <button type="button" className="desktop-overview-dismiss" aria-label="Close the overview" onClick={onClose} />
      <dialog
        open
        ref={dialogRef}
        className="desktop-overview"
        aria-label="Desktop overview"
        tabIndex={-1}
        onKeyDown={handleKeyDown}
      >
        <h2>
          {setupName} · {desktops.length} {desktops.length === 1 ? 'desktop' : 'desktops'}
        </h2>
        <div className="desktop-overview-hint">
          Arrows move · ↵ switch · ⇧↵ send the focused pane · digits switch · Delete removes an empty desktop · Esc closes
        </div>
        <div className="desktop-overview-grid">
          {slotted.map(card)}
          {extras.length > 0 && <div className="desktop-overview-separator">More desktops · no shortcut</div>}
          {extras.map(card)}
          <button type="button" className="desktop-overview-card new" onClick={() => act(onCreate)}>
            + New desktop
          </button>
        </div>
      </dialog>
    </div>
  );
}
