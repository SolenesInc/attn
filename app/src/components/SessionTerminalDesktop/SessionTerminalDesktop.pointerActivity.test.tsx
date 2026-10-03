import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SessionTerminalDesktop } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalDesktopState } from '../../types/desktop';

vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal(
      props: { onPointerActivity?: () => void },
      _ref: React.Ref<unknown>,
    ) {
      return <div data-testid="terminal" onMouseMove={props.onPointerActivity} />;
    }),
  };
});

const terminalState: TerminalDesktopState = {
  agents: [{ id: 'pane-1', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'agent' }],
  layoutTree: { type: 'pane', paneId: 'pane-1' },
};

describe('SessionTerminalDesktop pointer activity', () => {
  it("attributes terminal movement to that pane's terminal", () => {
    const onTerminalPointerActivity = vi.fn();
    render(
      <SessionTerminalDesktop
        desktopId="desktop-1"
        desktopSessions={[{ id: 'sess-1', label: 'agent', agent: 'claude', cwd: '/tmp/project' }]}
        terminalState={terminalState}
        activePaneId="pane-1"
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={createPaneRuntimeEventRouterController()}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
        onTerminalPointerActivity={onTerminalPointerActivity}
      />,
    );

    fireEvent.mouseMove(screen.getByTestId('terminal'));

    expect(onTerminalPointerActivity).toHaveBeenCalledWith('rt-1');
  });
});
