import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Sidebar } from './Sidebar';
import { buildWorkspaceViewModels } from '../utils/workspaceViewModels';
import { buildQueueBands } from '../utils/queueBands';

const baseProps = {
  selectedId: null,
  selectedWorkspaceId: null,
  collapsed: false,
  headerActions: [],
  onSelectSession: vi.fn(),
  onSelectWorkspace: vi.fn(),
  onNewSession: vi.fn(),
  onCloseSession: vi.fn(),
  onReloadSession: vi.fn(),
  onGoToDashboard: vi.fn(),
  onToggleCollapse: vi.fn(),
};

const sessions = [
  { id: 'claude', agent: 'claude', label: 'Investigate logs' },
  { id: 'codex', agent: 'codex', label: 'Implement sidebar logos' },
  { id: 'pi', agent: 'pi', label: 'Check rendering' },
  { id: 'copilot', agent: 'copilot', label: 'Review changes' },
  { id: 'shell', agent: 'shell', label: 'Run tests' },
  { id: 'plugin', agent: 'custom-driver', label: 'Plugin session' },
  { id: 'missing', agent: undefined, label: 'Older session' },
].map((session) => ({ ...session, state: 'idle' as const, workspaceId: 'workspace' }));

function sidebarData(muted = false, members = false) {
  const workspaces = buildWorkspaceViewModels(
    [{ id: 'workspace', title: 'attn', directory: '/repo/attn', muted }],
    sessions.map((session) => ({
      ...session,
      chiefOfStaff: members && session.id === 'claude',
      crewMember: members && session.id === 'pi' ? 'fern' : undefined,
    })),
  );
  return {
    workspaces,
    visualOrder: workspaces,
    visualIndexByWorkspaceId: new Map([['workspace', 0]]),
  };
}

describe('sidebar harness identity', () => {
  it.each([false, true])('shows harnesses and fallbacks in workspace rows (muted=%s)', (muted) => {
    const data = sidebarData(muted);
    const onSelectSession = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...data}
        workspaces={muted ? [] : data.workspaces}
        visualOrder={muted ? [] : data.visualOrder}
        onSelectSession={onSelectSession}
        mutedWorkspaces={muted ? data.workspaces : []}
        mutedExpanded
      />,
    );
    for (const [id, name] of [
      ['claude', 'Claude'], ['codex', 'Codex'], ['pi', 'Pi'], ['copilot', 'Copilot'],
      ['shell', 'Shell'], ['plugin', 'Custom Driver'], ['missing', 'Unknown harness'],
    ]) {
      const row = screen.getByTestId(`sidebar-session-${id}`);
      const icon = within(row).getByRole('img', { name });
      expect(icon).toHaveAttribute('title', name);
      expect(row.querySelector('.state-indicator')).toBeInTheDocument();
      fireEvent.click(icon);
      expect(onSelectSession).toHaveBeenLastCalledWith(id);
    }
  });

  it('keeps harness identity when switching between workspace and queue arrangements', () => {
    const data = sidebarData(false, true);
    const props = { ...baseProps, ...data, crew: [{ id: 'fern' }, { id: 'sleeping' }] };
    const { rerender } = render(<Sidebar {...props} queue={buildQueueBands(data.workspaces)} />);
    expect(within(screen.getByTestId('queue-crew-fern')).getByRole('img', { name: 'Pi' })).toBeInTheDocument();
    expect(within(screen.getByTestId('queue-crew-sleeping')).queryByRole('img')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open Implement sidebar logos' })).toHaveAttribute('title', 'Codex');
    expect(screen.getByRole('img', { name: 'Claude' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'Custom Driver' })).toBeInTheDocument();
    rerender(<Sidebar {...props} queue={null} />);
    expect(within(screen.getByTestId('sidebar-session-pi')).getByRole('img', { name: 'Pi' })).toBeInTheDocument();
    expect(within(screen.getByTestId('sidebar-session-codex')).getByRole('img', { name: 'Codex' })).toBeInTheDocument();
  });
});
