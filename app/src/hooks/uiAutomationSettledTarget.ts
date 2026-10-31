import { getCurrentWindow } from '@tauri-apps/api/window';
import { waitForAutomationDom } from './uiAutomationDom';
import { nextAnimationFrame } from './uiAutomationSettle';

// The app's longest finite animation is 1400ms (SeedStateIcon.css); only a stuck target reaches this.
export const SETTLE_LIMIT_MS = 5_000;

interface BoxPoint {
  x: number;
  y: number;
}

export interface NativeTarget {
  selector: string;
  at: BoxPoint;
  offset: BoxPoint;
}

function boxPoint(value: unknown, fallback: BoxPoint, field: string): BoxPoint {
  if (value === undefined) return fallback;
  const point = value as Partial<BoxPoint> | null;
  if (typeof point?.x !== 'number' || typeof point.y !== 'number') {
    throw new Error(`dom_native_point ${field} needs numeric x and y, got ${JSON.stringify(value)}`);
  }
  return { x: point.x, y: point.y };
}

export function parseNativeTarget(payload: Record<string, unknown>): NativeTarget {
  if (typeof payload.selector !== 'string' || !payload.selector) {
    throw new Error('dom_native_point requires selector');
  }
  return {
    selector: payload.selector,
    at: boxPoint(payload.at, { x: 0.5, y: 0.5 }, 'at'),
    offset: boxPoint(payload.offset, { x: 0, y: 0 }, 'offset'),
  };
}

function findTarget(selector: string): HTMLElement {
  const element = document.querySelector(selector);
  if (!(element instanceof HTMLElement)) {
    throw new Error(`dom_native_point selector not found in DOM: ${selector}`);
  }
  return element;
}

function runningAnimationsOn(element: Element): Animation[] {
  const animations = element.getAnimations({ subtree: true });
  for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
    animations.push(...ancestor.getAnimations());
  }
  return animations.filter((animation) => animation.playState === 'running');
}

function endsInFiniteTime(animation: Animation): boolean {
  return Number.isFinite(animation.effect?.getComputedTiming().endTime);
}

function describeAnimation(animation: Animation) {
  const target = animation.effect instanceof KeyframeEffect ? animation.effect.target : null;
  return {
    name: animation instanceof CSSTransition ? `transition ${animation.transitionProperty}`
      : animation instanceof CSSAnimation ? `animation ${animation.animationName}`
        : animation.id || 'script animation',
    target: target ? describeElement(target) : null,
    currentTime: animation.currentTime,
    endTime: animation.effect?.getComputedTiming().endTime ?? null,
  };
}

function stillMovingError(selector: string, animations: Animation[], detail: string) {
  return new Error(`${selector} did not settle within the ${SETTLE_LIMIT_MS}ms limit: ${detail}; running animations: ${JSON.stringify(animations.map(describeAnimation))}`);
}

async function untilFinished(animations: Animation[], deadline: number, selector: string) {
  let timer = 0;
  const expired = new Promise<'expired'>((resolve) => {
    timer = window.setTimeout(() => resolve('expired'), Math.max(0, deadline - performance.now()));
  });
  const outcome = await Promise.race([
    Promise.all(animations.map((animation) => animation.finished.catch(() => undefined))),
    expired,
  ]);
  window.clearTimeout(timer);
  if (outcome === 'expired') {
    throw stillMovingError(selector, animations.filter((animation) => animation.playState === 'running'), 'its finite animations were still running');
  }
}

export async function waitForFiniteAnimations(selector: string, animations: Animation[]) {
  const finite = animations.filter((animation) => animation.playState === 'running' && endsInFiniteTime(animation));
  await untilFinished(finite, performance.now() + SETTLE_LIMIT_MS, selector);
  return finite.length;
}

function sameBox(a: DOMRect, b: DOMRect) {
  return a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;
}

