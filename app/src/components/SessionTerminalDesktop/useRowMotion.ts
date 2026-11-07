import { useCallback, useEffect, useLayoutEffect, useRef, type RefObject } from 'react';
import type { NormalizedPaneBounds } from '../../types/desktop';
import { freeStillFrame } from '../../utils/stillFrame';
import { isDomInstance } from '../../utils/realm';

const STIFFNESS = 630;
const DAMPING = 39;
const MOVE_MS = 520;
const FADE_MS = 120;
const EXIT_MS = 110;
const ENTER_SCALE = 0.9;
const EXIT_SCALE = 0.92;
const KEYFRAME_INTERVALS = 40;
const SMALLEST_VISIBLE_MOVE_PX = 1;
const ENTERING_Z_INDEX = '3';

export interface RowMotionLayout {
  bounds: ReadonlyMap<string, NormalizedPaneBounds>;
  rowKey: string;
  firstColumnIndex: number;
  widthPx: number;
  heightPx: number;
}

interface Move {
  animation: Animation;
  dx: number;
  dy: number;
  element: HTMLElement;
}

interface MotionState {
  last: RowMotionLayout | null;
  lastPaused: boolean;
  elements: Map<string, HTMLElement>;
  moves: Map<string, Move>;
  held: Map<string, { element: HTMLElement; animation: Animation }>;
  awaitingElement: Set<string>;
  lastFrames: Map<string, HTMLCanvasElement>;
}

function springProgress(ms: number): number {
  const t = ms / 1000;
  const undamped = Math.sqrt(STIFFNESS);
  const ratio = DAMPING / (2 * undamped);
  const damped = undamped * Math.sqrt(1 - ratio * ratio);
  const decay = Math.exp(-ratio * undamped * t);
  return 1 - decay * (Math.cos(damped * t) + ((ratio * undamped) / damped) * Math.sin(damped * t));
}

const SPRING_SAMPLES = Array.from({ length: KEYFRAME_INTERVALS + 1 }, (_, index) =>
  index === KEYFRAME_INTERVALS ? 1 : springProgress((index / KEYFRAME_INTERVALS) * MOVE_MS));

function springKeyframes(transformAt: (progress: number) => string): Keyframe[] {
  return SPRING_SAMPLES.map((progress) => ({ transform: transformAt(progress) }));
}

export function prefersReducedMotion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

function isBrowserTile(element: HTMLElement): boolean {
  return element.dataset.tileKind === 'browser';
}

function sameBounds(a: ReadonlyMap<string, NormalizedPaneBounds>, b: ReadonlyMap<string, NormalizedPaneBounds>): boolean {
  if (a === b) return true;
  if (a.size !== b.size) return false;
  for (const [id, bounds] of a) {
    const other = b.get(id);
    if (!other || other.left !== bounds.left || other.top !== bounds.top || other.width !== bounds.width || other.height !== bounds.height) return false;
  }
  return true;
}

function rowSlidePx(before: RowMotionLayout, after: RowMotionLayout): number {
  if (before.rowKey !== after.rowKey) return 0;
  for (const [id, bounds] of after.bounds) {
    const earlier = before.bounds.get(id);
    if (earlier) return (bounds.left - earlier.left) * after.widthPx;
  }
  return Math.sign(before.firstColumnIndex - after.firstColumnIndex) * after.widthPx;
}

function remainingOffset(move: Move): { x: number; y: number } {
  const elapsed = Number(move.animation.currentTime ?? MOVE_MS);
  const left = elapsed >= MOVE_MS ? 0 : 1 - springProgress(elapsed);
  return { x: move.dx * left, y: move.dy * left };
}

function cancelMove(state: MotionState, leafId: string): void {
  state.moves.get(leafId)?.animation.cancel();
}

