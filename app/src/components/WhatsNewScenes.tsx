// Illustrations for the what's-new intro. Each scene plays before → after around a key press;
// the resting frame (and the reduced-motion frame) is the after state.

import type { CSSProperties, ReactNode } from 'react';
import { KeyCombos } from './Keycap';
import './WhatsNewScenes.css';

type AgentState = 'waiting' | 'busy' | 'idle';

export interface ScenePress {
  combos: string[][];
}

function Scene({ label, press, late = false, children }: {
  label: string;
  press: ScenePress | null;
  late?: boolean;
  children: ReactNode;
}) {
  return (
    <div className={`wn-scene${late ? ' wn-scene--late' : ''}`} role="img" aria-label={label}>
      <svg viewBox="0 0 520 250" aria-hidden="true">
        <rect className="wn-frame" x="8" y="8" width="504" height="234" rx="10" />
        <circle className="wn-light" cx="22" cy="18" r="3.5" />
        <circle className="wn-light" cx="34" cy="18" r="3.5" />
        <circle className="wn-light" cx="46" cy="18" r="3.5" />
        {children}
      </svg>
      {press && press.combos.length > 0 && (
        <div className="wn-press" data-testid="whats-new-scene-press">
          <KeyCombos combos={press.combos} />
        </div>
      )}
    </div>
  );
}

const Before = ({ children }: { children: ReactNode }) => <g className="wn-before">{children}</g>;
const After = ({ children }: { children: ReactNode }) => <g className="wn-after">{children}</g>;
const Flash = ({ children }: { children: ReactNode }) => <g className="wn-flash">{children}</g>;

function Sidebar({ width = 132 }: { width?: number }) {
  return <rect className="wn-sidebar" x="9" y="28" width={width} height="213" />;
}

function Row({ x = 16, y, w = 118, label, state, selected = false }: {
  x?: number;
  y: number;
  w?: number;
  label: string;
  state: AgentState;
  selected?: boolean;
}) {
  return (
    <g>
      {selected && <rect className="wn-row-selected" x={x} y={y - 11} width={w} height="18" rx="4" />}
      <circle className={`wn-dot is-${state}`} cx={x + 9} cy={y - 2} r="3.5" />
      <text className="wn-text" x={x + 19} y={y + 2}>{label}</text>
    </g>
  );
}

function Chip({ x, y, number, current = false }: { x: number; y: number; number: number; current?: boolean }) {
  return (
    <g>
      <rect className={`wn-chip${current ? ' is-current' : ''}`} x={x} y={y - 9} width="16" height="13" rx="3" />
      <text className={`wn-chip-text${current ? ' is-current' : ''}`} x={x + 8} y={y + 1} textAnchor="middle">
        {number}
      </text>
    </g>
  );
}

function DesktopRule({ y, number, current = false }: { y: number; number: number; current?: boolean }) {
  return (
    <g>
      <Chip x={16} y={y} number={number} current={current} />
      <line className="wn-rule" x1="38" y1={y - 2.5} x2="134" y2={y - 2.5} />
    </g>
  );
}

function Lines({ x, y, w, count = 3 }: { x: number; y: number; w: number; count?: number }) {
  const widths = [0.82, 0.6, 0.72, 0.45, 0.66, 0.54];
  return (
    <g>
      {Array.from({ length: count }, (_, i) => (
        <rect key={i} className="wn-line" x={x} y={y + i * 12} width={w * widths[i % widths.length]} height="4" rx="2" />
      ))}
    </g>
  );
}

function Pane({ x, y, w, h, label, state = 'busy', active = false, lines = 4, ask }: {
  x: number;
  y: number;
  w: number;
  h: number;
  label: string;
  state?: AgentState;
  active?: boolean;
  lines?: number;
  ask?: string;
}) {
  return (
    <g>
      <rect className={`wn-pane${active ? ' is-active' : ''}`} x={x} y={y} width={w} height={h} rx="6" />
      <circle className={`wn-dot is-${state}`} cx={x + 11} cy={y + 11} r="3" />
      <text className="wn-text wn-text--small" x={x + 20} y={y + 14}>{label}</text>
      <Lines x={x + 10} y={y + 28} w={w - 20} count={lines} />
      {ask && (
        <text className="wn-text wn-text--ask" x={x + 10} y={y + h - 14}>{ask}</text>
      )}
    </g>
  );
}

