import { useEffect, useRef, useState } from 'react';
import { DelegationChainProvider, DelegationChainTrigger, type ChainSession, type DelegationChainHandle } from '../../src/components/DelegationChain';
import { ActionMenu } from '../../src/components/ActionMenu';
import FocusTrap from 'focus-trap-react';
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

export function DelegationChainHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [current, setCurrent] = useState('builder');
  const [menu, setMenu] = useState(false);
  const [settings, setSettings] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const chain = useRef<DelegationChainHandle>(null);
  const terminal = useRef<HTMLTextAreaElement>(null);
  useEffect(() => { terminal.current?.focus(); }, [current, collapsed]);
  useEffect(() => { onReady(); setTriggerRerender(() => () => {}); }, [onReady, setTriggerRerender]);
  useShortcut('ui.actionMenu', () => { chain.current?.dismiss(); setMenu((open) => !open); }, true);
  useShortcut('session.historyBack', () => setCurrent('root'));
  useShortcut('ui.openSettings', () => setSettings((open) => !open));
  useShortcut('session.toggleSidebar', () => { chain.current?.dismiss(); setCollapsed((value) => !value); });
  useEscapeStack(() => setSettings(false), settings);
  return (
    <DelegationChainProvider ref={chain} sessions={agents} navigationKey={current} blocked={menu || settings} onSelectSession={(id) => { setCurrent(id); terminal.current?.focus(); }}>
      <div style={{ display: 'flex', gap: 24, padding: 16, color: 'var(--color-text-primary)', background: 'var(--color-bg-app)', minHeight: '100vh' }}>
        <aside className="sidebar" style={{ width: 230, flexShrink: 0 }}>
          {!collapsed && agents.map((agent) => (
            <div key={agent.id} data-testid={`row-${agent.id}`} className={`session-item ${current === agent.id ? 'selected' : ''}`} style={{ display: 'flex', gap: 8, marginRight: 12 }}>
              <SessionLabel label={agent.label} session={agent} hasDelegates={agent.id === 'root'} />
              <DelegationChainTrigger session={agent} hasDelegates={agent.id === 'root'} />
            </div>
          ))}
        </aside>
        <main style={{ minWidth: 0 }}>
          <div data-testid="agent-header"><DelegationChainTrigger session={agents.find((agent) => agent.id === current)!} variant="header" /></div>
          <textarea ref={terminal} aria-label="Terminal keyboard target" />
          <output data-testid="selected-agent">{current}</output>
        </main>
      </div>
      <ActionMenu isOpen={menu} onClose={() => setMenu(false)} actions={[{
        id: 'show-delegation-chain', title: 'Show delegation chain', description: 'Navigate this agent’s dispatcher, peers, and delegates', icon: '↳',
        run: () => chain.current?.open(current, terminal.current),
      }]} />
      {settings && <FocusTrap focusTrapOptions={{ escapeDeactivates: false }}>
        <div role="dialog" aria-label="Settings"><input aria-label="Settings search" /></div>
      </FocusTrap>}
    </DelegationChainProvider>
  );
}
