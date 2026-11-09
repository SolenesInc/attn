import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, within } from '@testing-library/react';
import * as clipboard from '../../utils/clipboardBridge';
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
    <SessionTerminalDesktop shelfNarrowestCols={66}
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

  it('is not a drag handle on a lone tile, which has nowhere to move to', () => {
    const { container } = renderLonePane();

    const header = container.querySelector('.desktop-pane-header') as HTMLElement;
    expect(header.className).toContain('desktop-pane-header--static');
    expect(header.className).not.toContain('desktop-pane-header--draggable');
    expect(header).not.toHaveAttribute('title');
  });

  it('carries the session’s automation, pull request, role and usage in its header', () => {
    const { container } = renderLonePane({
      desktopSessions: [{
        id: 'sess-1',
        label: 'feed-nexus-web#101 · gpt-5.6-sol',
        agent: 'claude',
        cwd: '/tmp/project',
        usage: usage(2.61),
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

    const header = within(container.querySelector<HTMLElement>('.desktop-pane-header')!);
    expect(header.getByText('feed-nexus-web#101 · gpt-5.6-sol')).toBeInTheDocument();
    expect(header.getByText('GPT Sol medium')).toBeInTheDocument();
    expect(header.getByRole('button', { name: 'Open pull request feed-nexus-web#101' })).toBeInTheDocument();
    expect(header.getByText('Fix validation race')).toBeInTheDocument();
    expect(header.getByRole('button', { name: /Builder · Show delegation chain/i })).toBeInTheDocument();
    expect(header.getByLabelText('Session usage $2.61')).toBeInTheDocument();
  });

  it('shows the session’s short id and copies the full id on click', () => {
    const write = vi.spyOn(clipboard, 'writeClipboardText').mockResolvedValue();
    const id = '8e164800-98f1-4e33-8046-a05a2dee73f9';
    const { container } = renderLonePane({
      desktopSessions: [{ id, label: GENERATED_NAME, agent: 'claude', cwd: '/tmp/project' }],
      terminalState: { agents: [{ id: 'pane-1', runtimeId: 'rt-1', sessionId: id, title: 'shell' }], layoutTree: { type: 'pane', paneId: 'pane-1' } },
    });

    const button = within(container.querySelector<HTMLElement>('.desktop-pane-header')!).getByRole('button', { name: 'Copy session id 8e164800' });
    expect(button).toHaveTextContent('8e164800');
    fireEvent.click(button);
    expect(write).toHaveBeenCalledWith(id);
  });
});