async function settledBox(target: NativeTarget, deadline: number) {
  let animationsWaited = 0;
  let framesWaited = 0;
  for (;;) {
    const element = findTarget(target.selector);
    const moving = runningAnimationsOn(element).filter(endsInFiniteTime);
    if (moving.length > 0) {
      animationsWaited += moving.length;
      await untilFinished(moving, deadline, target.selector);
      continue;
    }
    const before = element.getBoundingClientRect();
    await nextAnimationFrame();
    await nextAnimationFrame();
    framesWaited += 2;
    const after = element.getBoundingClientRect();
    if (element.isConnected && sameBox(before, after) && !runningAnimationsOn(element).some(endsInFiniteTime)) {
      return { element, box: after, animationsWaited, framesWaited };
    }
    if (performance.now() >= deadline) {
      throw stillMovingError(target.selector, runningAnimationsOn(element), `its box moved from ${JSON.stringify(before)} to ${JSON.stringify(after)} between frames`);
    }
  }
}

function describeElement(element: Element) {
  const testId = element.getAttribute('data-testid');
  const label = element.getAttribute('aria-label');
  const classes = typeof element.className === 'string' && element.className.trim() ? `.${element.className.trim().split(/\s+/).join('.')}` : '';
  return `${element.tagName.toLowerCase()}${classes}${testId ? `[data-testid="${testId}"]` : ''}${label ? `[aria-label="${label}"]` : ''}`;
}

function pointIn(box: DOMRect, target: NativeTarget) {
  return {
    x: box.x + box.width * target.at.x + target.offset.x,
    y: box.y + box.height * target.at.y + target.offset.y,
  };
}

function whyUnreachable(element: HTMLElement, box: DOMRect, point: { x: number; y: number }): string | null {
  if (box.width === 0 || box.height === 0) {
    return `it has an empty ${box.width}x${box.height} box; it is hidden or not laid out`;
  }
  if (point.x < 0 || point.y < 0 || point.x >= window.innerWidth || point.y >= window.innerHeight) {
    return `its point (${point.x}, ${point.y}) is outside the ${window.innerWidth}x${window.innerHeight} viewport`;
  }
  const insideBox = point.x >= box.left && point.x < box.right && point.y >= box.top && point.y < box.bottom;
  const hit = insideBox ? document.elementFromPoint(point.x, point.y) : null;
  if (insideBox && !(hit && element.contains(hit))) {
    return `its point (${point.x}, ${point.y}) is covered by ${hit ? describeElement(hit) : 'nothing in the page'}`;
  }
  return null;
}

async function reachableBox(target: NativeTarget, deadline: number) {
  const first = await settledBox(target, deadline);
  let settled = first;
  let reason = whyUnreachable(first.element, first.box, pointIn(first.box, target));
  if (reason !== null && first.box.width > 0 && first.box.height > 0) {
    first.element.scrollIntoView({ block: 'center', inline: 'center', behavior: 'auto' });
    settled = await settledBox(target, deadline);
    reason = whyUnreachable(settled.element, settled.box, pointIn(settled.box, target));
  }
  if (reason !== null) {
    throw new Error(`dom_native_point cannot reach ${target.selector}: ${reason}`);
  }
  return {
    box: settled.box,
    scrolled: settled !== first,
    animationsWaited: first.animationsWaited + (settled === first ? 0 : settled.animationsWaited),
    framesWaited: first.framesWaited + (settled === first ? 0 : settled.framesWaited),
  };
}

export async function settledNativePoint(target: NativeTarget) {
  const deadline = performance.now() + SETTLE_LIMIT_MS;
  const appWindow = getCurrentWindow();
  const [scaleFactor, outerSize] = await Promise.all([appWindow.scaleFactor(), appWindow.outerSize()]);
  const frame = outerSize.toLogical(scaleFactor);
  await waitForAutomationDom({ selector: target.selector, timeoutMs: SETTLE_LIMIT_MS }).catch(() => {
    throw new Error(`dom_native_point ${target.selector} did not appear within the ${SETTLE_LIMIT_MS}ms limit`);
  });
  const { box, scrolled, animationsWaited, framesWaited } = await reachableBox(target, deadline);
  const point = pointIn(box, target);
  return {
    x: (Math.max(0, frame.width - window.innerWidth) / 2 + point.x) / frame.width,
    y: (Math.max(0, frame.height - window.innerHeight) + point.y) / frame.height,
    window: { width: frame.width, height: frame.height },
    bounds: { x: box.x, y: box.y, width: box.width, height: box.height },
    scrolled,
    animationsWaited,
    framesWaited,
  };
}
