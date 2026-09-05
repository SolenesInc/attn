import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalWorkspaceState } from '../../types/workspace';
import type { SessionUsage } from '../../types/generated';

vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

const GENERATED_NAME = 'judge yielded stops so background waits stay green';

function loneAgentWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-1', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-1' },
  };
}

function renderLonePane(props: Partial<React.ComponentProps<typeof SessionTerminalWorkspace>> = {}) {
  return render(
    <SessionTerminalWorkspace
      workspaceId="workspace-1"
      workspaceSessions={[{ id: 'sess-1', label: GENERATED_NAME, agent: 'claude', cwd: '/tmp/project' }]}
      workspace={loneAgentWorkspace()}
      activePaneId="pane-1"
      fontSize={13}
      enabled
      isActiveSession
      eventRouter={createPaneRuntimeEventRouterController()}
      onSplitPane={vi.fn()}
      onClosePane={vi.fn()}
      onFocusPane={vi.fn()}
      onNavigateOutOfSession={vi.fn()}
      {...props}
    />,
  );
}

function usage(costUsd?: number, hasUnpricedUsage = false, totalTokens = 3_550): SessionUsage {
  return {
    total_tokens: totalTokens,
    cost_usd: costUsd,
    has_unpriced_usage: hasUnpricedUsage,
    models: [{
      model: 'claude-opus-5',
      input_tokens: 4,
      output_tokens: totalTokens - 4,
      cache_read_tokens: 0,
      cache_write_5m_tokens: 0,
      cache_write_1h_tokens: 0,
      cache_write_unclassified_tokens: 0,
      total_tokens: totalTokens,
      cost_usd: costUsd,
      has_unpriced_usage: hasUnpricedUsage,
    }],
  };
}

