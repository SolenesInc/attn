import FocusTrap from 'focus-trap-react';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { Seed, SeedHandoverOptions } from '../hooks/useDaemonSocket';
import {
  useGardenFullscreenView,
  type GardenMode,
  type GardenOpenMode,
} from '../hooks/useGardenPresentation';
import { GardenBoard } from './GardenBoard';
import type { Verb } from './gardenBoardModel';
import { GardenPanel } from './GardenPanel';
import type { SeedDocument } from './SeedDocumentView';
import './GardenFrame.css';

export type { GardenMode } from '../hooks/useGardenPresentation';

export interface FrameRect {
  top: number;
  left: number;
  width: number;
  height: number;
}

const FULL_INSET = 12;
const EXPAND_MS = 180;
const COLLAPSE_MS = 150;

function reduceMotion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

export function useDockSlotRect() {
  const ref = useRef<HTMLDivElement | null>(null);
  const [rect, setRect] = useState<FrameRect | null>(null);

  useLayoutEffect(() => {
    const measure = () => {
      const slot = ref.current;
      if (!slot) return;
      const box = slot.getBoundingClientRect();
      const next: FrameRect = { top: box.top, left: box.left, width: box.width, height: box.height };
      setRect((prev) => (
        prev && prev.top === next.top && prev.left === next.left
          && prev.width === next.width && prev.height === next.height
          ? prev
          : next
      ));
    };
    measure();
    const observer = new ResizeObserver(measure);
    if (ref.current) observer.observe(ref.current);
    window.addEventListener('resize', measure);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', measure);
    };
  }, []);

  return [ref, rect] as const;
}

export interface GardenFrameProps {
  mode: GardenMode;
  dockRect: FrameRect | null;
  onToggleFrame: () => void;
  onEscapeFloor: () => void;
  onClose: () => void;
  seeds: Seed[];
  seedsTotal: number;
  fetchSeedDocument?: (seedId: string) => Promise<SeedDocument>;
  onOpenAsTile?: (seedId: string) => void;
  onOpenMarkdownArtifact?: (path: string) => void;
  checkArtifactPath?: (path: string) => Promise<boolean>;
  onResumeSeed?: (seedId: string) => void;
  onHandoverSeed?: (options: Omit<SeedHandoverOptions, 'sourceSessionId'>) => Promise<unknown>;
  liveSessions?: Set<string>;
  loaded?: boolean;
  moveSeed?: (seedId: string, verb: Verb, reason?: string) => Promise<unknown>;
  noteSeed?: (seedId: string, body: string) => Promise<unknown>;
}

