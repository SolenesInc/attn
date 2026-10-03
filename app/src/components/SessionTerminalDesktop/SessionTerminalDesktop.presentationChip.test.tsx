import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SessionTerminalDesktop } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalDesktopState } from '../../types/desktop';
import type { Presentation } from '../../types/generated';

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

function loneAgentDesktop(): TerminalDesktopState {
  return {
    agents: [{ id: 'pane-1', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-1' },
  };
}

function makePresentation(overrides: Partial<Presentation> = {}): Presentation {
  return {
    id: 'pres-1',
    created_at: '2026-07-01T00:00:00Z',
    kind: 'pr',
    latest_round_seq: 1,
    latest_round_submitted: false,
    repo_path: '/repo',
    session_id: 'sess-1',
    status: 'open',
    title: 'My presentation',
    ...overrides,
  };
}

describe('SessionTerminalDesktop presentation chip', () => {
  it('renders the chip in the header when the pane session has a presentation', () => {
    const onOpenPresentation = vi.fn();
    render(
      <SessionTerminalDesktop
        desktopId="desktop-1"
        desktopSessions={[{
          id: 'sess-1',
          label: 'shell',
          agent: 'shell',
          cwd: '/tmp/project',
          presentation: makePresentation(),
        }]}
        terminalState={loneAgentDesktop()}
        activePaneId="pane-1"
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={createPaneRuntimeEventRouterController()}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
        onOpenPresentation={onOpenPresentation}
      />,
    );

    const header = document.querySelector('.desktop-pane-header');
    expect(header?.className).not.toContain('desktop-pane-header-hidden');

    const chip = screen.getByRole('button', { name: /review/i });
    expect(chip).toHaveAttribute('title', 'My presentation');
    fireEvent.click(chip);
    expect(onOpenPresentation).toHaveBeenCalledWith('pres-1');
  });

  it('keeps the header and its session name on a lone tile with no presentation and no nudge', () => {
    render(
      <SessionTerminalDesktop
        desktopId="desktop-1"
        desktopSessions={[{ id: 'sess-1', label: 'shell', agent: 'shell', cwd: '/tmp/project' }]}
        terminalState={loneAgentDesktop()}
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

    const header = document.querySelector('.desktop-pane-header');
    expect(header?.className).toContain('desktop-pane-header--static');
    expect(document.querySelector('.desktop-pane-title')?.textContent).toBe('shell');
    expect(screen.queryByRole('button', { name: /review/i })).not.toBeInTheDocument();
  });
});