describe('SessionTerminalWorkspace pane header', () => {
  it('names the session on a lone tile with nothing else to show', () => {
    const { container } = renderLonePane();

    expect(container.querySelector('.workspace-pane-title')?.textContent).toBe(GENERATED_NAME);
  });

  it('keeps automation and PR provenance visible below the session name', () => {
    renderLonePane({
      workspaceSessions: [{
        id: 'sess-1',
        label: 'feed-nexus-web#101 · gpt-5.6-sol',
        agent: 'claude',
        cwd: '/tmp/project',
        automation: {
          run_id: 'run-1',
          definition_id: 'review-sol',
          definition_name: 'Requested PR review - GPT Sol medium',
          trigger_type: 'github_review_requested',
          pull_request: {
            repository: 'ghe.spotify.net/audiobook-feed-mgmt/feed-nexus-web',
            number: 101,
            url: 'https://ghe.spotify.net/audiobook-feed-mgmt/feed-nexus-web/pull/101',
            title: 'Fix validation race',
            head_sha: '82f1c7a000000000000000000000000000000000',
          },
        },
      }],
    });

    expect(screen.getByText('GPT Sol medium')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'feed-nexus-web#101 ↗' })).toBeInTheDocument();
    expect(screen.getByText('Fix validation race')).toBeInTheDocument();
  });

  it('is not a drag handle on a lone tile, which has nowhere to move to', () => {
    const { container } = renderLonePane();

    const header = container.querySelector('.workspace-pane-header') as HTMLElement;
    expect(header.className).toContain('workspace-pane-header--static');
    expect(header.className).not.toContain('workspace-pane-header--draggable');
    expect(header).not.toHaveAttribute('title');
  });

  it('offers rename on a lone tile, where a wrong generated name is most visible', () => {
    const onRenameSession = vi.fn();
    renderLonePane({ onRenameSession });

    const rename = screen.getByTestId('rename-pane-pane-1');
    fireEvent.click(rename);

    expect(screen.getByDisplayValue(GENERATED_NAME)).toBeInTheDocument();
  });

  it("shows the session's state beside its name, as the sidebar row does", () => {
    const { container } = renderLonePane({
      workspaceSessions: [{ id: 'sess-1', label: GENERATED_NAME, agent: 'claude', cwd: '/tmp/project', state: 'working' }],
    });

    const dot = container.querySelector('.workspace-pane-header .state-indicator');
    expect(dot?.className).toContain('state-indicator--working');
  });

  it('shows a priced session in USD rounded to cents', () => {
    renderLonePane({
      workspaceSessions: [{
        id: 'sess-1',
        label: GENERATED_NAME,
        agent: 'claude',
        cwd: '/tmp/project',
        usage: usage(1.234),
      }],
    });

    expect(screen.getByLabelText('Session usage $1.23')).toHaveTextContent('$1.23');
  });

  it('does not round real sub-cent usage down to free', () => {
    renderLonePane({
      workspaceSessions: [{
        id: 'sess-1',
        label: GENERATED_NAME,
        agent: 'claude',
        cwd: '/tmp/project',
        usage: usage(0.004),
      }],
    });

    expect(screen.getByLabelText('Session usage <$0.01')).toHaveTextContent('<$0.01');
  });

  it('keeps the known dollar amount and marks partially priced usage', () => {
    renderLonePane({
      workspaceSessions: [{
        id: 'sess-1',
        label: GENERATED_NAME,
        agent: 'claude',
        cwd: '/tmp/project',
        usage: usage(1.234, true),
      }],
    });

    expect(screen.getByLabelText('Session usage $1.23*, some usage has no price')).toHaveTextContent('$1.23*');
  });

  it('falls back to compact tokens when none of the usage can be priced', () => {
    renderLonePane({
      workspaceSessions: [{
        id: 'sess-1',
        label: GENERATED_NAME,
        agent: 'claude',
        cwd: '/tmp/project',
        usage: usage(undefined, true, 184_000),
      }],
    });

    expect(screen.getByLabelText('Session usage 184k tokens, some usage has no price')).toHaveTextContent('184k tokens');
  });

  it('shows no cost before the session has usage', () => {
    renderLonePane();

    expect(screen.queryByLabelText(/Session usage/)).not.toBeInTheDocument();
  });

  it('updates the visible cost when fresh session usage arrives', () => {
    const { rerender } = renderLonePane({
      workspaceSessions: [{
        id: 'sess-1',
        label: GENERATED_NAME,
        agent: 'claude',
        cwd: '/tmp/project',
        usage: usage(0.42),
      }],
    });

    expect(screen.getByLabelText('Session usage $0.42')).toBeInTheDocument();

    rerender(
      <SessionTerminalWorkspace
        workspaceId="workspace-1"
        workspaceSessions={[{
          id: 'sess-1',
          label: GENERATED_NAME,
          agent: 'claude',
          cwd: '/tmp/project',
          usage: usage(0.73),
        }]}
        workspace={loneAgentWorkspace()}
        activePaneId="pane-1"
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={createPaneRuntimeEventRouterController()}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />,
    );

    expect(screen.queryByLabelText('Session usage $0.42')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Session usage $0.73')).toBeInTheDocument();
  });

  it('pins the active session usage when the Action menu requests it', async () => {
    const session = {
      id: 'sess-1',
      label: GENERATED_NAME,
      agent: 'claude',
      cwd: '/tmp/project',
      usage: usage(0.73),
    };
    const { rerender } = renderLonePane({ workspaceSessions: [session] });

    rerender(
      <SessionTerminalWorkspace
        workspaceId="workspace-1"
        workspaceSessions={[session]}
        usagePopoverRequest={{ sessionId: 'sess-1', nonce: 1 }}
        workspace={loneAgentWorkspace()}
        activePaneId="pane-1"
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={createPaneRuntimeEventRouterController()}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />,
    );

    expect(await screen.findByRole('dialog', { name: 'Session usage breakdown' })).toHaveTextContent('esc close');
  });

  it('falls back to the pane title when the session carries no label', () => {
    const { container } = renderLonePane({ workspaceSessions: [] });

    expect(container.querySelector('.workspace-pane-title')?.textContent).toBe('shell');
  });
  it('carries the seed a delegated session reports to, and opens it', () => {
    const onOpenSeed = vi.fn();
    renderLonePane({
      workspaceSessions: [{ id: 'sess-1', label: GENERATED_NAME, agent: 'claude', cwd: '/tmp/project', seedId: 's-rep111' }],
      onOpenSeed,
    });

    fireEvent.click(screen.getByTestId('seed-chip-sess-1'));

    expect(onOpenSeed).toHaveBeenCalledWith('s-rep111');
  });

  it('shows no seed chip on a session that reports to none', () => {
    renderLonePane({ onOpenSeed: vi.fn() });

    expect(screen.queryByTestId('seed-chip-sess-1')).not.toBeInTheDocument();
  });

  it('shows and opens the current crew member-only claim', () => {
    const onOpenSeed = vi.fn();
    renderLonePane({
      workspaceSessions: [{
        id: 'sess-1', label: 'Fern', agent: 'claude', cwd: '/tmp/project', crewMember: 'fern',
      }],
      gardenSeeds: [{
        id: 's-crew11', title: 'Member work', body: '', status: 'growing', step_slug: 'member-work',
        planter_session: '', planter_member: '', tender_session: '', tender_member: 'fern',
        edges: [], ready: false, template: false, gate: false, vars: [], rev: 1,
        created_at: '2026-09-04T12:00:00Z', updated_at: '2026-09-04T12:00:00Z',
        state_changed_at: '2026-09-04T12:00:00Z', state_changed_at_exact: true,
      }],
      onOpenSeed,
    });

    expect(screen.getByText('Member work')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('seed-chip-sess-1'));
    expect(onOpenSeed).toHaveBeenCalledWith('s-crew11');
  });
});
