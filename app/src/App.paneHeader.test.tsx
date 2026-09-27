import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentPane, agentWorkspace, daemonSeed, daemonSession, daemonWorkspace, type DaemonSession } from './test/daemonFixtures';
import type { EventMessage } from './test/protocol';
import { openActionMenu, openSession } from './test/appFixtures';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

type SessionUsage = NonNullable<DaemonSession['usage']>;
type InitialState = Partial<EventMessage<'initial_state'>>;

function modelUsage(overrides: Partial<SessionUsage['models'][number]> = {}): SessionUsage['models'][number] {
  return {
    model: 'claude-opus-5',
    purpose: 'agent',
    input_tokens: 4,
    output_tokens: 3_546,
    cache_read_tokens: 0,
    cache_write_5m_tokens: 0,
    cache_write_1h_tokens: 0,
    cache_write_unclassified_tokens: 0,
    total_tokens: 3_550,
    has_unpriced_usage: false,
    ...overrides,
  };
}

function usage(costUsd: number | undefined, hasUnpricedUsage = false, totalTokens = 3_550): SessionUsage {
  return {
    total_tokens: totalTokens,
    cost_usd: costUsd,
    has_unpriced_usage: hasUnpricedUsage,
    models: [modelUsage({ total_tokens: totalTokens, cost_usd: costUsd, has_unpriced_usage: hasUnpricedUsage })],
  };
}

async function openPane(session: Partial<DaemonSession> = {}, initialState: InitialState = {}) {
  const sessions = [daemonSession('s1', { label: 'ledger sweep', state: 'idle', ...session }), ...(initialState.sessions ?? [])];
  const view = await renderApp({
    initialState: { workspaces: sessions.map((entry) => agentWorkspace(entry.id)), ...initialState, sessions },
  });
  await openSession(view.daemon, sessions[0].label);
  return view;
}

const header = () => document.querySelector<HTMLElement>('[data-pane-id="pane-s1"] .workspace-pane-header')!;
const inHeader = () => within(header());
const usageBadge = () => inHeader().queryByLabelText(/^Session usage/);
const breakdown = () => screen.queryByRole('dialog', { name: 'Session usage breakdown' });

async function update(daemon: ScriptedDaemon, session: DaemonSession) {
  await gesture(daemon, () => daemon.emit({ event: 'session_state_changed', session }));
}

