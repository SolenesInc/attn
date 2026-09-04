import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import './PaneSeedChip.css';
import type { PaneSeedDisplay } from './paneSeedDisplay';
import { plotStateCounts, popoverRows, seedChipPresentation } from './paneSeedDisplay';
import { TendedSeedsPopover } from './TendedSeedsPopover';
import { SeedPlotIcon, SeedStateIcon } from './SeedStateIcon';

const HOVER_OPEN_MS = 160;
const HOVER_CLOSE_MS = 240;

function useSeedChipPopover(pinned: boolean, onPopoverClosed: () => void) {
  const chipRef = useRef<HTMLButtonElement>(null);
  const [hoverOpen, setHoverOpen] = useState(false);
  const [clickPinned, setClickPinned] = useState(false);
  const [anchor, setAnchor] = useState<{ top: number; right: number } | null>(null);
  const openTimer = useRef<number | undefined>(undefined);
  const closeTimer = useRef<number | undefined>(undefined);
  const [replay, setReplay] = useState(0);

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
    setReplay((value) => value + 1);
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

  return {
    chipRef, anchor, replay,
    open: popoverOpen,
    pinned: popoverPinned,
    scheduleOpen, scheduleClose,
    close: closePopover,
    pin: () => setClickPinned(true),
    replayAnimation: () => setReplay((value) => value + 1),
    cancelClose: () => window.clearTimeout(closeTimer.current),
  };
}

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
  const popover = useSeedChipPopover(pinned, onPopoverClosed);
  const presentation = seedChipPresentation(display);
  if (!presentation) return null;

  const rows = popoverRows(display, crownSeedId);
  const { label, status, stateLabel, aggregate, seedId, clickTarget, fraction } = presentation;

  return (
    <>
      <button
        ref={popover.chipRef}
        type="button"
        className="pane-seed-chip"
        data-kind={display.kind}
        data-status={status}
        data-seed-state={aggregate ? 'growing' : status}
        data-seed-id={seedId}
        data-testid={`seed-chip-${sessionId}`}
        title=""
        aria-label={`${label}, ${stateLabel}${unread ? ', unread activity' : ''}. ${clickTarget ? 'Open seed' : 'Show seeds'}`}
        aria-haspopup={aggregate ? 'listbox' : undefined}
        aria-expanded={popover.open}
        onPointerDown={(event) => event.stopPropagation()}
        onPointerEnter={popover.scheduleOpen}
        onPointerLeave={popover.scheduleClose}
        onFocus={popover.replayAnimation}
        onBlur={popover.scheduleClose}
        onKeyDown={(event) => {
          if (event.key === 'ArrowDown') {
            event.preventDefault();
            event.stopPropagation();
            popover.pin();
          }
        }}
        onClick={(event) => {
          event.stopPropagation();
          if (popover.pinned) {
            popover.close();
          } else if (clickTarget) {
            popover.close();
            onOpenSeed(clickTarget);
          } else {
            popover.pin();
          }
        }}
      >
        {aggregate ? <SeedPlotIcon /> : <SeedStateIcon status={status} replay={popover.replay} />}
        <span className="pane-seed-chip-title">{label}</span>
        {display.kind === 'plot' ? (
          <span className="pane-seed-counts">
            {plotStateCounts(display.plot).map(([state, count]) => (
              <span key={state} data-seed-state={state} title={`${count} ${state}`}>
                <SeedStateIcon status={state} size={18} />{count}
              </span>
            ))}
          </span>
        ) : null}
        {fraction ? <span className="pane-seed-chip-fraction">{fraction}</span> : null}
        {!aggregate ? <span className="seed-state-label">{stateLabel}</span> : null}
        {unread ? (
          <span
            className="pane-seed-chip-unread"
            data-testid={`seed-chip-unread-${sessionId}`}
            aria-label="Unread activity"
          />
        ) : null}
      </button>
      {popover.open && popover.anchor && rows.length > 0 ? (
        <TendedSeedsPopover
          rows={rows}
          crownSeedId={crownSeedId}
          anchor={popover.anchor}
          anchorRef={popover.chipRef}
          pinned={popover.pinned}
          onOpenSeed={onOpenSeed}
          onClose={popover.close}
          onPointerEnter={popover.cancelClose}
          onPointerLeave={popover.scheduleClose}
        />
      ) : null}
    </>
  );
}
