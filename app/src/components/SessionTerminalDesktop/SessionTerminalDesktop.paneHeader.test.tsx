import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SessionTerminalDesktop } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalDesktopState } from '../../types/desktop';
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

function loneAgentDesktop(): TerminalDesktopState {
  return {
    agents: [{ id: 'pane-1', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-1' },
  };
}

function renderLonePane(props: Partial<React.ComponentProps<typeof SessionTerminalDesktop>> = {}) {
  return render(
    <SessionTerminalDesktop
      desktopId="desktop-1"
      desktopSessions={[{ id: 'sess-1', label: GENERATED_NAME, agent: 'claude', cwd: '/tmp/project' }]}
      terminalState={loneAgentDesktop()}
      activePaneId="pane-1"
      selectedSessionId="sess-1"
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
      purpose: 'agent',
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

describe('SessionTerminalDesktop pane header', () => {

  it('keeps automation and PR provenance visible below the session name', () => {
    renderLonePane({
      desktopSessions: [{
        id: 'sess-1',
        label: 'feed-nexus-web#101 · gpt-5.6-sol',
        agent: 'claude',
        cwd: '/tmp/project',
        automation: {
          run_id: 'run-1',
          definition_id: 1,
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

    const automation = screen.getByText('GPT Sol medium');
    expect(automation).toBeInTheDocument();
    expect(automation.closest('.desktop-pane-identity-main')).toBeNull();
    expect(screen.getByRole('button', { name: 'feed-nexus-web#101 ↗' })).toBeInTheDocument();
    expect(screen.getByText('Fix validation race')).toBeInTheDocument();
  });

  it('is not a drag handle on a lone tile, which has nowhere to move to', () => {
    const { container } = renderLonePane();

    const header = container.querySelector('.desktop-pane-header') as HTMLElement;
    expect(header.className).toContain('desktop-pane-header--static');
    expect(header.className).not.toContain('desktop-pane-header--draggable');
    expect(header).not.toHaveAttribute('title');
  });

  it('places delegation controls after session usage on the headline', () => {
    const { container } = renderLonePane({
      desktopSessions: [{
        id: 'sess-1',
        label: GENERATED_NAME,
        agent: 'claude',
        cwd: '/tmp/project',
        usage: usage(2.61),
      }],
      delegationSessions: [
        { id: 'dispatcher', label: 'chief', agent: 'claude', state: 'idle' },
        {
          id: 'sess-1',
          label: GENERATED_NAME,
          agent: 'claude',
          state: 'working',
          dispatcher_session_id: 'dispatcher',
          delegation_role: { name: 'Builder' },
        },
      ],
      onSelectSession: vi.fn(),
    });

    const headline = container.querySelector('.desktop-pane-identity-main');
    expect(headline).not.toBeNull();
    expect(headline).toContainElement(screen.getByLabelText('Session usage $2.61'));
    expect(headline).toContainElement(screen.getByRole('button', { name: /Builder · Show delegation chain/i }));
    expect(headline?.textContent).toMatch(/\$2\.61.*Builder/i);
  });

});