function Palette({ query, rows, footer }: {
  query: string;
  rows: Array<{ label: string; state?: AgentState; hint?: string }>;
  footer: string;
}) {
  const top = 50;
  return (
    <g>
      <rect className="wn-dim" x="9" y="9" width="502" height="232" rx="9" />
      <rect className="wn-popover" x="110" y={top} width="300" height="164" rx="9" />
      <text className="wn-text" x="124" y={top + 22}>{query}</text>
      <rect className="wn-caret" x={126 + query.length * 6.4} y={top + 11} width="1.5" height="14" />
      <line className="wn-rule" x1="110" y1={top + 34} x2="410" y2={top + 34} />
      {rows.map((row, i) => {
        const y = top + 56 + i * 24;
        return (
          <g key={row.label}>
            {i === 0 && <rect className="wn-row-selected" x="118" y={y - 14} width="284" height="22" rx="5" />}
            {row.state ? (
              <circle className={`wn-dot is-${row.state}`} cx="132" cy={y - 3} r="3.5" />
            ) : (
              <text className="wn-text wn-text--dim" x="127" y={y + 1}>›</text>
            )}
            <text className="wn-text" x="144" y={y + 1}>{row.label}</text>
            {row.hint && <text className="wn-text wn-text--dim" x="396" y={y + 1} textAnchor="end">{row.hint}</text>}
          </g>
        );
      })}
      <line className="wn-rule" x1="110" y1={top + 144} x2="410" y2={top + 144} />
      <text className="wn-text wn-text--dim wn-text--small" x="124" y={top + 158}>{footer}</text>
    </g>
  );
}

function DesktopOneBackdrop() {
  return (
    <g>
      <Sidebar />
      <DesktopRule y={46} number={1} current />
      <Row y={64} label="api" state="waiting" selected />
      <Row y={84} label="web" state="busy" />
      <DesktopRule y={110} number={2} />
      <Row y={128} label="docs" state="busy" />
      <Pane x={150} y={38} w={172} h={194} label="api" state="waiting" active />
      <Pane x={330} y={38} w={172} h={194} label="web" />
    </g>
  );
}

export function ProfilesScene({ press }: { press: ScenePress | null }) {
  return (
    <Scene label="Switching from the Work profile to Personal swaps every agent and desktop" press={press}>
      <Sidebar />
      <Before>
        <rect className="wn-pill" x="16" y="36" width="66" height="18" rx="9" />
        <text className="wn-text" x="26" y="49">Work ▾</text>
        <DesktopRule y={72} number={1} current />
        <Row y={90} label="api" state="waiting" selected />
        <Row y={110} label="web" state="busy" />
        <Row y={130} label="tests" state="busy" />
        <Pane x={150} y={38} w={172} h={194} label="api" state="waiting" active />
        <Pane x={330} y={38} w={172} h={194} label="web" />
      </Before>
      <After>
        <rect className="wn-pill" x="16" y="36" width="84" height="18" rx="9" />
        <text className="wn-text" x="26" y="49">Personal ▾</text>
        <DesktopRule y={72} number={1} current />
        <Row y={90} label="blog" state="busy" selected />
        <Row y={110} label="taxes" state="waiting" />
        <Pane x={150} y={38} w={352} h={194} label="blog" active lines={6} />
      </After>
      <Flash>
        <rect className="wn-popover" x="190" y="78" width="170" height="86" rx="8" />
        <text className="wn-text wn-text--dim wn-text--small" x="204" y="98">Switch profile</text>
        <text className="wn-text" x="204" y="124">Work</text>
        <rect className="wn-row-selected" x="198" y="132" width="154" height="20" rx="4" />
        <text className="wn-text" x="204" y="146">Personal</text>
      </Flash>
    </Scene>
  );
}

