import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import '../../src/components/SessionTerminalDesktop/SessionTerminalDesktop.css';
import { useRowMotion, type RowMotionLayout } from '../../src/components/SessionTerminalDesktop/useRowMotion';
import type { NormalizedPaneBounds } from '../../src/types/desktop';
import { stillFrameOf } from '../../src/utils/stillFrame';
import type { HarnessProps } from '../types';

const WIDTH = 900;
const HEIGHT = 400;

interface Screen {
  row: string[];
  first: number;
  shown: number;
  paused?: boolean;
  shelfOff?: boolean;
}

const NEVER_PAINTED_TILE = 'never-painted';

function TileBody({ id, keepLeavingFrame }: { id: string; keepLeavingFrame: (leafId: string, frame: HTMLCanvasElement | null) => void }) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  useLayoutEffect(() => {
    const canvas = canvasRef.current;
    if (canvas && id === NEVER_PAINTED_TILE) {
      canvas.width = 0;
      canvas.height = 0;
    }
    return () => {
      if (canvas) keepLeavingFrame(id, stillFrameOf(canvas));
    };
  }, [id, keepLeavingFrame]);
  return <canvas ref={canvasRef} />;
}

function columnBounds(index: number, count: number): NormalizedPaneBounds {
  const width = 1 / count;
  const left = index * width;
  return { left, top: 0, right: left + width, bottom: 1, width, height: 1 };
}

declare global {
  interface Window {
    __rowMotionShow?: (screen: Screen) => void;
  }
}

export function RowMotionHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [screen, setScreen] = useState<Screen>({ row: ['a', 'b', 'c', 'd'], first: 0, shown: 2 });
  const shown = screen.row.slice(screen.first, screen.first + screen.shown);
  const hasRow = shown.length > 0 && !screen.shelfOff;
  const layout = useMemo<RowMotionLayout | null>(() => (hasRow
    ? {
      bounds: new Map(shown.map((id, index) => [id, columnBounds(index, shown.length)])),
      rowKey: screen.row.join('|'),
      firstColumnIndex: screen.first,
      widthPx: WIDTH,
      heightPx: HEIGHT,
    }
    : null), [hasRow, screen.first, screen.row, shown.join('|')]);
  const { keepLeavingFrame } = useRowMotion({ containerRef, layout, paused: screen.paused ?? false, mountedLeavesKey: shown.join('|') });

  useEffect(() => {
    window.__rowMotionShow = setScreen;
    setTriggerRerender(() => () => {});
    onReady();
    return () => {
      delete window.__rowMotionShow;
    };
  }, [onReady, setTriggerRerender]);

  return (
    <div className="session-terminal-desktop shelf-layout" style={{ position: 'relative', width: WIDTH, height: HEIGHT, display: 'flex' }}>
      {shown.length > 0 && (
        <div className="session-terminal-panes" ref={containerRef}>
          {shown.map((id, index) => {
            const bounds = columnBounds(index, shown.length);
            return (
              <div
                key={id}
                className="desktop-pane"
                data-pane-id={id}
                data-testid={`pane-${id}`}
                style={{ left: `${bounds.left * 100}%`, top: 0, width: `${bounds.width * 100}%`, height: '100%' }}
              >
                {id}
                <TileBody id={id} keepLeavingFrame={keepLeavingFrame} />
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
