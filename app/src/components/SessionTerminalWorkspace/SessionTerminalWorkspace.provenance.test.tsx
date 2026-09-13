import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalWorkspaceState } from '../../types/workspace';
import type { SessionPullRequest } from '../../types/generated';
import { BuiltinDelegationRole } from '../../types/generated';
import { DelegationChainProvider, type ChainSession } from '../DelegationChain';

vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));

// The terminal surface pulls in the Ghostty WASM model; stub it so the import
// graph stays light in jsdom.
vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

function loneAgentWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-1', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-1' },
  };
}

function renderPane(
  pullRequests: SessionPullRequest[],
  delegationSessions: ChainSession[] = [],
  onSelectSession = vi.fn(),
) {
  const view = (currentDelegationSessions: ChainSession[]) => (
    <DelegationChainProvider sessions={currentDelegationSessions} onSelectSession={onSelectSession}>
    <SessionTerminalWorkspace
      workspaceId="workspace-1"
      workspaceSessions={[{
        id: 'sess-1',
        label: 'ledger sweep',
        agent: 'shell',
        cwd: '/tmp/project',
        pullRequests,
      }]}
      delegationSessions={currentDelegationSessions}
      workspace={loneAgentWorkspace()}
      activePaneId="pane-1"
      fontSize={13}
      enabled
      isActiveSession
      eventRouter={createPaneRuntimeEventRouterController()}
      onSplitPane={vi.fn()}
      onClosePane={vi.fn()}
      onFocusPane={vi.fn()}
      onSelectSession={onSelectSession}
      onNavigateOutOfSession={vi.fn()}
    />
    </DelegationChainProvider>
  );
  const rendered = render(view(delegationSessions));
  return { ...rendered, updateDelegation: (next: ChainSession[]) => rendered.rerender(view(next)) };
}

describe('SessionTerminalWorkspace provenance line', () => {
  it('refreshes a mounted header when delegation metadata arrives', () => {
    const session: ChainSession = { id: 'sess-1', label: 'ledger sweep', agent: 'codex', state: 'idle' };
    const { updateDelegation } = renderPane([], [session]);
    expect(screen.queryByTestId('delegation-chain-trigger-sess-1')).not.toBeInTheDocument();
    updateDelegation([{ ...session, delegation_role: { name: 'Orchestrator', builtin: BuiltinDelegationRole.Orchestrator } }]);
    expect(screen.getByTestId('delegation-chain-trigger-sess-1')).toHaveTextContent('Orchestrator');
  });
  it('passes the pane delegation links through the header', () => {
    const onSelectSession = vi.fn();
    renderPane([], [
      { id: 'dispatcher', label: 'docs sweep', agent: 'claude', state: 'idle' },
      {
        id: 'sess-1',
        label: 'ledger sweep',
        agent: 'shell',
        state: 'working',
        dispatcher_session_id: 'dispatcher',
        delegation_role: { name: 'Orchestrator', builtin: BuiltinDelegationRole.Orchestrator },
      },
      {
        id: 'delegate',
        label: 'glossary rework',
        agent: 'codex',
        state: 'working',
        dispatcher_session_id: 'sess-1',
      },
    ], onSelectSession);

    const trigger = screen.getByRole('button', { name: /Orchestrator · Show delegation chain/ });
    expect(trigger.closest('.workspace-pane-identity-main')).not.toBeNull();
    expect(trigger).toHaveTextContent('Orchestrator');
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole('button', { name: /docs sweep/ }));
    expect(onSelectSession).toHaveBeenCalledWith('dispatcher');

    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole('button', { name: /glossary rework/ }));
    expect(onSelectSession).toHaveBeenLastCalledWith('delegate');
  });

  it('carries the session PR on the pane header', () => {
    renderPane([{
      repository: 'github.com/victorarias/attn',
      number: 71,
      url: 'https://github.com/victorarias/attn/pull/71',
      title: 'feat(garden): sweep the agent ledger nightly',
      created_at: '2026-08-30T12:00:00Z',
      state: 'open',
      status_fetched_at: '2026-08-30T12:05:00Z',
      ci_status: 'failure',
    }]);

    const header = document.querySelector('.workspace-pane-header');
    expect(header).not.toBeNull();
    expect(header?.textContent).toContain('attn#71');
    expect(header?.textContent).toContain('checks failed');
  });

  it('opens the popover from the header entry', () => {
    renderPane([{
      repository: 'github.com/victorarias/attn',
      number: 71,
      url: 'https://github.com/victorarias/attn/pull/71',
      created_at: '2026-08-30T12:00:00Z',
      state: 'open',
    }]);

    fireEvent.click(screen.getByTestId('session-provenance-pr'));
    expect(screen.getByTestId('session-pr-popover')).toBeInTheDocument();
  });

  it('hands the open popover from delegates to PR details', () => {
    renderPane([{
      repository: 'github.com/victorarias/attn',
      number: 71,
      url: 'https://github.com/victorarias/attn/pull/71',
      created_at: '2026-08-30T12:00:00Z',
      state: 'open',
    }], [
      { id: 'sess-1', label: 'ledger sweep', agent: 'shell', state: 'working' },
      {
        id: 'delegate',
        label: 'glossary rework',
        agent: 'codex',
        state: 'working',
        dispatcher_session_id: 'sess-1',
      },
    ]);

    fireEvent.click(screen.getByRole('button', { name: 'Show delegation chain for ledger sweep' }));
    expect(screen.getByTestId('delegation-chain-popover')).toBeInTheDocument();

    fireEvent.pointerEnter(screen.getByTestId('session-provenance-pr'));
    expect(screen.getByTestId('delegation-chain-popover')).toBeInTheDocument();
    expect(screen.queryByTestId('session-pr-popover')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('session-provenance-pr'));
    expect(screen.queryByTestId('delegation-chain-popover')).not.toBeInTheDocument();
    expect(screen.getByTestId('session-pr-popover')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Show delegation chain for ledger sweep' }));
    expect(screen.queryByTestId('session-pr-popover')).not.toBeInTheDocument();
    expect(screen.getByTestId('delegation-chain-popover')).toBeInTheDocument();
  });
});
