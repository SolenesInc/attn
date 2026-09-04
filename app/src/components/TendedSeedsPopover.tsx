import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import FocusTrap from 'focus-trap-react';
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { PaneSeedPopoverRow } from './paneSeedDisplay';
import { PaneSeedContext } from './PaneSeedContext';
import { SeedPlotIcon, SeedStateIcon } from './SeedStateIcon';
import { seedStateLabel } from './seedStatePresentation';
import { terminalSeedBodyExcerpt } from '../utils/terminalSeedPreviewText';
import './TendedSeedsPopover.css';

const VIEWPORT_MARGIN = 8;

interface TendedSeedsPopoverProps {
  rows: PaneSeedPopoverRow[];
  crownSeedId?: string;
  anchor: { top: number; right: number };
  anchorRef?: React.RefObject<HTMLElement | null>;
  pinned: boolean;
  onOpenSeed: (seedId: string) => void;
  onClose: () => void;
  onPointerEnter?: () => void;
  onPointerLeave?: () => void;
}

function plotFraction(row: PaneSeedPopoverRow): { done: number; total: number } | null {
  const progress = row.seed?.plot_progress;
  if (!progress || !progress.total) return null;
  return { done: progress.done, total: progress.total };
}

export function TendedSeedsPopover({
  rows,
  crownSeedId,
  anchor,
  anchorRef,
  pinned,
  onOpenSeed,
  onClose,
  onPointerEnter,
  onPointerLeave,
}: TendedSeedsPopoverProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  const firstSelectable = rows.findIndex((row) => row.role !== 'crown');
  const [selected, setSelected] = useState(firstSelectable === -1 ? 0 : firstSelectable);
  const single = rows.length === 1 || (rows.length === 2 && rows[0].role === 'tended' && rows[1].role === 'crown');
  const primary = rows[0];

  useEscapeStack(onClose, pinned);

  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const place = () => {
      const rect = el.getBoundingClientRect();
      const source = anchorRef?.current?.getBoundingClientRect();
      const left = Math.max(VIEWPORT_MARGIN, Math.min(
        (source?.right ?? anchor.right) - rect.width,
        window.innerWidth - rect.width - VIEWPORT_MARGIN,
      ));
      const top = Math.max(VIEWPORT_MARGIN, Math.min(
        source ? source.bottom + 4 : anchor.top,
        window.innerHeight - rect.height - VIEWPORT_MARGIN,
      ));
      setPosition((previous) => previous?.left === left && previous.top === top ? previous : { top, left });
    };
    place();
    const observer = new ResizeObserver(place);
    observer.observe(el);
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [anchor.top, anchor.right, anchorRef]);

  // Capture phase: the terminal and pane handlers stop propagation of pointer
  // events, so a bubble-phase document listener never hears an outside click.
  useEffect(() => {
    if (!pinned) return;
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (containerRef.current?.contains(target)) return;
      if (anchorRef?.current?.contains(target)) return;
      onClose();
    };
    document.addEventListener('pointerdown', handlePointerDown, true);
    return () => document.removeEventListener('pointerdown', handlePointerDown, true);
  }, [pinned, onClose, anchorRef]);

  const move = (delta: number) => {
    if (rows.length === 0) return;
    setSelected((index) => (index + delta + rows.length) % rows.length);
  };

  const surface = (
    <div
      ref={containerRef}
      className="tended-seeds-popover"
      role={single ? 'dialog' : 'listbox'}
      aria-label={single ? 'Seed context' : 'Seeds this agent is tending'}
      tabIndex={-1}
      style={position ? { top: position.top, left: position.left } : { top: anchor.top, left: 0, visibility: 'hidden' }}
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
      onPointerDown={(event) => event.stopPropagation()}
      onKeyDown={(event) => {
        event.stopPropagation();
        if (single) {
          if (event.key === 'Enter' && event.target === event.currentTarget && primary) {
            event.preventDefault();
            onClose();
            onOpenSeed(primary.seedId);
          }
          return;
        }
        if (event.key === 'ArrowDown' || (event.key === 'n' && event.ctrlKey)) {
          event.preventDefault();
          move(1);
        } else if (event.key === 'ArrowUp' || (event.key === 'p' && event.ctrlKey)) {
          event.preventDefault();
          move(-1);
        } else if (event.key === 'Enter') {
          event.preventDefault();
          const row = rows[selected];
          if (row) {
            onClose();
            onOpenSeed(row.seedId);
          }
        }
      }}
    >
      {single && primary ? (
        <>
          <PaneSeedContext
            key={`${primary.seedId}:${primary.seed?.rev}`}
            seed={primary.seed}
            seedId={primary.seedId}
            reportsHere={primary.seedId === crownSeedId || primary.role === 'crown'}
          />
          {rows[1] ? (
            <button className="pane-seed-context-report" onClick={() => { onClose(); onOpenSeed(rows[1].seedId); }}>
              Reports to {rows[1].seed?.title || rows[1].seedId} ↗
            </button>
          ) : null}
          <button
            className="pane-seed-context-open"
            onClick={() => { onClose(); onOpenSeed(primary.seedId); }}
          >
            <span>Open seed <span aria-hidden="true">↗</span></span>
            <span className="pane-seed-context-id">{primary.seedId}</span>
          </button>
        </>
      ) : (
        <ul className="tended-seeds-popover-list">
          {rows.map((row, index) => {
            const fraction = row.role === 'plot' ? plotFraction(row) : null;
            const title = row.seed?.title.trim() || row.seedId;
            return (
              <li key={row.seedId}>
                <button
                  type="button"
                  role="option"
                  aria-selected={pinned && index === selected}
                  className={`tended-seeds-row tended-seeds-row--${row.role} ${pinned && index === selected ? 'tended-seeds-row--selected' : ''}`.trim()}
                  data-status={row.seed?.status ?? 'unknown'}
                  data-seed-state={row.seed?.status ?? 'unknown'}
                  onPointerMove={() => setSelected(index)}
                  onClick={(event) => {
                    event.stopPropagation();
                    onClose();
                    onOpenSeed(row.seedId);
                  }}
                >
                  {row.role === 'plot' ? (
                    <SeedPlotIcon />
                  ) : (
                    <SeedStateIcon status={row.seed?.status} />
                  )}
                  <span className="tended-seeds-row-content">
                    <span className="tended-seeds-row-title">{title}</span>
                    <span className="tended-seeds-row-note">
                      {row.role === 'crown' || row.seedId === crownSeedId ? 'Reports here. ' : ''}
                      {terminalSeedBodyExcerpt(row.seed?.reason || row.seed?.body || '', 100)}
                    </span>
                  </span>
                  {fraction ? (
                    <span className="tended-seeds-row-fraction">
                      {fraction.done}/{fraction.total}
                    </span>
                  ) : (
                    <span className="seed-state-label">{seedStateLabel(row.seed?.status)}</span>
                  )}
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {pinned && !single ? (
        <div className="tended-seeds-popover-hint" aria-hidden="true">
          <span>↑↓ move</span>
          <span>↵ open</span>
          <span>esc close</span>
        </div>
      ) : null}
    </div>
  );

  // Keep the same DOM when pinning so measurement and in-flight note reads survive.
  // Only a pinned preview traps focus against the terminal's refocusing.
  return createPortal(<PinnedTrap containerRef={containerRef} active={pinned}>{surface}</PinnedTrap>, document.body);
}

function PinnedTrap({
  containerRef,
  active,
  children,
}: {
  containerRef: React.RefObject<HTMLDivElement | null>;
  active: boolean;
  children: ReactNode;
}) {
  return (
    <FocusTrap
      active={active}
      focusTrapOptions={{
        allowOutsideClick: true,
        escapeDeactivates: false,
        initialFocus: () => containerRef.current ?? false,
        fallbackFocus: () => containerRef.current ?? document.body,
      }}
    >
      {children}
    </FocusTrap>
  );
}
