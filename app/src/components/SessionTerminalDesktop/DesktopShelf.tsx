import { useLayoutEffect, useRef, useState } from 'react';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { formatShortcut } from '../../shortcuts/formatShortcut';
import { triggerShortcut } from '../../shortcuts/useShortcut';
import { useAsksOf } from '../../store/asks';
import { useDesktopShelfStore, type ShelfColumn, type ShelfTile } from '../../store/desktopShelf';
import { useSessionStore } from '../../store/sessions';
import type { AskHolderSession } from '../../utils/asks';
import { StateIndicator } from '../StateIndicator';
import { NARROWEST_STEPS } from './rowLayout';
import { closeShelfPeek, useShelfPeekHover } from './useShelfPeekHover';
import './DesktopShelf.css';

const CHIP_PX = 112;
const LIVE_DOT_PX = 28;
const CAPS_PX = 104;

const NOTHING: readonly string[] = [];

function needsYou(tile: ShelfTile, unseen: readonly string[]): boolean {
  return tile.state === 'waiting_input' || tile.state === 'pending_approval' || unseen.includes(tile.leafId);
}

function useChipMarks(desktopId: string) {
  const unseen = useDesktopShelfStore((state) => state.unseenByDesktop[desktopId] ?? NOTHING);
  const flashing = useDesktopShelfStore((state) => state.flashingByDesktop[desktopId] ?? NOTHING);
  return (columns: readonly ShelfColumn[]) => {
    const tiles = columns.flatMap((column) => column.tiles);
    const needs = tiles.some((tile) => needsYou(tile, unseen)) ? ' is-needs' : '';
    const flash = tiles.some((tile) => flashing.includes(tile.leafId)) ? ' is-arrival' : '';
    return {
      className: needs + flash,
      onAnimationEnd: () => useDesktopShelfStore.getState().flashEnded(desktopId, tiles.map((tile) => tile.leafId)),
    };
  };
}

function TileMark({ tile }: { tile: ShelfTile }) {
  if (tile.state) {
    return <StateIndicator state={tile.state} offline={tile.offline} size="sm" seed={tile.askHolder?.id} />;
  }
  return <span className={`desktop-shelf-mark desktop-shelf-mark--${tile.kind}`} aria-hidden="true" />;
}

function AskMark({ session }: { session: AskHolderSession }) {
  return useAsksOf(session).length > 0 ? <span className="ask-marker" aria-hidden="true">?</span> : null;
}

function ColumnChip({ column, desktopId, onGo }: { column: ShelfColumn; desktopId: string; onGo?: () => void }) {
  const peekHover = useShelfPeekHover(desktopId, column.goLeafId);
  const marks = useChipMarks(desktopId)([column]);
  return (
    <button
      type="button"
      className={`desktop-shelf-chip${marks.className}`}
      onAnimationEnd={marks.onAnimationEnd}
      data-testid={`shelf-chip-${column.key}`}
      title={column.label}
      {...peekHover}
      onMouseDown={(event) => event.preventDefault()}
      onClick={() => {
        closeShelfPeek();
        onGo?.();
        useSessionStore.getState().selectLeaf(desktopId, column.goLeafId);
      }}
    >
      {column.tiles.map((tile) => <TileMark key={tile.leafId} tile={tile} />)}
      <span className="desktop-shelf-chip-label">{column.label}</span>
      {column.tiles.map((tile) => (tile.askHolder ? <AskMark key={tile.leafId} session={tile.askHolder} /> : null))}
    </button>
  );
}

function Cap({ columns, side, desktopId }: { columns: ShelfColumn[]; side: 'before' | 'after'; desktopId: string }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLSpanElement>(null);
  const marks = useChipMarks(desktopId)(columns);
  useEscapeStack(() => setOpen(false), open);
  useLayoutEffect(() => {
    if (!open) return;
    const close = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    window.addEventListener('pointerdown', close, true);
    return () => window.removeEventListener('pointerdown', close, true);
  }, [open]);
  if (columns.length === 0) return null;
  return (
    <span className="desktop-shelf-cap-anchor" ref={root}>
      <button
        type="button"
        className={`desktop-shelf-chip desktop-shelf-cap${marks.className}`}
        onAnimationEnd={marks.onAnimationEnd}
        data-testid={`shelf-cap-${side}`}
        aria-expanded={open}
        title={`${columns.length} more this way`}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => setOpen((current) => !current)}
      >
        {side === 'before' ? `‹ ${columns.length}` : `${columns.length} ›`}
      </button>
      {open && (
        <div className={`desktop-shelf-menu desktop-shelf-menu--${side}`} role="menu" data-testid={`shelf-menu-${side}`}>
          {columns.map((column) => (
            <ColumnChip key={column.key} column={column} desktopId={desktopId} onGo={() => setOpen(false)} />
          ))}
        </div>
      )}
    </span>
  );
}

