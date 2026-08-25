import { useCallback, useEffect, useRef, useState, type CSSProperties, type RefObject } from 'react';
import './CrewWake.css';

export type WakePhase = 'rest' | 'armed' | 'breaking';

// A tripwire, not a deadline: a confirming second click takes a few hundred milliseconds,
// so four seconds is about ten times the gesture it must never interrupt.
export const WAKE_ARM_TIMEOUT_MS = 4000;

const WAKE_BREAK_MS = 620;

interface WakeConfirm {
  phase: WakePhase;
  trigger: () => void;
  rowRef: RefObject<HTMLDivElement | null>;
}

export function useWakeConfirm(onWake: (() => void) | undefined): WakeConfirm {
  const [phase, setPhase] = useState<WakePhase>('rest');
  const rowRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (phase !== 'armed') return;
    const disarm = () => setPhase('rest');
    const onPointerDown = (event: PointerEvent) => {
      if (rowRef.current?.contains(event.target as Node)) return;
      disarm();
    };
    const onFocusIn = (event: FocusEvent) => {
      if (rowRef.current?.contains(event.target as Node)) return;
      disarm();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') disarm();
    };
    const timer = window.setTimeout(disarm, WAKE_ARM_TIMEOUT_MS);
    document.addEventListener('pointerdown', onPointerDown, true);
    document.addEventListener('focusin', onFocusIn, true);
    document.addEventListener('keydown', onKeyDown, true);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener('pointerdown', onPointerDown, true);
      document.removeEventListener('focusin', onFocusIn, true);
      document.removeEventListener('keydown', onKeyDown, true);
    };
  }, [phase]);

  useEffect(() => {
    if (phase !== 'breaking') return;
    const timer = window.setTimeout(() => setPhase('rest'), WAKE_BREAK_MS);
    return () => window.clearTimeout(timer);
  }, [phase]);

  // Reads `phase` rather than an updater: StrictMode runs an updater twice, which would
  // wake the member twice.
  const trigger = useCallback(() => {
    if (!onWake) return;
    // The flare is not a target: a click landing in it would re-arm a row whose wake is already sent.
    if (phase === 'breaking') return;
    if (phase !== 'armed') {
      setPhase('armed');
      return;
    }
    setPhase('breaking');
    onWake();
  }, [onWake, phase]);

  return { phase, trigger, rowRef };
}

const RAY_ANGLES = [-72, -36, 0, 36, 72];
const HORIZON_Y = 13;
const RAY_INNER = 5;
const RAY_OUTER = 7.6;

function rayPath(angle: number): string {
  const radians = (angle * Math.PI) / 180;
  const point = (radius: number) => [
    (10 + radius * Math.sin(radians)).toFixed(2),
    (HORIZON_Y - radius * Math.cos(radians)).toFixed(2),
  ];
  const [x1, y1] = point(RAY_INNER);
  const [x2, y2] = point(RAY_OUTER);
  return `M${x1} ${y1}L${x2} ${y2}`;
}

// Arming animates to a held frame and stops there: a loop that never stops repainting is
// a battery bug on a machine that runs attn all day.
export function CrewWakeSun({ phase }: { phase: WakePhase }) {
  const lit = phase !== 'rest';
  return (
    <svg
      className={`crew-sun crew-sun--${phase}`}
      viewBox="0 0 20 20"
      aria-hidden="true"
      focusable="false"
    >
      {/* Drawn outward from directly beneath the sun, so the ground widens with
          the light rather than being there all along. */}
      <path className="crew-sun-horizon" d={`M10 ${HORIZON_Y}H3`} pathLength={1} />
      <path className="crew-sun-horizon" d={`M10 ${HORIZON_Y}H17`} pathLength={1} />
      {lit && <circle className="crew-sun-glow" cx="10" cy="8.8" r="6.4" />}
      {phase === 'breaking' && <circle className="crew-sun-shock" cx="10" cy="8.8" r="3.4" />}
      <g className="crew-sun-body">
        {RAY_ANGLES.map((angle, index) => (
          <path
            key={angle}
            className="crew-sun-ray"
            style={{ '--ray': Math.abs(index - 2) } as CSSProperties}
            d={rayPath(angle)}
            pathLength={1}
          />
        ))}
        <circle className="crew-sun-disc" cx="10" cy={HORIZON_Y} r="3.4" />
      </g>
    </svg>
  );
}
