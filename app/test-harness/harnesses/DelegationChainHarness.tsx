import { useEffect, useRef, useState } from 'react';
import { DelegationChainProvider, DelegationChainTrigger, type ChainSession, type DelegationChainHandle } from '../../src/components/DelegationChain';
import { ActionMenu } from '../../src/components/ActionMenu';
import { BuiltinDelegationRole } from '../../src/types/generated';
import { useShortcut } from '../../src/shortcuts/useShortcut';
import type { HarnessProps } from '../types';
import '../../src/App.css';

const agents: ChainSession[] = [
  { id: 'root', label: 'Coordinate role identity', agent: 'claude', state: 'idle', delegation_role: { name: 'Orchestrator', builtin: BuiltinDelegationRole.Orchestrator } },
  { id: 'builder', label: 'Build chain navigator', agent: 'codex', state: 'idle', dispatcher_session_id: 'root', delegation_role: { name: 'Builder', builtin: BuiltinDelegationRole.Builder } },
  { id: 'child', label: 'Check keyboard flow', agent: 'codex', state: 'idle', dispatcher_session_id: 'builder' },
  { id: 'reviewer', label: 'Review role identity', agent: 'claude', state: 'idle', dispatcher_session_id: 'root', delegation_role: { name: 'Reviewer', builtin: BuiltinDelegationRole.Reviewer } },
];

export function DelegationChainHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [current, setCurrent] = useState('builder');
  const [menu, setMenu] = useState(false);
  const chain = useRef<DelegationChainHandle>(null);
  const terminal = useRef<HTMLTextAreaElement>(null);
  useEffect(() => { onReady(); setTriggerRerender(() => () => {}); }, [onReady, setTriggerRerender]);
  useShortcut('ui.actionMenu', () => { chain.current?.dismiss(); setMenu((open) => !open); }, true);
  return (
    <DelegationChainProvider ref={chain} sessions={agents} onSelectSession={(id) => { setCurrent(id); terminal.current?.focus(); }}>
      <div style={{ display: 'flex', gap: 24, padding: 16, color: 'var(--color-text-primary)', background: 'var(--color-bg-app)', minHeight: '100vh' }}>
        <aside style={{ width: 230, flexShrink: 0 }}>
          {agents.map((agent) => (
            <div key={agent.id} data-testid={`row-${agent.id}`} className={`session-item ${current === agent.id ? 'selected' : ''}`} style={{ display: 'flex', gap: 8 }}>
              <span style={{ flex: 1 }}>{agent.label}</span>
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
    </DelegationChainProvider>
  );
}
