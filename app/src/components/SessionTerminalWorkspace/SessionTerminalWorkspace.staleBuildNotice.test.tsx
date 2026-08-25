import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalWorkspaceState } from '../../types/workspace';

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

function renderPane(terminalBuildStale: boolean) {
  return render(
    <SessionTerminalWorkspace
      workspaceId="workspace-1"
      workspaceSessions={[{
        id: 'sess-1',
        label: 'shell',
        agent: 'shell',
        cwd: '/tmp/project',
        terminalBuildStale,
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
}

describe('SessionTerminalWorkspace stale terminal build notice', () => {
  it('says nothing when the worker was built against the same terminal', () => {
    renderPane(false);
    expect(screen.queryByTestId('terminal-stale-build-notice')).toBeNull();
  });

  it('offers the reload when the daemon says the worker is a build behind', () => {
    renderPane(true);
    const notice = screen.getByTestId('terminal-stale-build-notice');
    expect(notice.textContent).toContain('older terminal');
    expect(notice.textContent).toContain('Reload');
  });

  it('stays dismissed for the rest of the session', () => {
    renderPane(true);
    fireEvent.click(screen.getByTestId('terminal-stale-build-notice-dismiss'));
    expect(screen.queryByTestId('terminal-stale-build-notice')).toBeNull();
  });
});
