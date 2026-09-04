import { useEffect, useState } from 'react';
import { PaneSeedChip } from '../../src/components/PaneSeedChip';
import { derivePaneSeedDisplay } from '../../src/components/paneSeedDisplay';
import { DaemonApiProvider, type DaemonApi } from '../../src/contexts/DaemonApiContext';
import type { Seed } from '../../src/hooks/useDaemonSocket';
import type { HarnessProps } from '../types';
import '../../src/App.css';

const states = ['planted', 'growing', 'dormant', 'harvested', 'withered'];
const now = new Date().toISOString();
const base: Seed = {
  id: 's-garden', title: 'Give the garden a little life', body: 'Make every lifecycle state recognizable at header size.',
  status: 'growing', state_changed_at: now, state_changed_at_exact: true, step_slug: 'garden-life',
  planter_session: '', planter_member: '', tender_session: 'garden-agent', tender_member: '',
  edges: [], ready: false, template: false, gate: false, vars: [], rev: 1, created_at: now, updated_at: now,
};
const plot = { ...base, id: 's-plot', title: 'Polish the Garden', plot_progress: { done: 3, total: 7, ready: 0, growing: 2, blocked: 0, dormant: 1, withered: 1 } };
const api = {
  sendSeedDocumentGet: async (id: string) => ({
    seed: { ...base, id, status: id.startsWith('s-') && states.includes(id.slice(2)) ? id.slice(2) : 'growing' },
    children: [], artifacts: [], references: [], notes_total: 1, tender_holds: false,
    notes: [{ id: 'n-1', seed_id: id, kind: 'note', body: 'The silhouettes work at 24px. Next, check the hover at the edge of a narrow pane.', created_at: now, author_session: '', author_member: '' }],
  }),
} as unknown as DaemonApi;

export function SeedHeaderHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [status, setStatus] = useState('growing');
  const [opened, setOpened] = useState('');
  const [terminalEscapes, setTerminalEscapes] = useState(0);
  useEffect(() => { onReady(); setTriggerRerender(() => () => {}); }, [onReady, setTriggerRerender]);
  const seed = { ...base, id: `s-${status}`, status, tender_session: status === 'growing' ? 'garden-agent' : '' };
  return (
    <DaemonApiProvider api={api}>
      <div style={{ padding: 28, color: 'var(--color-text-primary)', background: 'var(--color-bg-app)', minHeight: '100vh' }}>
        <div style={{ display: 'flex', gap: 12, marginBottom: 24 }}>
          {states.map((state) => <button key={state} onClick={() => setStatus(state)}>{state}</button>)}
          <button onClick={() => { document.documentElement.dataset.theme = document.documentElement.dataset.theme === 'light' ? 'dark' : 'light'; }}>Theme</button>
        </div>
        <div data-testid="seed-header" style={{ width: 430, display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 8, border: '1px solid var(--color-border)', marginBottom: 20 }}>
          <span>Agent</span>
          <PaneSeedChip display={derivePaneSeedDisplay([seed], 'garden-agent', seed.id)} crownSeedId={seed.id} unread={false} sessionId="garden-agent" pinned={false} onOpenSeed={setOpened} onPopoverClosed={() => {}} />
        </div>
        <div style={{ display: 'grid', gap: 20, width: 430 }}>
          {states.map((state) => (
            <PaneSeedChip key={state} display={{ kind: 'crown', seedId: `s-${state}`, seed: { ...base, id: `s-${state}`, status: state } }} unread={state === 'harvested'} sessionId={state} pinned={false} onOpenSeed={setOpened} onPopoverClosed={() => {}} />
          ))}
          <PaneSeedChip display={{ kind: 'plot', plot, tended: [base, { ...base, id: 's-b', title: 'Useful previews', body: 'Show the latest note and outcome.' }] }} unread={false} sessionId="plot" pinned={false} onOpenSeed={setOpened} onPopoverClosed={() => {}} />
        </div>
        <output data-testid="opened">{opened}</output>
        <textarea
          aria-label="Terminal keyboard target"
          onKeyDown={(event) => { if (event.key === 'Escape') setTerminalEscapes((count) => count + 1); }}
        />
        <output data-testid="terminal-escapes">{terminalEscapes}</output>
      </div>
    </DaemonApiProvider>
  );
}