function startMove(state: MotionState, leafId: string, element: HTMLElement, dx: number, dy: number): void {
  const animation = element.animate(
    springKeyframes((progress) => `translate(${dx * (1 - progress)}px, ${dy * (1 - progress)}px)`),
    { duration: MOVE_MS },
  );
  const move = { animation, dx, dy, element };
  state.moves.set(leafId, move);
  const forget = () => {
    if (state.moves.get(leafId) === move) state.moves.delete(leafId);
  };
  animation.onfinish = forget;
  animation.oncancel = forget;
}

function startEntering(state: MotionState, leafId: string, element: HTMLElement): void {
  cancelMove(state, leafId);
  element.style.zIndex = ENTERING_Z_INDEX;
  const animation = element.animate(
    springKeyframes((progress) => `scale(${ENTER_SCALE + (1 - ENTER_SCALE) * progress})`),
    { duration: MOVE_MS },
  );
  const move = { animation, dx: 0, dy: 0, element };
  state.moves.set(leafId, move);
  const settle = () => {
    element.style.zIndex = '';
    if (state.moves.get(leafId) === move) state.moves.delete(leafId);
  };
  animation.onfinish = settle;
  animation.oncancel = settle;
  element.animate([{ opacity: 0 }, { opacity: 1 }], { duration: FADE_MS, easing: 'ease-out' });
}

function detachFromTheLayout(element: HTMLElement): void {
  element.removeAttribute('data-pane-id');
  element.removeAttribute('data-pane-session-id');
  element.classList.remove('leaf-arrival');
  element.inert = true;
  element.setAttribute('aria-hidden', 'true');
}

function dropFrame(state: MotionState, leafId: string): void {
  const frame = state.lastFrames.get(leafId);
  if (frame) freeStillFrame(frame);
  state.lastFrames.delete(leafId);
}

function holdLeaving(state: MotionState, container: HTMLElement, leafId: string, slidePx: number): void {
  const element = state.elements.get(leafId);
  if (!element || element.isConnected || isBrowserTile(element)) {
    dropFrame(state, leafId);
    return;
  }
  const lastFrame = state.lastFrames.get(leafId);
  state.lastFrames.delete(leafId);
  detachFromTheLayout(element);
  if (lastFrame) element.querySelector('.desktop-pane-body')?.replaceChildren(lastFrame);
  container.appendChild(element);
  const running = state.moves.get(leafId);
  const drawnAt = running ? remainingOffset(running) : { x: 0, y: 0 };
  running?.animation.cancel();
  const from = `translate(${drawnAt.x}px, ${drawnAt.y}px)`;
  const animation = slidePx
    ? element.animate(springKeyframes((progress) => `${from} translateX(${slidePx * progress}px)`), { duration: MOVE_MS })
    : element.animate(
        [{ opacity: 1, transform: `${from} scale(1)` }, { opacity: 0, transform: `${from} scale(${EXIT_SCALE})` }],
        { duration: EXIT_MS, easing: 'ease-out' },
      );
  const held = { element, animation };
  state.held.set(leafId, held);
  const release = () => {
    element.remove();
    if (lastFrame) freeStillFrame(lastFrame);
    if (state.held.get(leafId) === held) state.held.delete(leafId);
  };
  animation.onfinish = release;
  animation.oncancel = release;
}

function cancelAll(state: MotionState): void {
  for (const move of state.moves.values()) move.animation.cancel();
  for (const held of state.held.values()) held.animation.cancel();
  state.awaitingElement.clear();
  for (const leafId of state.lastFrames.keys()) dropFrame(state, leafId);
  state.elements.clear();
}

function emptyRowAfter(layout: RowMotionLayout): RowMotionLayout {
  return { ...layout, bounds: new Map(), rowKey: '' };
}

