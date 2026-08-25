import { useEffect, useMemo, useRef, useState } from 'react';
import { attachTerminalInput } from '../../ghostty';
import { loadGhostty } from '../../ghostty/wasm';
import { listenPtyEvents, ptyWrite } from '../../pty/bridge';
import { createTerminalKeyInterceptor } from '../SessionTerminalWorkspace/terminalKeyHandler';
import type { ScreenSnapshotResult } from '../../hooks/useDaemonSocket';
import type { UISessionState } from '../../types/sessionState';
import { getTerminalTheme, getTerminalAnsiPalette } from '../../utils/terminalSizing';
import { WebGlGridRenderer } from './WebGlGridRenderer';
import { ensureTerminalIconFont } from '../../utils/terminalIconFont';
import { TerminalGrid, type GridTileSpec } from './TerminalGrid';
import type { Rect } from './GridRenderer';
import { GridHiddenSessions, type HiddenGridSession } from './GridHiddenSessions';
import { setGridAutomationHandle, INACTIVE_GRID_STATE } from './gridAutomation';
import {
  FONT_FAMILY,
  FONT_SIZE,
  TERMINAL_SCROLLBACK_BYTES,
  colorNumber,
  measureCanonicalCell,
} from './gridConfig';
import './grid.css';

export interface GridSessionTile {
  runtimeId: string;
  sessionId: string;
  title: string;
  attention: boolean;
  state: UISessionState;
}

interface GridViewProps {
  tiles: GridSessionTile[];
  layout: { rows: number; cols: number };
  offBoardCount?: number;
  hiddenSessions?: HiddenGridSession[];
  onRemoveTile?: (sessionId: string) => void;
  onRestoreTile?: (sessionId: string) => void;
  resolvedTheme: Parameters<typeof getTerminalTheme>[0];
  getScreenSnapshot?: (runtimeId: string) => Promise<ScreenSnapshotResult | null>;
}

const RESET_BYTES = new TextEncoder().encode('\x1bc');

function b64ToBytes(b64: string): Uint8Array {
  const binary = atob(b64);
  const len = binary.length;
  const out = new Uint8Array(len);
  for (let i = 0; i < len; i += 1) out[i] = binary.charCodeAt(i);
  return out;
}

const toSpecs = (tiles: GridSessionTile[]): GridTileSpec[] =>
  tiles.map((t) => ({ id: t.runtimeId, attention: t.attention, state: t.state }));

