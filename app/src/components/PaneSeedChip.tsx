import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import './PaneSeedChip.css';
import type { PaneSeedDisplay } from './paneSeedDisplay';
import { plotStateCounts, popoverRows } from './paneSeedDisplay';
import { TendedSeedsPopover } from './TendedSeedsPopover';
import type { Seed } from '../hooks/useDaemonSocket';
import { SeedPlotIcon, SeedStateIcon, seedStateLabel } from './SeedStateIcon';

const HOVER_OPEN_MS = 160;
const HOVER_CLOSE_MS = 240;

// Guards the leaf-drag: onPointerDown stops propagation so a click that drifts >=4px
// cannot relocate the pane, and onClick stops the header from re-selecting it.
export function PaneSeedChip({
  display,
  crownSeedId,
  crownSeed,
  unread,
  sessionId,
  pinned,
  onOpenSeed,
  onPopoverClosed,
}: {
  display: PaneSeedDisplay;
  crownSeedId?: string;
  crownSeed?: Seed;
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

  if (display.kind === 'none') return null;

  const rows = popoverRows(display, crownSeedId, crownSeed);

  let label: string;
  let status: string;
  let fraction: string | null = null;
  switch (display.kind) {
    case 'crown': {
      label = display.seed?.title.trim() || display.seedId;
      status = display.seed?.status ?? 'unknown';
      break;
    }
    case 'seed': {
      label = display.seed.title.trim() || display.seed.id;
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
  const aggregate = display.kind === 'plot' || display.kind === 'multi';
  const stateLabel = aggregate ? (fraction ? `${fraction} harvested` : 'Growing') : seedStateLabel(status);

  return (
    <>
      <button
        ref={chipRef}
        type="button"
        className="pane-seed-chip"
        data-kind={display.kind}
        data-status={status}
        data-seed-state={aggregate ? 'growing' : status}
        data-seed-id={clickTarget ?? (display.kind === 'plot' ? display.plot.id : undefined)}
        data-testid={`seed-chip-${sessionId}`}
        title=""
        aria-label={`${label}, ${stateLabel}${unread ? ', unread activity' : ''}. ${clickTarget ? 'Open seed' : 'Show seeds'}`}
        aria-haspopup={aggregate ? 'listbox' : undefined}
        aria-expanded={popoverOpen}
        onPointerDown={(event) => event.stopPropagation()}
        onPointerEnter={scheduleOpen}
        onPointerLeave={scheduleClose}
        onFocus={() => setReplay((value) => value + 1)}
        onBlur={scheduleClose}
        onKeyDown={(event) => {
          if (event.key === 'ArrowDown') {
            event.preventDefault();
            event.stopPropagation();
            setClickPinned(true);
          }
        }}
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
      >
        {aggregate ? <SeedPlotIcon /> : <SeedStateIcon status={status} replay={replay} />}
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
      {popoverOpen && anchor && rows.length > 0 ? (
        <TendedSeedsPopover
          rows={rows}
          crownSeedId={crownSeedId}
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
