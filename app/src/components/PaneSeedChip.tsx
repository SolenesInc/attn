import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import './PaneSeedChip.css';
import type { PaneSeedDisplay } from './paneSeedDisplay';
import { popoverRows } from './paneSeedDisplay';
import { TendedSeedsPopover } from './TendedSeedsPopover';

const HOVER_OPEN_MS = 160;
const HOVER_CLOSE_MS = 240;

// Guards the leaf-drag: onPointerDown stops propagation so a click that drifts >=4px
// cannot relocate the pane, and onClick stops the header from re-selecting it.
export function PaneSeedChip({
  display,
  crownSeedId,
  unread,
  sessionId,
  pinned,
  onOpenSeed,
  onPopoverClosed,
}: {
  display: PaneSeedDisplay;
  crownSeedId?: string;
  unread: boolean;
  sessionId: string;
  pinned: boolean;
  onOpenSeed: (seedId: string) => void;
  onPopoverClosed: () => void;
}) {
  const chipRef = useRef<HTMLButtonElement>(null);
  const [hoverOpen, setHoverOpen] = useState(false);
  const [clickPinned, setClickPinned] = useState(false);
  const [anchor, setAnchor] = useState<{ top: number; right: number } | null>(null);
  const openTimer = useRef<number | undefined>(undefined);
  const closeTimer = useRef<number | undefined>(undefined);

  const popoverPinned = pinned || clickPinned;
  const popoverOpen = popoverPinned || hoverOpen;

  // Measured once per open: a per-render rect is a fresh object every time and
  // would loop the popover's layout effect.
  useLayoutEffect(() => {
    if (!popoverOpen) {
      setAnchor(null);
      return;
    }
    const rect = chipRef.current?.getBoundingClientRect();
    if (rect) setAnchor({ top: rect.bottom + 4, right: rect.right });
  }, [popoverOpen]);

  useEffect(() => () => {
    window.clearTimeout(openTimer.current);
    window.clearTimeout(closeTimer.current);
  }, []);

  const scheduleOpen = useCallback(() => {
    window.clearTimeout(closeTimer.current);
    window.clearTimeout(openTimer.current);
    openTimer.current = window.setTimeout(() => setHoverOpen(true), HOVER_OPEN_MS);
  }, []);

  const scheduleClose = useCallback(() => {
    window.clearTimeout(openTimer.current);
    window.clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => setHoverOpen(false), HOVER_CLOSE_MS);
  }, []);

  const closePopover = useCallback(() => {
    window.clearTimeout(openTimer.current);
    window.clearTimeout(closeTimer.current);
    setHoverOpen(false);
    setClickPinned(false);
    if (pinned) onPopoverClosed();
  }, [pinned, onPopoverClosed]);

  if (display.kind === 'none') return null;

  const rows = popoverRows(display, crownSeedId);

  let label: string;
  let idText: string | null = null;
  let status: string;
  let fraction: string | null = null;
  switch (display.kind) {
    case 'crown': {
      label = display.seed?.title.trim() || display.seedId;
      idText = display.seed?.title.trim() ? display.seedId : null;
      status = 'crown';
      break;
    }
    case 'seed': {
      label = display.seed.title.trim() || display.seed.id;
      idText = display.seed.title.trim() ? display.seed.id : null;
      status = display.seed.status;
      break;
    }
    case 'plot': {
      label = display.plot.title.trim() || display.plot.id;
      status = 'plot';
      const progress = display.plot.plot_progress;
      if (progress && progress.total) fraction = `${progress.done}/${progress.total}`;
      break;
    }
    case 'multi': {
      label = `tending ${display.tended.length}`;
      status = 'multi';
      break;
    }
  }

  const clickTarget = display.kind === 'crown' ? display.seedId : display.kind === 'seed' ? display.seed.id : null;

  return (
    <>
      <button
        ref={chipRef}
        type="button"
        className="pane-seed-chip"
        data-kind={display.kind}
        data-status={status}
        data-testid={`seed-chip-${sessionId}`}
        onPointerDown={(event) => event.stopPropagation()}
        onPointerEnter={scheduleOpen}
        onPointerLeave={scheduleClose}
        onClick={(event) => {
          event.stopPropagation();
          if (popoverPinned) {
            closePopover();
          } else if (clickTarget) {
            closePopover();
            onOpenSeed(clickTarget);
          } else {
            setClickPinned(true);
          }
        }}
        title={display.kind === 'multi' ? 'Seeds this agent is tending' : `${label}. Click to open`}
      >
        {display.kind === 'plot' ? (
          <span className="pane-seed-chip-mark pane-seed-chip-mark--plot" aria-hidden="true">
            <i /><i /><i /><i />
          </span>
        ) : (
          <span
            className={`pane-seed-chip-mark ${display.kind === 'crown' ? 'pane-seed-chip-mark--crown' : 'pane-seed-chip-mark--seed'}`}
            aria-hidden="true"
          />
        )}
        <span className="pane-seed-chip-title">{label}</span>
        {fraction ? <span className="pane-seed-chip-fraction">{fraction}</span> : null}
        {idText ? <span className="pane-seed-chip-id">{idText}</span> : null}
        {unread ? (
          <span
            className="pane-seed-chip-unread"
            data-testid={`seed-chip-unread-${sessionId}`}
            aria-label="Unread activity"
          />
        ) : null}
      </button>
      {popoverOpen && anchor && rows.length > 0 ? (
        <TendedSeedsPopover
          rows={rows}
          anchor={anchor}
          anchorRef={chipRef}
          pinned={popoverPinned}
          onOpenSeed={onOpenSeed}
          onClose={closePopover}
          onPointerEnter={() => window.clearTimeout(closeTimer.current)}
          onPointerLeave={scheduleClose}
        />
      ) : null}
    </>
  );
}