function animateChange(state: MotionState, container: HTMLElement, before: RowMotionLayout, after: RowMotionLayout): void {
  const slidePx = rowSlidePx(before, after);
  const paneElement = (leafId: string) => {
    for (const child of container.children) {
      if (isDomInstance(child, 'HTMLElement') && child.dataset.paneId === leafId) return child;
    }
    return null;
  };
  for (const leafId of before.bounds.keys()) {
    if (!after.bounds.has(leafId)) holdLeaving(state, container, leafId, slidePx);
  }
  for (const [leafId, bounds] of after.bounds) {
    state.held.get(leafId)?.animation.cancel();
    const element = paneElement(leafId);
    const earlier = before.bounds.get(leafId);
    if (!earlier) {
      if (!element) state.awaitingElement.add(leafId);
      else if (isBrowserTile(element)) continue;
      else if (slidePx) startMove(state, leafId, element, -slidePx, 0);
      else startEntering(state, leafId, element);
      continue;
    }
    if (!element || isBrowserTile(element)) continue;
    const running = state.moves.get(leafId);
    const remaining = running ? remainingOffset(running) : { x: 0, y: 0 };
    const dx = earlier.left * before.widthPx + remaining.x - bounds.left * after.widthPx;
    const dy = earlier.top * before.heightPx + remaining.y - bounds.top * after.heightPx;
    cancelMove(state, leafId);
    if (Math.abs(dx) >= SMALLEST_VISIBLE_MOVE_PX || Math.abs(dy) >= SMALLEST_VISIBLE_MOVE_PX) startMove(state, leafId, element, dx, dy);
  }
}

function startAwaitedEntrances(state: MotionState, layout: RowMotionLayout, container: HTMLElement): void {
  for (const leafId of state.awaitingElement) {
    if (!layout.bounds.has(leafId)) {
      state.awaitingElement.delete(leafId);
      continue;
    }
    for (const child of container.children) {
      if (isDomInstance(child, 'HTMLElement') && child.dataset.paneId === leafId) {
        state.awaitingElement.delete(leafId);
        if (!isBrowserTile(child)) startEntering(state, leafId, child);
        break;
      }
    }
  }
}

function rememberElements(state: MotionState, container: HTMLElement): void {
  state.elements.clear();
  for (const child of container.children) {
    if (isDomInstance(child, 'HTMLElement') && child.dataset.paneId) state.elements.set(child.dataset.paneId, child);
  }
}

export function useRowMotion(input: {
  containerRef: RefObject<HTMLDivElement | null>;
  layout: RowMotionLayout | null;
  paused: boolean;
  mountedLeavesKey: string;
}): { keepLeavingFrame: (leafId: string, frame: HTMLCanvasElement | null) => void } {
  const { containerRef, layout, paused, mountedLeavesKey } = input;
  const stateRef = useRef<MotionState>({
    last: null,
    lastPaused: true,
    elements: new Map(),
    moves: new Map(),
    held: new Map(),
    awaitingElement: new Set(),
    lastFrames: new Map(),
  });

  useLayoutEffect(() => {
    const state = stateRef.current;
    const container = containerRef.current;
    if (!layout || !container) {
      cancelAll(state);
      state.last = state.last && !container ? emptyRowAfter(state.last) : null;
      state.lastPaused = paused;
      return;
    }
    const before = state.last;
    const resuming = state.lastPaused;
    state.last = layout;
    state.lastPaused = paused;
    if (paused || resuming || prefersReducedMotion() || (before && before.widthPx !== layout.widthPx)) {
      cancelAll(state);
    } else {
      if (before && !sameBounds(before.bounds, layout.bounds)) animateChange(state, container, before, layout);
      startAwaitedEntrances(state, layout, container);
    }
    rememberElements(state, container);
  }, [containerRef, layout, mountedLeavesKey, paused]);

  useEffect(() => {
    const state = stateRef.current;
    return () => cancelAll(state);
  }, []);

  const keepLeavingFrame = useCallback((leafId: string, frame: HTMLCanvasElement | null) => {
    dropFrame(stateRef.current, leafId);
    if (!frame) return;
    if (prefersReducedMotion()) freeStillFrame(frame);
    else stateRef.current.lastFrames.set(leafId, frame);
  }, []);
  return { keepLeavingFrame };
}
