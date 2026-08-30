import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { PaneSeedPopoverRow } from './paneSeedDisplay';
import './TendedSeedsPopover.css';

const VIEWPORT_MARGIN = 8;

interface TendedSeedsPopoverProps {
  rows: PaneSeedPopoverRow[];
  anchor: { top: number; right: number };
  anchorRef?: React.RefObject<HTMLElement | null>;
  pinned: boolean;
  onOpenSeed: (seedId: string) => void;
  onClose: () => void;
  onPointerEnter?: () => void;
  onPointerLeave?: () => void;
}

function rowStatus(row: PaneSeedPopoverRow): string {
  if (row.role === 'crown') return 'crown';
  return row.seed?.status ?? 'planted';
}

function plotFraction(row: PaneSeedPopoverRow): { done: number; total: number } | null {
  const progress = row.seed?.plot_progress;
  if (!progress || !progress.total) return null;
  return { done: progress.done, total: progress.total };
}

export function TendedSeedsPopover({
  rows,
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

  useEscapeStack(onClose, pinned);

  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    let left = anchor.right - rect.width;
    let top = anchor.top;
    if (left < VIEWPORT_MARGIN) left = VIEWPORT_MARGIN;
    if (top + rect.height > window.innerHeight - VIEWPORT_MARGIN) {
      top = Math.max(VIEWPORT_MARGIN, window.innerHeight - rect.height - VIEWPORT_MARGIN);
    }
    setPosition({ top, left });
  }, [anchor.top, anchor.right]);

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
      role="listbox"
      aria-label="Seeds this agent is tending"
      tabIndex={-1}
      style={position ? { top: position.top, left: position.left } : { top: anchor.top, left: 0, visibility: 'hidden' }}
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
      onPointerDown={(event) => event.stopPropagation()}
      onKeyDown={(event) => {
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
                data-status={rowStatus(row)}
                onPointerMove={() => setSelected(index)}
                onClick={(event) => {
                  event.stopPropagation();
                  onClose();
                  onOpenSeed(row.seedId);
                }}
              >
                {row.role === 'plot' ? (
                  <span className="tended-seeds-mark tended-seeds-mark--plot" aria-hidden="true">
                    <i /><i /><i /><i />
                  </span>
                ) : (
                  <span
                    className={`tended-seeds-mark ${row.role === 'crown' ? 'tended-seeds-mark--crown' : 'tended-seeds-mark--seed'}`}
                    aria-hidden="true"
                  />
                )}
                <span className="tended-seeds-row-title">{title}</span>
                {fraction ? (
                  <span className="tended-seeds-row-fraction">
                    {fraction.done}/{fraction.total}
                  </span>
                ) : row.role === 'crown' ? (
                  <span className="tended-seeds-row-note">reports to</span>
                ) : (
                  <span className="tended-seeds-row-id">{row.seedId}</span>
                )}
              </button>
            </li>
          );
        })}
      </ul>
      {pinned ? (
        <div className="tended-seeds-popover-hint" aria-hidden="true">
          <span>↑↓ move</span>
          <span>↵ open</span>
          <span>esc close</span>
        </div>
      ) : null}
    </div>
  );

  // The trap is what actually holds keyboard focus against the terminal's own
  // refocusing; a hover popover must never steal focus, so it goes untrapped.
  return pinned ? <PinnedTrap containerRef={containerRef}>{surface}</PinnedTrap> : surface;
}

function PinnedTrap({
  containerRef,
  children,
}: {
  containerRef: React.RefObject<HTMLDivElement | null>;
  children: ReactNode;
}) {
  return (
    <FocusTrap
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