export function DesktopsScene({ press }: { press: ScenePress | null }) {
  return (
    <Scene label="Pressing the desktop 2 shortcut swaps the panes for desktop 2's arrangement" press={press}>
      <Sidebar />
      <Row y={64} label="api" state="waiting" />
      <Row y={84} label="web" state="busy" />
      <Row y={128} label="docs" state="busy" />
      <Row y={148} label="tests" state="busy" />
      <DesktopRule y={174} number={3} />
      <Row y={192} label="review" state="idle" />
      <Before>
        <DesktopRule y={46} number={1} current />
        <DesktopRule y={110} number={2} />
        <rect className="wn-row-selected" x="16" y="53" width="118" height="18" rx="4" />
        <Row y={64} label="api" state="waiting" />
        <Pane x={150} y={38} w={172} h={194} label="api" state="waiting" active />
        <Pane x={330} y={38} w={172} h={194} label="web" />
      </Before>
      <After>
        <DesktopRule y={46} number={1} />
        <DesktopRule y={110} number={2} current />
        <rect className="wn-row-selected" x="16" y="117" width="118" height="18" rx="4" />
        <Row y={128} label="docs" state="busy" />
        <Pane x={150} y={38} w={200} h={93} label="docs" active lines={3} />
        <Pane x={150} y={139} w={200} h={93} label="tests" lines={3} />
        <rect className="wn-tile" x="358" y="38" width="144" height="194" rx="6" />
        <text className="wn-text wn-text--small" x="368" y="52">plan.md</text>
        <Lines x={368} y={66} w={124} count={6} />
      </After>
    </Scene>
  );
}

export function ArrangeScene({ press }: { press: ScenePress | null }) {
  const drag: CSSProperties = { ['--wn-drag-x' as string]: '-180px', ['--wn-drag-y' as string]: '101px' };
  return (
    <Scene
      label="Dragging web's header docks it below api; then sending web to desktop 2 takes you there with it"
      press={press}
      late
    >
      <Sidebar />
      <Row y={64} label="api" state="waiting" />
      <g className="wn-stage1 wn-stage2">
        <DesktopRule y={46} number={1} current />
        <rect className="wn-row-selected" x="16" y="73" width="118" height="18" rx="4" />
        <Row y={84} label="web" state="busy" />
        <DesktopRule y={110} number={2} />
        <Row y={128} label="tests" state="busy" />
      </g>
      <g className="wn-stage1">
        <Pane x={150} y={38} w={172} h={194} label="api" state="waiting" />
        <Pane x={330} y={38} w={172} h={194} label="web" active />
      </g>
      <g className="wn-stage2">
        <Pane x={150} y={38} w={352} h={93} label="api" state="waiting" lines={3} />
        <Pane x={150} y={139} w={352} h={93} label="web" active lines={3} />
      </g>
      <g className="wn-stage3">
        <DesktopRule y={46} number={1} />
        <DesktopRule y={90} number={2} current />
        <Row y={108} label="tests" state="busy" />
        <rect className="wn-row-selected" x="16" y="117" width="118" height="18" rx="4" />
        <Row y={128} label="web" state="busy" />
        <Pane x={150} y={38} w={172} h={194} label="tests" />
        <Pane x={330} y={38} w={172} h={194} label="web" active />
      </g>
      <Flash>
        <rect className="wn-drop" x="150" y="139" width="352" height="93" rx="6" />
      </Flash>
      <g className="wn-drag" style={drag}>
        <rect className="wn-ghost" x="330" y="38" width="172" height="20" rx="5" />
        <circle className="wn-dot is-busy" cx="341" cy="48" r="3" />
        <text className="wn-text wn-text--small" x="350" y="51">web</text>
        <path className="wn-cursor" d="M 400 44 l 0 14 l 4 -4 l 3 6 l 2 -1 l -3 -6 l 5 0 z" />
      </g>
    </Scene>
  );
}

