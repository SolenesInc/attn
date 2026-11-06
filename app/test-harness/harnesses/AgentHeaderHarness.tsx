import { useEffect } from 'react';
import { SessionTerminalDesktop } from '../../src/components/SessionTerminalDesktop';
import { createPaneRuntimeEventRouterController } from '../../src/components/SessionTerminalDesktop/paneRuntimeEventRouter';
import { DaemonApiProvider, type DaemonApi } from '../../src/contexts/DaemonApiContext';
import { useAsksStore } from '../../src/store/asks';
import { BuiltinDelegationRole, type Ask, type SessionUsage } from '../../src/types/generated';
import type { Seed } from '../../src/hooks/useDaemonSocket';
import type { TerminalLayoutNode } from '../../src/types/desktop';
import type { DesktopSelectionStyle } from '../../src/utils/desktopSelectionStyle';
import type { HarnessProps } from '../types';
import '../../src/App.css';
import '../../src/components/Sidebar.css';

const eventRouter = createPaneRuntimeEventRouterController();
const api = {} as DaemonApi;
const noop = () => {};
const at = '2026-09-15T12:00:00Z';

function seed(id: string, title: string, tender: string | null, extra: Partial<Seed> = {}): Seed {
  return {
    id, title, body: '', status: 'growing', state_changed_at: at, state_changed_at_exact: true, step_slug: id,
    profile_id: 'profile-default', planter: { ref: 'user', name: 'the user' },
    tender: tender ? { ref: `session:${tender}`, name: tender, session_id: tender } : undefined, claimed: Boolean(tender),
    edges: [], ready: false, template: false, gate: false, vars: [], rev: 1, created_at: at, updated_at: at,
    ...extra,
  };
}

const partOf = (plot: string) => ({ edges: [{ kind: 'part-of', to: plot }] }) as Partial<Seed>;

const seeds: Seed[] = [
  seed('s-header', 'Keep header controls aligned across every agent tile', 'release'),
  seed('s-plot', 'Outpost Notebook sync', null, {
    plot_progress: { blocked: 0, done: 2, parked: 0, growing: 2, ready: 1, total: 5, withered: 0 },
  }),
  seed('s-plot-a', 'Sync every profile folder', 'sync', partOf('s-plot')),
  seed('s-plot-b', 'Pin the outpost copy placement', 'sync', partOf('s-plot')),
  seed('s-proto', 'Prototype options for the session pane header strip', 'proto'),
];

const usage: SessionUsage = {
  total_tokens: 12500, cost_usd: 2.61,
  models: [{ model: 'gpt-5.6-sol', purpose: 'agent', input_tokens: 10000, output_tokens: 2500,
    cache_read_tokens: 0, cache_write_5m_tokens: 0, cache_write_1h_tokens: 0,
    cache_write_unclassified_tokens: 0, total_tokens: 12500, cost_usd: 2.61 }],
};

const asks: Ask[] = [
  { id: 'ask-release', session_id: 'release', subject: 'Ship the release today or wait for review?', created_at: at, base_dir: '/tmp', parts: [] },
  { id: 'ask-sync', session_id: 'sync', subject: 'Which store should keep the cache?', created_at: at, base_dir: '/tmp', parts: [] },
];

const sessions = [
  {
    id: 'release', label: 'Coordinate the release', agent: 'codex' as const, cwd: '/tmp/release', state: 'working' as const,
    usage, autoSettleFiresAt: new Date(Date.now() + 3_600_000).toISOString(),
    pullRequests: [{ repository: 'github.com/victor/attn', number: 412, url: 'https://github.com/victor/attn/pull/412',
      state: 'open', title: 'Two-row pane header', created_at: at }],
  },
  {
    id: 'sync', label: 'Review the outpost sync', agent: 'claude' as const, cwd: '/tmp/sync', state: 'waiting_input' as const,
    host: 'outpost-1', hostName: 'ai-sandbox', usage,
  },
  { id: 'bare', label: 'Tidy the scratch branch', agent: 'codex' as const, cwd: '/tmp/bare', state: 'idle' as const },
  {
    id: 'proto', label: 'Prototype options for the session pane header', agent: 'claude' as const, cwd: '/tmp/proto',
    state: 'idle' as const, host: 'outpost-2', hostName: 'gpu-box-2', offline: true,
    automation: { run_id: 'run-1', definition_id: 'review', definition_name: 'Requested PR review - GPT Sol medium', trigger_type: 'schedule' },
  },
];

