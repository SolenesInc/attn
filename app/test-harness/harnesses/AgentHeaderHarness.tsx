import { useEffect } from 'react';
import { SessionTerminalWorkspace } from '../../src/components/SessionTerminalWorkspace';
import { createPaneRuntimeEventRouterController } from '../../src/components/SessionTerminalWorkspace/paneRuntimeEventRouter';
import { DaemonApiProvider, type DaemonApi } from '../../src/contexts/DaemonApiContext';
import { BuiltinDelegationRole } from '../../src/types/generated';
import type { Seed } from '../../src/hooks/useDaemonSocket';
import type { HarnessProps } from '../types';
import '../../src/App.css';

const eventRouter = createPaneRuntimeEventRouterController();
const api = {} as DaemonApi;
const noop = () => {};
const seed: Seed = {
  id: 's-header', title: 'Keep header controls aligned', body: 'Center the session identity beside its seed.',
  status: 'growing', state_changed_at: '2026-09-15T12:00:00Z', state_changed_at_exact: true, step_slug: 'header-alignment',
  planter_session: '', planter_member: '', tender_session: 'agent', tender_member: '',
  edges: [], ready: false, template: false, gate: false, vars: [], rev: 1,
  created_at: '2026-09-15T12:00:00Z', updated_at: '2026-09-15T12:00:00Z',
};

export function AgentHeaderHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const options = new URLSearchParams(window.location.search);
  const split = options.has('split');
  const hasSeed = options.has('seed');
  const label = 'Coordinate the release';
  const agents = [{ id: 'pane-agent', runtimeId: 'agent', sessionId: 'agent', title: label }];
  if (split) agents.push({ id: 'pane-peer', runtimeId: 'peer', sessionId: 'peer', title: 'Review the release' });
  useEffect(() => { onReady(); setTriggerRerender(() => noop); }, [onReady, setTriggerRerender]);
  return (
    <DaemonApiProvider api={api}>
      <div style={{ height: '100vh', display: 'flex', background: 'var(--color-bg-app)' }}>
        <SessionTerminalWorkspace
          workspaceId="header-alignment"
          workspaceSessions={agents.map((agent) => ({
            id: agent.sessionId, label: agent.title, agent: 'codex', cwd: '/tmp/header-alignment', state: 'idle',
            seedId: hasSeed ? seed.id : undefined,
            usage: options.has('usage') ? {
              total_tokens: 12500, cost_usd: 2.61,
              models: [{ model: 'gpt-5.6-sol', purpose: 'agent', input_tokens: 10000, output_tokens: 2500,
                cache_read_tokens: 0, cache_write_5m_tokens: 0, cache_write_1h_tokens: 0,
                cache_write_unclassified_tokens: 0, total_tokens: 12500, cost_usd: 2.61 }],
            } : undefined,
            automation: options.has('provenance') ? {
              run_id: 'run-1', definition_id: 'release', definition_name: 'Release checks', trigger_type: 'schedule',
            } : undefined,
          }))}
          delegationSessions={options.has('role') ? agents.map((agent) => ({
            id: agent.sessionId, label: agent.title, agent: 'codex', state: 'idle',
            delegation_role: { name: 'Orchestrator', builtin: BuiltinDelegationRole.Orchestrator },
          })) : []}
          gardenSeeds={hasSeed ? [seed] : []}
          onOpenSeed={noop}
          workspace={{ agents, layoutTree: split ? {
            type: 'split', splitId: 'split', direction: 'vertical', ratio: 0.5,
            children: [{ type: 'pane', paneId: 'pane-agent' }, { type: 'pane', paneId: 'pane-peer' }],
          } : { type: 'pane', paneId: 'pane-agent' } }}
          activePaneId="pane-agent"
          fontSize={13}
          enabled={false}
          isActiveSession={false}
          terminalsLive={false}
          eventRouter={eventRouter}
          onSplitPane={noop}
          onClosePane={noop}
          onFocusPane={noop}
          onNavigateOutOfSession={noop}
        />
      </div>
    </DaemonApiProvider>
  );
}