export function GardenFrame({
  mode,
  dockRect,
  onToggleFrame,
  onEscapeFloor,
  onClose,
  seeds,
  seedsTotal,
  fetchSeedDocument,
  onOpenAsTile,
  onOpenMarkdownArtifact,
  checkArtifactPath,
  onResumeSeed,
  onHandoverSeed,
  liveSessions,
  loaded = true,
  moveSeed,
  noteSeed,
}: GardenFrameProps) {
  const [preferredView, chooseView] = useGardenFullscreenView();
  const view = mode === 'full' ? preferredView : 'list';
  const frameRef = useRef<HTMLDivElement | null>(null);
  const [viewport, setViewport] = useState(() => ({ w: window.innerWidth, h: window.innerHeight }));
  const [flying, setFlying] = useState(false);
  const previousMode = useRef<GardenMode>(mode);
  const lastOpenMode = useRef<GardenOpenMode>(mode === 'full' ? 'full' : 'dock');

  useLayoutEffect(() => {
    if (mode !== 'closed') lastOpenMode.current = mode;
  }, [mode]);

  useEffect(() => {
    if (mode !== 'full') return;
    const onResize = () => setViewport({ w: window.innerWidth, h: window.innerHeight });
    onResize();
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, [mode]);

  const showingBoard = mode === 'full' && view === 'board' && Boolean(moveSeed && noteSeed);

  useLayoutEffect(() => {
    const was = previousMode.current;
    previousMode.current = mode;
    const promotion = (was === 'dock' && mode === 'full') || (was === 'full' && mode === 'dock');
    if (!promotion) return;
    const root = frameRef.current;
    if (!root) return;
    if (mode === 'dock' && !root.contains(document.activeElement)) {
      (root.querySelector('.garden-frame__body') as HTMLElement | null)?.focus();
    }
    if (reduceMotion()) return;
    setFlying(true);
    const duration = mode === 'full' ? EXPAND_MS : COLLAPSE_MS;
    const done = window.setTimeout(() => setFlying(false), duration + 40);
    return () => window.clearTimeout(done);
  }, [mode]);

  const full: FrameRect = {
    top: FULL_INSET,
    left: FULL_INSET,
    width: Math.max(0, viewport.w - FULL_INSET * 2),
    height: Math.max(0, viewport.h - FULL_INSET * 2),
  };
  const dismissingFullscreen = mode === 'closed' && lastOpenMode.current === 'full';
  const rect = mode === 'full' || dismissingFullscreen ? full : dockRect;
  if (!rect) return null;

  const open = mode !== 'closed';
  const boardable = mode === 'full' && Boolean(moveSeed && noteSeed);
  const viewToggle = boardable ? (
    <div className="garden-view-switch" role="group" aria-label="Garden view">
      <button type="button" aria-pressed={view === 'list'} onClick={() => chooseView('list')}>
        list
      </button>
      <button type="button" aria-pressed={view === 'board'} onClick={() => chooseView('board')}>
        board
      </button>
    </div>
  ) : undefined;
  return (
    <>
      <div className={`garden-frame-backdrop${mode === 'full' ? ' is-visible' : ''}`} aria-hidden="true" />
      <div
        ref={frameRef}
        className={`garden-frame is-${mode}${dismissingFullscreen ? ' is-full-dismissal' : ''}${flying ? ' is-flying' : ''}`}
        style={{ top: rect.top, left: rect.left, width: rect.width, height: rect.height }}
        role={mode === 'full' ? 'dialog' : undefined}
        aria-modal={mode === 'full' ? true : undefined}
        aria-label="The garden"
        aria-hidden={!open}
        inert={!open || undefined}
        data-testid="garden-frame"
      >
        <FocusTrap
          active={mode === 'full'}
          focusTrapOptions={{
            escapeDeactivates: false,
            clickOutsideDeactivates: false,
            initialFocus: false,
            fallbackFocus: '.garden-frame__body',
            returnFocusOnDeactivate: false,
          }}
        >
          <div className="garden-frame__body" tabIndex={-1}>
            {showingBoard ? (
              <GardenBoard
                seeds={seeds}
                seedsTotal={seedsTotal}
                liveSessions={liveSessions ?? new Set()}
                loaded={loaded}
                onTransition={moveSeed!}
                onNote={noteSeed!}
                viewToggle={viewToggle}
                onClose={onClose}
                onEscapeFloor={onEscapeFloor}
              />
            ) : (
              <GardenPanel
                isOpen={open}
                seeds={seeds}
                seedsTotal={seedsTotal}
                fetchSeedDocument={fetchSeedDocument}
                onOpenAsTile={onOpenAsTile}
                onOpenMarkdownArtifact={onOpenMarkdownArtifact}
                checkArtifactPath={checkArtifactPath}
                onResumeSeed={onResumeSeed}
                onHandoverSeed={onHandoverSeed}
                onClose={onClose}
                viewToggle={viewToggle}
                frame={mode === 'full' ? 'full' : 'dock'}
                onToggleFrame={onToggleFrame}
                onEscapeFloor={onEscapeFloor}
              />
            )}
          </div>
        </FocusTrap>
      </div>
    </>
  );
}