const roles = [
  { id: 'release', label: 'Coordinate the release', agent: 'codex', state: 'working' as const,
    delegation_role: { name: 'Orchestrator', builtin: BuiltinDelegationRole.Orchestrator } },
  { id: 'sync', label: 'Review the outpost sync', agent: 'claude', state: 'waiting_input' as const,
    dispatcher_session_id: 'release', delegation_role: { name: 'Builder', builtin: BuiltinDelegationRole.Builder } },
  { id: 'proto', label: 'Prototype options for the session pane header', agent: 'claude', state: 'idle' as const,
    dispatcher_session_id: 'release', delegation_role: { name: 'Scout', icon: '' } },
];

const pane = (id: string) => ({ type: 'pane', paneId: `pane-${id}` }) as const;
const column = (top: string, bottom: string) => ({
  type: 'split', splitId: `split-${top}`, direction: 'horizontal', ratio: 0.5, children: [pane(top), pane(bottom)],
}) as const;
const GRID: TerminalLayoutNode = {
  type: 'split', splitId: 'split-grid', direction: 'vertical', ratio: 0.5,
  children: [column('release', 'sync'), column('bare', 'proto')],
};

function AgentDesktop({ id, ids, layoutTree, selection, usageRequest }: {
  id: string;
  ids: readonly string[];
  layoutTree: TerminalLayoutNode;
  selection?: DesktopSelectionStyle;
  usageRequest?: string;
}) {
  return (
    <SessionTerminalDesktop
      desktopId={`agent-header-${id}`}
      desktopSessions={sessions}
      delegationSessions={roles}
      gardenSeeds={seeds}
      onOpenSeed={noop}
      onOpenAsk={noop}
      onSelectSession={noop}
      usagePopoverRequest={usageRequest ? { sessionId: usageRequest, nonce: 1 } : undefined}
      desktopSelectionStyle={selection}
      terminalState={{
        agents: sessions.filter((session) => ids.includes(session.id)).map((session) => ({
          id: `pane-${session.id}`, runtimeId: session.id, sessionId: session.id, title: session.label,
        })),
        layoutTree,
      }}
      activePaneId={`pane-${ids[0]}`}
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
  );
}

export function AgentHeaderHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const options = new URLSearchParams(window.location.search);
  const usageRequest = options.get('usage') ?? undefined;
  const tileWidth = options.get('width');
  const selection = options.get('selection') as DesktopSelectionStyle | null;
  const asksShown = useAsksStore((state) => state.asks.length === asks.length);
  useEffect(() => {
    useAsksStore.getState().setAsks(asks);
    setTriggerRerender(() => noop);
  }, [setTriggerRerender]);
  useEffect(() => {
    if (asksShown) onReady();
  }, [asksShown, onReady]);
  return (
    <DaemonApiProvider api={api}>
      <div
        data-testid="agent-wall"
        style={{ height: '100vh', display: 'flex', background: 'var(--color-bg-app)', ...(tileWidth ? { '--tile-width': `${tileWidth}px` } : {}) } as React.CSSProperties}
      >
        {selection ? (
          <AgentDesktop id="grid" ids={sessions.map((session) => session.id)} layoutTree={GRID} selection={selection} />
        ) : sessions.map((session) => (
          <div key={session.id} style={{ flex: '0 0 var(--tile-width, 480px)', display: 'flex', minWidth: 0 }}>
            <AgentDesktop id={session.id} ids={[session.id]} layoutTree={pane(session.id)} usageRequest={usageRequest} />
          </div>
        ))}
      </div>
    </DaemonApiProvider>
  );
}