export function QueueScene({ press }: { press: ScenePress | null }) {
  return (
    <Scene label="Settling api takes it out of the waiting list and shows web, the next agent waiting" press={press}>
      <Sidebar />
      <text className="wn-label" x="18" y="44">WAITING</text>
      <Before>
        <Row y={62} label="api" state="waiting" selected />
        <Row y={82} label="web" state="waiting" />
        <Row y={102} label="docs" state="waiting" />
        <text className="wn-label" x="18" y="130">BUSY</text>
        <Row y={148} label="tests" state="busy" />
        <Pane x={150} y={38} w={352} h={194} label="api" state="waiting" active lines={7} ask="Opened the pull request. Anything else?" />
      </Before>
      <After>
        <Row y={62} label="web" state="waiting" selected />
        <Row y={82} label="docs" state="waiting" />
        <text className="wn-label" x="18" y="110">BUSY</text>
        <Row y={128} label="tests" state="busy" />
        <Row y={148} label="api" state="idle" />
        <Pane x={150} y={38} w={352} h={194} label="web" state="waiting" active lines={7} ask="Two tests fail on main. Fix them first?" />
      </After>
    </Scene>
  );
}

export function QueueBarScene({ press }: { press: ScenePress | null }) {
  return (
    <Scene label="Hiding the queue sidebar folds it into a bar across the top of the window" press={press}>
      <Before>
        <Sidebar />
        <text className="wn-label" x="18" y="44">WAITING</text>
        <Row y={62} label="api" state="waiting" selected />
        <Row y={82} label="web" state="waiting" />
        <Row y={102} label="docs" state="waiting" />
        <text className="wn-label" x="18" y="130">BUSY</text>
        <Row y={148} label="tests" state="busy" />
        <Chip x={16} y={226} number={1} current />
        <Chip x={36} y={226} number={2} />
        <Chip x={56} y={226} number={3} />
        <Pane x={150} y={38} w={352} h={194} label="api" state="waiting" active lines={7} />
      </Before>
      <After>
        <rect className="wn-sidebar" x="9" y="28" width="502" height="24" />
        <rect className="wn-pill" x="16" y="33" width="60" height="14" rx="7" />
        <text className="wn-text wn-text--small" x="24" y="44">Work ▾</text>
        <rect className="wn-pill" x="84" y="33" width="208" height="14" rx="7" />
        <circle className="wn-dot is-waiting" cx="94" cy="40" r="3" />
        <text className="wn-text wn-text--small" x="102" y="44">3 waiting · api › web › docs ▾</text>
        <Chip x={444} y={44} number={1} current />
        <Chip x={464} y={44} number={2} />
        <Chip x={484} y={44} number={3} />
        <Pane x={18} y={60} w={484} h={172} label="api" state="waiting" active lines={7} />
      </After>
    </Scene>
  );
}

export function AgentPaletteScene({ press }: { press: ScenePress | null }) {
  return (
    <Scene label="The agent palette filters every agent and tile as you type" press={press}>
      <DesktopOneBackdrop />
      <After>
        <Palette
          query="we"
          rows={[
            { label: 'web', state: 'waiting', hint: 'waiting · 2m' },
            { label: 'webhooks', state: 'busy', hint: 'desktop 3' },
            { label: 'weekly-report.md', hint: 'tile' },
            { label: 'web-e2e', state: 'idle', hint: 'snoozed' },
          ]}
          footer="↑↓ move · ↵ jump · type > for commands"
        />
      </After>
    </Scene>
  );
}

export function CommandPaletteScene({ press }: { press: ScenePress | null }) {
  return (
    <Scene label="The command palette lists actions such as turning on the agent queue or making a new desktop" press={press}>
      <DesktopOneBackdrop />
      <After>
        <Palette
          query=">"
          rows={[
            { label: 'Turn on the agent queue' },
            { label: 'New desktop' },
            { label: 'Switch to Personal' },
            { label: 'Open the notebook' },
          ]}
          footer="↑↓ move · ↵ run · esc closes"
        />
      </After>
    </Scene>
  );
}