describe('App pane header', () => {
  it('names the session', async () => {
    await openPane();
    expect(inHeader().getByText('ledger sweep')).toBeInTheDocument();
  });

  it('falls back to the pane title when the session carries no label', async () => {
    const workspace = daemonWorkspace('workspace-s1', {
      root: { type: 'pane', pane_id: 'pane-s1' },
      panes: [{ ...agentPane('s1', 'workspace-s1'), title: 'shell' }],
    });
    const { daemon } = await openPane({}, { workspaces: [workspace] });

    await update(daemon, daemonSession('s1', { label: '', state: 'idle' }));

    expect(inHeader().getByText('shell')).toBeInTheDocument();
    expect(inHeader().queryByText('ledger sweep')).toBeNull();
  });

  it('shows the session’s state beside its name', async () => {
    const { daemon } = await openPane({ state: 'unknown' });
    expect(inHeader().getByLabelText('state unknown')).toBeInTheDocument();

    await update(daemon, daemonSession('s1', { label: 'ledger sweep', state: 'scheduled' }));
    expect(inHeader().getByLabelText('scheduled')).toBeInTheDocument();
    expect(inHeader().queryByLabelText('state unknown')).toBeNull();
  });

  it('renames the session from the header', async () => {
    const { daemon } = await openPane();

    fireEvent.click(inHeader().getByRole('button', { name: 'Rename session ledger sweep' }));
    const name = screen.getByDisplayValue('ledger sweep');
    fireEvent.change(name, { target: { value: 'nightly ledger sweep' } });
    await gesture(daemon, () => fireEvent.keyDown(name, { key: 'Enter' }));

    expect(daemon.sentOf('rename_session')).toEqual([{ cmd: 'rename_session', session_id: 's1', label: 'nightly ledger sweep' }]);
  });

  it('focuses the agent from its header until the user returns to the split', async () => {
    const { daemon } = await openPane();

    await gesture(daemon, () => fireEvent.click(inHeader().getByRole('button', { name: 'Focus agent ledger sweep' })));
    expect(inHeader().queryByRole('button', { name: 'Focus agent ledger sweep' })).toBeNull();

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Return to split' })));
    expect(inHeader().getByRole('button', { name: 'Focus agent ledger sweep' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Return to split' })).toBeNull();
  });

  it.each([
    ['a priced session in dollars rounded to cents', usage(1.234), 'Session usage $1.23', '$1.23'],
    ['real sub-cent usage as less than a cent', usage(0.004), 'Session usage <$0.01', '<$0.01'],
    ['the known amount of partially priced usage, marked', usage(1.234, true), 'Session usage $1.23*, some usage has no price', '$1.23*'],
    ['compact tokens when none of the usage is priced', usage(undefined, true, 184_000), 'Session usage 184k tokens, some usage has no price', '184k tokens'],
  ])('shows %s', async (_, sessionUsage, label, text) => {
    await openPane({ usage: sessionUsage });
    expect(inHeader().getByLabelText(label)).toHaveTextContent(text);
  });

  it('shows no cost before the session has usage, or while its measurement is incomplete, and follows fresh usage', async () => {
    const { daemon } = await openPane();
    expect(usageBadge()).toBeNull();

    await update(daemon, daemonSession('s1', { label: 'ledger sweep', state: 'idle', usage: usage(0.42) }));
    expect(usageBadge()).toHaveAccessibleName('Session usage $0.42');

    await update(daemon, daemonSession('s1', { label: 'ledger sweep', state: 'idle', usage: usage(0.73) }));
    expect(usageBadge()).toHaveAccessibleName('Session usage $0.73');

    await update(daemon, daemonSession('s1', { label: 'ledger sweep', state: 'idle', usage: { ...usage(0.73), measurement_incomplete: true } }));
    expect(usageBadge()).toBeNull();
  });

  it('breaks usage down per model with exact token counts, the guardian beside the agent, and why some is unpriced', async () => {
    await openPane({
      usage: {
        total_tokens: 186_197,
        cost_usd: 1.2345,
        has_unpriced_usage: true,
        models: [
          modelUsage({
            input_tokens: 12_345,
            output_tokens: 6_789,
            cache_read_tokens: 150_000,
            cache_write_5m_tokens: 10_000,
            cache_write_1h_tokens: 5_000,
            cache_write_unclassified_tokens: 187,
            total_tokens: 184_321,
            cost_usd: 1.234,
            has_unpriced_usage: true,
            unpriced_reason: 'Cache write duration is unavailable.',
          }),
          modelUsage({ purpose: 'guardian', input_tokens: 1_746, output_tokens: 130, total_tokens: 1_876, cost_usd: 0.0005 }),
        ],
      },
    });

    fireEvent.focus(usageBadge()!);

    const panel = within(breakdown()!);
    expect(panel.getByText('186,197 tokens')).toBeInTheDocument();
    expect(panel.getByText('claude-opus-5')).toBeInTheDocument();
    expect(panel.getByText('12,345')).toBeInTheDocument();
    expect(panel.getByText('6,789')).toBeInTheDocument();
    expect(panel.getByText('Guardian · claude-opus-5')).toBeInTheDocument();
    expect(panel.getByText('1,746')).toBeInTheDocument();
    expect(panel.getByText('* Cache write duration is unavailable.')).toBeInTheDocument();
  });

  it('keeps the hover preview of usage open while the pointer moves into it', async () => {
    await openPane({ usage: usage(0.42) });

    fireEvent.pointerEnter(usageBadge()!);
    await act(() => vi.advanceTimersByTimeAsync(160));
    const panel = breakdown()!;
    fireEvent.pointerLeave(usageBadge()!);
    fireEvent.pointerEnter(panel);
    await act(() => vi.advanceTimersByTimeAsync(240));

    expect(breakdown()).toBe(panel);
  });

  it('pins the usage breakdown from the Action menu, Escape closes it, and each later request opens the named session’s again', async () => {
    const { daemon } = await openPane({ usage: usage(0.42, false, 1_111) }, {
      sessions: [daemonSession('s2', { label: 'docs sweep', state: 'idle', usage: usage(0.42, false, 2_222) })],
    });
    const pinUsage = async (label: string) => {
      const search = await openActionMenu(daemon);
      fireEvent.change(search, { target: { value: `${label} usage` } });
      await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));
    };
    const dismiss = () => gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));

    await pinUsage('ledger sweep');
    expect(breakdown()).toHaveTextContent('esc close');
    expect(breakdown()).toHaveTextContent('1,111 tokens');
    await dismiss();
    expect(breakdown()).toBeNull();

    await pinUsage('ledger sweep');
    expect(breakdown()).toHaveTextContent('1,111 tokens');
    await dismiss();

    await openSession(daemon, 'docs sweep');
    await pinUsage('docs sweep');
    expect(breakdown()).toHaveTextContent('2,222 tokens');
  });

  it('carries the seed a session reports to and opens it, and carries none for a session that reports to no seed', async () => {
    const { daemon } = await openPane({ seed_id: 's-rep111' }, { seeds: [daemonSeed('s-rep111', { title: 'Report the ledger' })] });
    expect(inHeader().getByTestId('seed-chip-s1')).toHaveTextContent('Report the ledger');

    await gesture(daemon, () => fireEvent.click(inHeader().getByTestId('seed-chip-s1')));
    expect(daemon.sentOf('open_seed')).toEqual([expect.objectContaining({ seed_id: 's-rep111', session_id: 's1' })]);

    await update(daemon, daemonSession('s1', { label: 'ledger sweep', state: 'idle' }));
    expect(inHeader().queryByTestId('seed-chip-s1')).toBeNull();
  });

  it('shows and opens the seed a crew member tends', async () => {
    const { daemon } = await openPane(
      { label: 'Fern', crew_member: 'fern' },
      { seeds: [daemonSeed('s-crew11', { title: 'Member work', tender_member: 'fern' })] },
    );

    expect(inHeader().getByTestId('seed-chip-s1')).toHaveTextContent('Member work');
    await gesture(daemon, () => fireEvent.click(inHeader().getByTestId('seed-chip-s1')));
    expect(daemon.sentOf('open_seed')).toEqual([expect.objectContaining({ seed_id: 's-crew11', session_id: 's1' })]);
  });

  it('shows a delegation role that arrives after the header mounted, and its chain opens the agents it links', async () => {
    const { daemon } = await openPane({}, {
      sessions: [
        daemonSession('dispatcher', { label: 'docs sweep', state: 'idle' }),
        daemonSession('delegate', { label: 'glossary rework', agent: 'codex', dispatcher_session_id: 's1' }),
      ],
    });
    await update(daemon, daemonSession('s1', { label: 'ledger sweep', state: 'idle' }));

    await update(daemon, daemonSession('s1', {
      label: 'ledger sweep',
      state: 'idle',
      dispatcher_session_id: 'dispatcher',
      delegation_role: { name: 'Orchestrator', builtin: 'orchestrator' },
    }));
    const role = () => inHeader().getByRole('button', { name: /Orchestrator · Show delegation chain/ });
    expect(role()).toHaveTextContent('Orchestrator');

    await gesture(daemon, () => fireEvent.click(role()));
    await gesture(daemon, () => fireEvent.click(within(screen.getByRole('dialog', { name: 'Delegation chain' })).getByRole('button', { name: /docs sweep/ })));
    expect(daemon.sentOf('session_selected').slice(-1)[0]).toEqual({ cmd: 'session_selected', id: 'dispatcher' });
  });

  it('hands the open popover between the delegation chain and the pull request details', async () => {
    const { daemon } = await openPane({
      pull_requests: [{
        repository: 'github.com/victorarias/attn',
        number: 71,
        url: 'https://github.com/victorarias/attn/pull/71',
        created_at: '2026-08-30T12:00:00Z',
        state: 'open',
      }],
    }, {
      sessions: [daemonSession('delegate', { label: 'glossary rework', agent: 'codex', dispatcher_session_id: 's1' })],
    });
    const chain = () => screen.queryByRole('dialog', { name: 'Delegation chain' });
    const prDetails = () => screen.queryByRole('dialog', { name: /^Pull request / });
    const chainTrigger = () => inHeader().getByRole('button', { name: 'Show delegation chain for ledger sweep' });
    const prEntry = () => inHeader().getByRole('button', { name: 'Pull request attn#71 details' });

    await gesture(daemon, () => fireEvent.click(chainTrigger()));
    fireEvent.pointerEnter(prEntry());
    expect(chain()).not.toBeNull();
    expect(prDetails()).toBeNull();

    await gesture(daemon, () => fireEvent.click(prEntry()));
    expect(chain()).toBeNull();
    expect(prDetails()).not.toBeNull();

    await gesture(daemon, () => fireEvent.click(chainTrigger()));
    expect(prDetails()).toBeNull();
    expect(chain()).not.toBeNull();
  });

  it('tells the user a session runs an older terminal until they dismiss it, for the rest of the session', async () => {
    const { daemon } = await openPane({ terminal_build_stale: true });
    const notice = () => screen.queryByTestId('terminal-stale-build-notice');
    expect(notice()).toHaveTextContent('This session is running an older terminal.');

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Dismiss older-terminal notice' })));
    await update(daemon, daemonSession('s1', { label: 'ledger sweep', state: 'working', terminal_build_stale: true }));

    expect(notice()).toBeNull();
  });

  it('says nothing about the terminal build of a session that runs the current one', async () => {
    await openPane({ terminal_build_stale: false });
    expect(screen.queryByTestId('terminal-stale-build-notice')).toBeNull();
  });
});