export function GridView({
  tiles,
  layout,
  offBoardCount = 0,
  hiddenSessions = [],
  onRemoveTile,
  onRestoreTile,
  resolvedTheme,
  getScreenSnapshot,
}: GridViewProps) {
  const stageRef = useRef<HTMLDivElement | null>(null);
  const gridRef = useRef<TerminalGrid | null>(null);
  const tilesRef = useRef(tiles);
  tilesRef.current = tiles;

  const sessionIdByRuntime = useMemo(() => {
    const map = new Map<string, string>();
    for (const t of tiles) map.set(t.runtimeId, t.sessionId);
    return map;
  }, [tiles]);

  // rect is container-space, aligning with the canvas tiles.
  const [removeTarget, setRemoveTarget] = useState<{ sessionId: string; rect: Rect } | null>(null);
  const layoutRef = useRef(layout);
  layoutRef.current = layout;

  const seedGenRef = useRef<Map<string, number>>(new Map());
  const seedCounterRef = useRef(0);
  const getSnapshotRef = useRef(getScreenSnapshot);
  getSnapshotRef.current = getScreenSnapshot;

  const reconcileSeeding = useRef((grid: TerminalGrid) => {
    const liveIds = new Set(tilesRef.current.map((t) => t.runtimeId));
    for (const id of [...seedGenRef.current.keys()]) {
      if (!liveIds.has(id)) seedGenRef.current.delete(id);
    }
    const fetchSnapshot = getSnapshotRef.current;
    for (const id of liveIds) {
      if (seedGenRef.current.has(id)) continue;
      const gen = (seedCounterRef.current += 1);
      seedGenRef.current.set(id, gen);
      if (!fetchSnapshot) continue;
      grid.beginSeeding(id);
      fetchSnapshot(id)
        .then((result) => {
          if (seedGenRef.current.get(id) !== gen) return;
          if (gridRef.current !== grid || !grid.hasTile(id)) return;
          if (!result) {
            grid.cancelSeeding(id);
            return;
          }
          const bytes = result.screenSnapshot ? b64ToBytes(result.screenSnapshot) : new Uint8Array(0);
          grid.seedTile(id, bytes, result.lastSeq, result.screenCols, result.screenRows);
        })
        .catch(() => {
          if (seedGenRef.current.get(id) === gen && gridRef.current === grid) grid.cancelSeeding(id);
        });
    }
  }).current;

  const signature = useMemo(
    () => tiles.map((t) => `${t.runtimeId}:${t.state}:${t.attention ? 1 : 0}`).join('|'),
    [tiles],
  );

  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;

    let disposed = false;
    let unlisten: (() => void) | null = null;
    let disposeInput: (() => void) | null = null;
    let resizeObserver: ResizeObserver | null = null;
    const metrics = measureCanonicalCell();
    const theme = getTerminalTheme(resolvedTheme);
    const renderer = new WebGlGridRenderer(FONT_SIZE, FONT_FAMILY, metrics, {
      background: theme.background,
      foreground: theme.foreground,
      cursor: theme.cursor,
    });

    void loadGhostty().then((ghostty) => {
      if (disposed) return;
      const grid = new TerminalGrid(renderer, ghostty, stage, metrics, {
        scrollbackLimit: TERMINAL_SCROLLBACK_BYTES,
        fgColor: colorNumber(theme.foreground),
        bgColor: colorNumber(theme.background),
        cursorColor: colorNumber(theme.cursor),
        palette: getTerminalAnsiPalette(resolvedTheme),
      });
      gridRef.current = grid;
      const current = tilesRef.current;
      grid.syncTiles(toSpecs(current));
      grid.setLayout(layoutRef.current.rows, layoutRef.current.cols);
      reconcileSeeding(grid);
      grid.start();
      resizeObserver = new ResizeObserver(() => grid.invalidate());
      resizeObserver.observe(stage);

      // The grid can open before the Nerd Font loads, caching blank icon glyphs.
      void ensureTerminalIconFont(FONT_SIZE).then(() => {
        if (!disposed) {
          renderer.invalidateGlyphCache();
          grid.invalidate();
        }
      });

      const forward = (data: string) => {
        const id = gridRef.current?.zoomedId();
        if (id) void ptyWrite({ id, data });
      };
      disposeInput = attachTerminalInput({
        element: stage,
        terminal: () => gridRef.current?.inputTarget() ?? null,
        send: forward,
        interceptKey: createTerminalKeyInterceptor(forward),
        onError: (operation, error) => {
          console.error(`[grid] terminal ${operation} input failed`, error);
        },
      });
      stage.focus({ preventScroll: true });

      setGridAutomationHandle({
        getState: () => {
          const grid = gridRef.current;
          if (!grid) return INACTIVE_GRID_STATE;
          const tileStates = grid.tileSummaries();
          return {
            active: true,
            tileCount: tileStates.length,
            zoomedId: grid.zoomedId(),
            layout: grid.currentLayout(),
            stats: grid.getStats(),
            tiles: tileStates,
          };
        },
        getTileText: (id) => gridRef.current?.getTileText(id) ?? null,
        zoom: (id) => gridRef.current?.zoomTo(id),
        hitTest: (x, y) => gridRef.current?.hitTest(x, y) ?? null,
        sendText: (text) => {
          const stageEl = stageRef.current;
          if (!stageEl) return false;
          stageEl.focus({ preventScroll: true });
          for (const ch of text) {
            const enter = ch === '\n' || ch === '\r';
            stageEl.dispatchEvent(new KeyboardEvent('keydown', {
              key: enter ? 'Enter' : ch,
              code: enter ? 'Enter' : undefined,
              bubbles: true,
              cancelable: true,
            }));
          }
          return true;
        },
      });
    });

    void listenPtyEvents((evt) => {
      const grid = gridRef.current;
      if (!grid) return;
      const p = evt.payload;
      if (p.event === 'data') {
        if (grid.hasTile(p.id)) {
          grid.writeBytes(p.id, typeof p.data === 'string' ? b64ToBytes(p.data) : p.data, p.seq);
        }
      } else if (p.event === 'local_resize') {
        if (grid.hasTile(p.id)) grid.resizeTile(p.id, p.cols, p.rows);
      } else if (p.event === 'reset') {
        if (grid.hasTile(p.id)) grid.writeBytes(p.id, RESET_BYTES);
      }
    }).then((dispose) => {
      if (disposed) dispose();
      else unlisten = dispose;
    });

    return () => {
      disposed = true;
      setGridAutomationHandle(null);
      disposeInput?.();
      unlisten?.();
      resizeObserver?.disconnect();
      seedGenRef.current.clear();
      const grid = gridRef.current;
      gridRef.current = null;
      if (grid) grid.dispose();
      else renderer.dispose();
    };
  }, [resolvedTheme, reconcileSeeding]);

  useEffect(() => {
    const grid = gridRef.current;
    if (!grid) return;
    const current = tilesRef.current;
    grid.syncTiles(toSpecs(current));
    grid.setLayout(layoutRef.current.rows, layoutRef.current.cols);
    reconcileSeeding(grid);
  }, [signature, reconcileSeeding]);

  useEffect(() => {
    gridRef.current?.setLayout(layout.rows, layout.cols);
  }, [layout.rows, layout.cols]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      const grid = gridRef.current;
      if (grid?.isZoomed()) {
        e.preventDefault();
        e.stopPropagation();
        grid.zoomTo(null);
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, []);

  const onStageClick = (e: React.MouseEvent) => {
    const grid = gridRef.current;
    if (!grid) return;
    if (grid.isZoomed()) {
      grid.zoomTo(null);
      return;
    }
    const id = grid.hitTest(e.clientX, e.clientY);
    if (id) {
      grid.zoomTo(id);
      setRemoveTarget(null);
      stageRef.current?.focus({ preventScroll: true });
    }
  };

  // Handlers live on .grid-view, not the stage: the × button is a child, so
  // moving onto it fires no mouseleave and cannot flicker it.
  const updateRemoveTarget = (e: React.MouseEvent) => {
    const grid = gridRef.current;
    if (!grid || !onRemoveTile || grid.isZoomed()) {
      if (removeTarget) setRemoveTarget(null);
      return;
    }
    const hit = grid.tileAt(e.clientX, e.clientY);
    const sessionId = hit ? sessionIdByRuntime.get(hit.id) : undefined;
    if (!hit || !sessionId) {
      if (removeTarget) setRemoveTarget(null);
      return;
    }
    if (removeTarget && removeTarget.sessionId === sessionId) return;
    setRemoveTarget({ sessionId, rect: hit.rect });
  };

  const clearRemoveTarget = () => {
    if (removeTarget) setRemoveTarget(null);
  };

  return (
    <div className="grid-view" onMouseMove={updateRemoveTarget} onMouseLeave={clearRemoveTarget}>
      <div className="grid-view-stage" ref={stageRef} onClick={onStageClick} />
      {tiles.length === 0 && (
        <div className="grid-view-empty">No active sessions</div>
      )}
      {removeTarget && (
        <button
          type="button"
          className="grid-tile-remove"
          style={{
            left: `${removeTarget.rect.x + removeTarget.rect.w - 28}px`,
            top: `${removeTarget.rect.y + 8}px`,
          }}
          title="Remove from grid"
          aria-label="Remove from grid"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            onRemoveTile?.(removeTarget.sessionId);
            setRemoveTarget(null);
          }}
        >
          ×
        </button>
      )}
      <GridHiddenSessions sessions={hiddenSessions} onRestore={(id) => onRestoreTile?.(id)} />
      {offBoardCount > 0 && (
        <div className="grid-view-offboard">
          {offBoardCount} more {offBoardCount === 1 ? 'session' : 'sessions'} not shown · enlarge the grid or pick Auto
        </div>
      )}
      <div className="grid-view-hint">
        click a tile to zoom &amp; type{onRemoveTile ? ' · hover a tile to remove it' : ''} · Esc to exit zoom · ⌘G closes grid
      </div>
    </div>
  );
}
