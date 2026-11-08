import { recordDiag, supportTerminalDiagnosticsSnapshot } from './terminalDiagnosticsLog';
import { isDomInstance } from './realm';
import { ROW_PANES } from '../components/SessionTerminalDesktop/rowPanes';

export interface MotionTraceStop {
  frames: number;
  firstPaints: Record<string, number>;
  firstContentPaints: Record<string, number>;
}

let pauseAfterMs: number | null = null;
let firstMotionAt: number | null = null;

let running: { id: string; startedAt: number; frames: number; handle: number } | null = null;

function stillFrameInk(pane: HTMLElement): number | null {
  const still = pane.querySelector<HTMLCanvasElement>('.desktop-pane-body > canvas');
  const context = still?.getContext('2d', { willReadFrequently: true });
  if (!still || !context || still.width === 0 || still.height === 0) return null;
  const { data } = context.getImageData(0, 0, still.width, Math.min(still.height, 400));
  const colors = new Set<number>();
  for (let index = 0; index < data.length; index += 64) colors.add((data[index] << 16) | (data[index + 1] << 8) | data[index + 2]);
  return colors.size;
}

function samplePanes() {
  return Array.from(document.querySelectorAll<HTMLElement>(ROW_PANES)).map((pane) => {
    const rect = pane.getBoundingClientRect();
    const style = getComputedStyle(pane);
    return {
      id: pane.dataset.paneId ?? null,
      x: rect.left,
      y: rect.top,
      width: rect.width,
      height: rect.height,
      boxLeft: pane.offsetLeft,
      boxWidth: pane.offsetWidth,
      transform: style.transform,
      opacity: style.opacity,
      willChange: style.willChange,
      stillInk: pane.dataset.paneId ? undefined : stillFrameInk(pane),
      stillWidth: pane.dataset.paneId ? undefined : pane.querySelector('.desktop-pane-body > canvas')?.clientWidth,
    };
  });
}

function keyframeProperties(effect: KeyframeEffect): string[] {
  return effect.getKeyframes().flatMap((frame) =>
    Object.keys(frame).filter((key) => !['offset', 'computedOffset', 'easing', 'composite'].includes(key)));
}

function sampleAnimations() {
  const animated = new Set<string>();
  const decoration = new Set<string>();
  for (const animation of document.getAnimations()) {
    const effect = animation.effect;
    if (!isDomInstance(effect, 'KeyframeEffect') || !effect.target?.closest('.session-terminal-panes')) continue;
    const isPaneMotion = effect.target.matches('.desktop-pane')
      && !effect.pseudoElement
      && !isDomInstance(animation, 'CSSAnimation')
      && !isDomInstance(animation, 'CSSTransition');
    for (const property of keyframeProperties(effect)) {
      if (isPaneMotion) animated.add(property);
      else decoration.add(`${effect.target.className}${effect.pseudoElement ?? ''}:${property}`);
    }
  }
  return { animated: [...animated].sort(), decoration: [...decoration].sort() };
}

function panesAnimations(): Animation[] {
  return document.getAnimations().filter((animation) => {
    const effect = animation.effect;
    return isDomInstance(effect, 'KeyframeEffect') && isDomInstance(effect.target, 'Element') && effect.target.matches(ROW_PANES);
  });
}

export function resumeMotion(): void {
  pauseAfterMs = null;
  firstMotionAt = null;
  for (const animation of panesAnimations()) animation.play();
}

export function startMotionTrace(id: string, pauseMotionAfterMs?: number): void {
  if (running) cancelAnimationFrame(running.handle);
  pauseAfterMs = pauseMotionAfterMs ?? null;
  firstMotionAt = null;
  const trace = { id, startedAt: Date.now(), frames: 0, handle: 0 };
  running = trace;
  const sample = () => {
    if (running !== trace) return;
    trace.frames += 1;
    if (pauseAfterMs !== null) {
      const motion = panesAnimations().filter((animation) => animation.playState === 'running');
      if (motion.length > 0) {
        firstMotionAt ??= Date.now();
        if (Date.now() - firstMotionAt >= pauseAfterMs) {
          for (const animation of motion) animation.pause();
          pauseAfterMs = null;
        }
      }
    }
    recordDiag({ kind: 'motion_frame', trace: id, panes: samplePanes(), ...sampleAnimations() });
    trace.handle = requestAnimationFrame(sample);
  };
  trace.handle = requestAnimationFrame(sample);
}

export function stopMotionTrace(): MotionTraceStop {
  if (!running) return { frames: 0, firstPaints: {}, firstContentPaints: {} };
  const trace = running;
  running = null;
  cancelAnimationFrame(trace.handle);
  const firstPaints: Record<string, number> = {};
  const firstContentPaints: Record<string, number> = {};
  for (const event of supportTerminalDiagnosticsSnapshot().events) {
    if (event.kind !== 'paint' || event.at < trace.startedAt || !event.pane) continue;
    if (event.quads === null) continue;
    firstPaints[event.pane] ??= event.at;
    if (Number(event.quads) > 0 && Number(event.modelPrintable) > 0) firstContentPaints[event.pane] ??= event.at;
  }
  return { frames: trace.frames, firstPaints, firstContentPaints };
}