function WidthControl({ shown, narrowestCols }: { shown: number; narrowestCols: number }) {
  return (
    <span className="desktop-shelf-stepper" data-testid="shelf-width-control">
      <span><b>{shown}</b> · ≥{narrowestCols} cols</span>
      <button
        type="button"
        title={`Fewer, wider tiles (${formatShortcut('shelf.fewerTiles')})`}
        aria-label="Fewer, wider tiles"
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => triggerShortcut('shelf.fewerTiles')}
      >
        −
      </button>
      <button
        type="button"
        title={`More, narrower tiles (${formatShortcut('shelf.moreTiles')})`}
        aria-label="More, narrower tiles"
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => triggerShortcut('shelf.moreTiles')}
      >
        +
      </button>
    </span>
  );
}

function allotChips(available: number, before: number, after: number, liveDots: number) {
  const withoutCaps = Math.floor((available - liveDots * LIVE_DOT_PX) / CHIP_PX);
  if (before + after <= withoutCaps) return { left: before, right: after };
  const room = Math.max(0, Math.floor((available - liveDots * LIVE_DOT_PX - CAPS_PX) / CHIP_PX));
  let left = 0;
  let right = 0;
  while (left + right < room && (left < before || right < after)) {
    if (right < after && (right <= left || left >= before)) right += 1;
    else left += 1;
  }
  return { left, right };
}

export function useDesktopHasShelf(desktopId: string | null): boolean {
  return useDesktopShelfStore((state) => {
    const shelf = desktopId ? state.shelfByDesktop[desktopId] : undefined;
    return Boolean(shelf && shelf.before.length + shelf.after.length > 0);
  });
}

export function DesktopShelf({ desktopId }: { desktopId: string }) {
  const shelf = useDesktopShelfStore((state) => state.shelfByDesktop[desktopId]);
  const root = useRef<HTMLDivElement>(null);
  const [available, setAvailable] = useState(0);
  const visible = Boolean(shelf && shelf.before.length + shelf.after.length > 0);
  useLayoutEffect(() => {
    const element = root.current;
    if (!element) return;
    setAvailable(element.getBoundingClientRect().width);
    const observer = new ResizeObserver(([entry]) => setAvailable(entry.contentRect.width));
    observer.observe(element);
    return () => observer.disconnect();
  }, [visible]);
  if (!shelf || !visible) return null;

  const { left, right } = allotChips(available, shelf.before.length, shelf.after.length, shelf.screen.length);
  const splitBefore = shelf.before.length - left;
  return (
    <div className="desktop-shelf" ref={root} data-testid="desktop-shelf">
      <Cap columns={shelf.before.slice(0, splitBefore)} side="before" desktopId={desktopId} />
      {shelf.before.slice(splitBefore).map((column) => (
        <ColumnChip key={column.key} column={column} desktopId={desktopId} />
      ))}
      <span className="desktop-shelf-live" data-testid="shelf-live-dots">
        {shelf.screen.map((column) => (
          <button
            key={column.key}
            type="button"
            className={`desktop-shelf-dot${column.tiles.some((tile) => tile.leafId === shelf.activeLeafId) ? ' is-current' : ''}`}
            data-testid={`shelf-dot-${column.key}`}
            title={column.label}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => useSessionStore.getState().selectLeaf(desktopId, column.goLeafId)}
          >
            {column.tiles.map((tile) => <TileMark key={tile.leafId} tile={tile} />)}
          </button>
        ))}
        <WidthControl shown={shelf.screen.length} narrowestCols={shelf.narrowestCols} />
      </span>
      {shelf.after.slice(0, right).map((column) => (
        <ColumnChip key={column.key} column={column} desktopId={desktopId} />
      ))}
      <Cap columns={shelf.after.slice(right)} side="after" desktopId={desktopId} />
    </div>
  );
}

export function ShelfWidthLabel({ desktopId }: { desktopId: string }) {
  const shelf = useDesktopShelfStore((state) => state.shelfByDesktop[desktopId]);
  if (!shelf) return null;
  const shown = shelf.screen.length;
  return (
    <div className="desktop-shelf-width-label" role="status" data-testid="shelf-width-label">
      <b>{shown} tile{shown === 1 ? '' : 's'}</b>
      <span>at least {shelf.narrowestCols} cols each</span>
      <span className="desktop-shelf-width-steps">
        {NARROWEST_STEPS.map((cols) => (
          <i key={cols} className={cols === shelf.narrowestCols ? 'is-on' : undefined}>{cols}</i>
        ))}
      </span>
    </div>
  );
}
