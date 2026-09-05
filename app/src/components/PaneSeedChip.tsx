import { useState } from 'react';
import './PaneSeedChip.css';
import type { PaneSeedDisplay } from './paneSeedDisplay';
import { plotStateCounts, popoverRows, seedChipPresentation } from './paneSeedDisplay';
import { TendedSeedsPopover } from './TendedSeedsPopover';
import { SeedPlotIcon, SeedStateIcon } from './SeedStateIcon';
import { useAnchoredPopover } from './useAnchoredPopover';

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
  const popover = useAnchoredPopover(pinned, onPopoverClosed);
  const [replay, setReplay] = useState(0);
  const presentation = seedChipPresentation(display);
  if (!presentation) return null;

  const rows = popoverRows(display, crownSeedId);
  const { label, status, stateLabel, aggregate, seedId, clickTarget, fraction } = presentation;

  return (
    <>
      <button
        ref={popover.anchorRef}
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
        onPointerEnter={() => {
          popover.scheduleOpen();
          setReplay((value) => value + 1);
        }}
        onPointerLeave={popover.scheduleClose}
        onFocus={() => setReplay((value) => value + 1)}
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
      {popover.open && popover.anchor && rows.length > 0 ? (
        <TendedSeedsPopover
          rows={rows}
          crownSeedId={crownSeedId}
          anchor={popover.anchor}
          anchorRef={popover.anchorRef}
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
