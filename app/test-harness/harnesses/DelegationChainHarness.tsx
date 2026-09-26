import { useEffect, useRef, useState } from 'react';
import { DelegationChainProvider, DelegationChainTrigger, type ChainSession, type DelegationChainHandle } from '../../src/components/DelegationChain';
import { UnifiedPalette } from '../../src/components/palette/UnifiedPalette';
import FocusTrap from 'focus-trap-react';
import type { AgentPaletteInput, PaletteSession } from '../../src/components/palette/agentPaletteRows';
import { useEscapeStack } from '../../src/hooks/useEscapeStack';
import { SessionLabel } from '../../src/components/SessionLabel';
import { BuiltinDelegationRole } from '../../src/types/generated';
import { useShortcut } from '../../src/shortcuts/useShortcut';
import type { HarnessProps } from '../types';
import '../../src/App.css';
import '../../src/components/Sidebar.css';

const agents: ChainSession[] = [
  { id: 'root', label: 'Coordinate role identity', agent: 'claude', state: 'idle', delegation_role: { name: 'Orchestrator', builtin: BuiltinDelegationRole.Orchestrator } },
  { id: 'builder', label: 'Build chain navigator', agent: 'codex', state: 'idle', dispatcher_session_id: 'root', delegation_role: { name: 'Builder', builtin: BuiltinDelegationRole.Builder } },
  { id: 'child', label: 'Check keyboard flow', agent: 'codex', state: 'idle', dispatcher_session_id: 'builder' },
  { id: 'reviewer', label: 'Review role identity', agent: 'claude', state: 'idle', dispatcher_session_id: 'root', delegation_role: { name: 'Reviewer', builtin: BuiltinDelegationRole.Reviewer } },
];

const NO_AGENTS: AgentPaletteInput<PaletteSession> = {
  bands: { chief: null, turns: [], settled: [], crew: [], snoozed: [] },
  crewRoster: [],
  workspaces: [],
  tileTitle: () => '',
  now: 0,
};

export function DelegationChainHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [current, setCurrent] = useState('builder');
  const [paletteQuery, setPaletteQuery] = useState<string | null>(null);
  const menu = paletteQuery !== null;
  const [settings, setSettings] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const [rowGeneration, setRowGeneration] = useState(0);
  const chain = useRef<DelegationChainHandle>(null);
  const terminal = useRef<HTMLTextAreaElement>(null);
  useEffect(() => { terminal.current?.focus(); }, [current]);
  useEffect(() => { onReady(); setTriggerRerender(() => () => {}); }, [onReady, setTriggerRerender]);
  useShortcut('ui.commandPalette', () => { chain.current?.prepareCommand(); setPaletteQuery((query) => (query === null ? '>' : null)); }, true);
  useShortcut('session.historyBack', () => setCurrent('root'));
  useShortcut('ui.openSettings', () => setSettings((open) => !open));
  useShortcut('session.toggleSidebar', () => { chain.current?.dismiss('sidebar-collapse'); setCollapsed((value) => !value); });
  useEscapeStack(() => setSettings(false), settings);
  return (
    <DelegationChainProvider ref={chain} sessions={agents} navigationKey={current} blocked={menu || settings} onRestoreFocusFallback={() => terminal.current?.focus()} onSelectSession={(id) => { setCurrent(id); terminal.current?.focus(); }}>
      <div style={{ display: 'flex', gap: 24, padding: 16, color: 'var(--color-text-primary)', background: 'var(--color-bg-app)', minHeight: '100vh' }}>
        <aside className="sidebar" style={{ width: 230, flexShrink: 0 }}>
          {!collapsed && agents.map((agent) => (
            <div key={`${rowGeneration}:${agent.id}`} data-testid={`row-${agent.id}`} className={`session-item ${current === agent.id ? 'selected' : ''}`} style={{ display: 'flex', gap: 8, marginRight: 12 }}>
              <SessionLabel label={agent.label} session={agent} hasDelegates={agent.id === 'root'} />
              <DelegationChainTrigger session={agent} hasDelegates={agent.id === 'root'} />
            </div>
          ))}
        </aside>
        <main style={{ minWidth: 0 }}>
          <div data-testid="agent-header"><DelegationChainTrigger session={agents.find((agent) => agent.id === current)!} variant="header" /></div>
          <textarea ref={terminal} aria-label="Terminal keyboard target" />
          <output data-testid="selected-agent">{current}</output>
          <button data-testid="replace-sidebar-rows" onClick={() => setRowGeneration((value) => value + 1)}>Replace sidebar rows</button>
        </main>
      </div>
      {paletteQuery !== null && (
        <UnifiedPalette
          query={paletteQuery}
          onQueryChange={setPaletteQuery}
          onClose={() => setPaletteQuery(null)}
          agents={NO_AGENTS}
          desktops={[]}
          commands={[{
            id: 'show-delegation-chain', title: 'Show delegation chain', description: 'Navigate this agent’s dispatcher, peers, and delegates', icon: '↳',
            run: () => chain.current?.open(current),
          }]}
          onOpenAgent={() => {}}
          onWakeMember={() => {}}
          onOpenTile={() => {}}
          onSettle={() => {}}
          onSnooze={() => {}}
        />
      )}
      {settings && <FocusTrap focusTrapOptions={{ escapeDeactivates: false }}>
        <div role="dialog" aria-label="Settings"><input aria-label="Settings search" /></div>
      </FocusTrap>}
    </DelegationChainProvider>
  );
}
