import { useLayoutEffect, useRef } from 'react';
import { applyColors, resolveColors } from '../colors/resolve';
import { useAppearance } from '../colors/useColors';
import './WhatsNewScenes.css';

type AgentState = 'waiting' | 'busy' | 'idle';

function Row({ y, label, state, selected = false }: { y: number; label: string; state: AgentState; selected?: boolean }) {
  return (
    <g>
      {selected && <rect className="wn-row-selected" x="16" y={y - 11} width="118" height="18" rx="4" />}
      <circle className={`wn-dot is-${state}`} cx="25" cy={y - 2} r="3.5" />
      <text className="wn-text" x="35" y={y + 2}>{label}</text>
    </g>
  );
}

const LINE_WIDTHS = [0.82, 0.6, 0.72, 0.45, 0.66, 0.54];

function Pane({ x, label, state, lines }: { x: number; label: string; state: AgentState; lines: number }) {
  return (
    <g>
      <rect className={`wn-pane is-${state}`} x={x} y="38" width="172" height="194" rx="6" />
      <circle className={`wn-dot is-${state}`} cx={x + 11} cy="49" r="3" />
      <text className="wn-text wn-text--small" x={x + 20} y="52">{label}</text>
      {Array.from({ length: lines }, (_, i) => (
        <rect key={i} className="wn-line" x={x + 10} y={66 + i * 12} width={152 * LINE_WIDTHS[i % LINE_WIDTHS.length]} height="4" rx="2" />
      ))}
    </g>
  );
}

function Window() {
  return (
    <svg viewBox="0 0 520 250" aria-hidden="true">
      <rect className="wn-frame" x="8" y="8" width="504" height="234" rx="10" />
      <circle className="wn-light" cx="22" cy="18" r="3.5" />
      <circle className="wn-light" cx="34" cy="18" r="3.5" />
      <circle className="wn-light" cx="46" cy="18" r="3.5" />
      <rect className="wn-sidebar" x="9" y="28" width="132" height="213" />
      <Row y={48} label="api" state="waiting" selected />
      <Row y={68} label="web" state="busy" />
      <Row y={88} label="docs" state="busy" />
      <Row y={108} label="review" state="idle" />
      <Pane x={150} label="api" state="waiting" lines={5} />
      <text className="wn-text wn-text--ask" x="160" y="196">Ship the new look?</text>
      <rect className="wn-button" x="160" y="206" width="46" height="18" rx="5" />
      <text className="wn-button-text" x="183" y="218.5" textAnchor="middle">Ship it</text>
      <Pane x={330} label="web" state="busy" lines={7} />
    </svg>
  );
}

export function NewLookScene() {
  return (
    <div className="wn-scene" role="img" aria-label="attn's window in the colors in effect: api waits for you while web and docs work">
      <Window />
    </div>
  );
}

export function NightScene() {
  const scene = useRef<HTMLDivElement>(null);
  const { appearance } = useAppearance();
  useLayoutEffect(() => {
    if (scene.current) applyColors(resolveColors('night', undefined, appearance), scene.current);
  }, [appearance]);
  return (
    <div ref={scene} className="wn-scene" role="img" aria-label="The same window in Night's colors, on indigo surfaces">
      <Window />
    </div>
  );
}
