import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Sidebar } from './Sidebar';
import { desktopGroups } from '../test/desktops';
import { buildQueueBands } from '../utils/queueBands';
import { BuiltinDelegationRole, type SessionDelegationRole } from '../types/generated';

const baseProps = {
  selectedId: null,
  selectedDesktopId: null,
  collapsed: false,
  surface: 'tree-open' as const,
  headerActions: [],
  onSelectSession: vi.fn(),
  onSelectDesktop: vi.fn(),
  onNewSession: vi.fn(),
  onCloseSession: vi.fn(),
  onReloadSession: vi.fn(),
  onGoToDashboard: vi.fn(),
  onToggleCollapse: vi.fn(),
};

const sessions: Array<{
  id: string;
  agent?: string;
  label: string;
  state: 'idle' | 'working' | 'scheduled' | 'unknown' | 'recoverable';
  desktopId: string;
  state_reason?: string;
  isWorktree?: boolean;
  delegation_role?: SessionDelegationRole;
}> = [
  { id: 'claude', agent: 'claude', label: 'Investigate logs' },
  { id: 'codex', agent: 'codex', label: 'Implement sidebar logos' },
  { id: 'pi', agent: 'pi', label: 'Check rendering' },
  { id: 'copilot', agent: 'copilot', label: 'Review changes' },
  { id: 'shell', agent: 'shell', label: 'Run tests' },
  { id: 'plugin', agent: 'custom-driver', label: 'Plugin session' },
  { id: 'missing', agent: undefined, label: 'Older session' },
].map((session) => ({ ...session, state: 'idle' as const, desktopId: 'desktop' }));

function sidebarData(members = false) {
  const desktops = desktopGroups(
    [{ id: 'desktop', title: 'attn' }],
    sessions.map((session) => ({
      ...session,
      chiefOfStaff: members && session.id === 'claude',
      crewMember: members && session.id === 'pi' ? 'fern' : undefined,
    })),
  );
  return { desktops, visualIndexByDesktopId: new Map([['desktop', 0]]) };
}

describe('sidebar harness identity', () => {
  it('shows harnesses and fallbacks in desktop rows', () => {
    const data = sidebarData();
    const onSelectSession = vi.fn();
    render(<Sidebar {...baseProps} {...data} onSelectSession={onSelectSession} />);
    for (const [id, name] of [
      ['claude', 'Claude'], ['codex', 'Codex'], ['pi', 'Pi'], ['copilot', 'Copilot'],
      ['shell', 'Shell'], ['plugin', 'Custom Driver'], ['missing', 'Unknown harness'],
    ]) {
      const row = screen.getByTestId(`sidebar-session-${id}`);
      const icon = within(row).getByRole('img', { name: `${name} · idle` });
      expect(icon).toHaveAttribute('title', name);
      expect(row.querySelector('.session-lead')).toHaveAttribute('data-state', 'idle');
      const select = within(row).getByRole('button', { name: /^Open / });
      expect(select).toHaveAttribute('title', name);
      fireEvent.click(select);
      expect(onSelectSession).toHaveBeenLastCalledWith(id);
    }
  });

  it('shows state in the lead, keeps delegation visible, and omits the worktree glyph', () => {
    const data = sidebarData();
    data.desktops[0].sessions[0].state = 'working';
    data.desktops[0].sessions[0].isWorktree = true;
    data.desktops[0].sessions[0].delegation_role = { name: 'Builder', builtin: BuiltinDelegationRole.Builder };
    render(<Sidebar {...baseProps} {...data} />);

    const row = screen.getByTestId('sidebar-session-claude');
    expect(row.querySelector('.session-lead')).toHaveAttribute('data-state', 'working');
    expect(within(row).getByRole('img', { name: 'Claude · working' })).toBeInTheDocument();
    expect(within(row).getByTestId('delegation-chain-trigger-claude')).toBeInTheDocument();
    expect(row).not.toHaveTextContent('⎇');
  });

  it('shows a state dot in the lead with harness logos off', () => {
    render(<Sidebar {...baseProps} {...sidebarData()} harnessLogosEnabled={false} />);
    const row = screen.getByTestId('sidebar-session-claude');
    expect(row.querySelector('.session-lead .state-indicator')).toBeInTheDocument();
    expect(row.querySelector('.session-lead')).toHaveAttribute('data-state', 'idle');
  });

  it('names the scheduled state with harness logos on', () => {
    const data = sidebarData();
    data.desktops[0].sessions[0].state = 'scheduled';
    render(<Sidebar {...baseProps} {...data} />);
    const row = screen.getByTestId('sidebar-session-claude');
    expect(within(row).getByRole('img', { name: 'Claude · scheduled' })).toBeInTheDocument();
  });

  it('explains an unknown state on the hit-tested row button', () => {
    const data = sidebarData();
    data.desktops[0].sessions[0].state = 'unknown';
    data.desktops[0].sessions[0].state_reason = 'stuck';
    render(<Sidebar {...baseProps} {...data} />);
    const row = screen.getByTestId('sidebar-session-claude');
    expect(within(row).getByRole('img', { name: 'Claude · Stuck — the agent has stopped reporting anything at all' })).toHaveAttribute(
      'title', 'Claude · Stuck — the agent has stopped reporting anything at all',
    );
    expect(within(row).getByRole('button', { name: 'Open Investigate logs' })).toHaveAttribute(
      'title', 'Claude · Stuck — the agent has stopped reporting anything at all',
    );
  });

  it.each([
    ['recoverable', undefined, 'Claude · Session will be recovered when opened'],
    ['unknown', 'some_future_clause', 'Claude'],
  ] as const)('keeps the %s row tooltip without inventing a reason', (state, reason, title) => {
    const data = sidebarData();
    data.desktops[0].sessions[0].state = state;
    data.desktops[0].sessions[0].state_reason = reason;
    render(<Sidebar {...baseProps} {...data} />);
    const row = screen.getByTestId('sidebar-session-claude');
    expect(within(row).getByRole('button', { name: 'Open Investigate logs' })).toHaveAttribute('title', title);
  });

  it('keeps harness identity when switching between desktop and queue arrangements', () => {
    const data = sidebarData(true);
    const props = { ...baseProps, ...data, crew: [{ id: 'fern' }, { id: 'sleeping' }] };
    const { rerender } = render(<Sidebar {...props} agentListOpen queue={buildQueueBands(data.desktops)} />);
    expect(within(screen.getByTestId('queue-crew-fern')).getByRole('img', { name: 'Pi · idle' })).toBeInTheDocument();
    expect(within(screen.getByTestId('queue-crew-sleeping')).getByRole('img', { name: 'Unknown harness · idle' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open Implement sidebar logos' })).toHaveAttribute('title', expect.stringContaining('Codex'));
    expect(screen.getByRole('img', { name: 'Claude · idle' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'Custom Driver · idle' })).toBeInTheDocument();
    rerender(<Sidebar {...props} queue={null} />);
    expect(within(screen.getByTestId('sidebar-session-pi')).getByRole('img', { name: 'Pi · idle' })).toBeInTheDocument();
    expect(within(screen.getByTestId('sidebar-session-codex')).getByRole('img', { name: 'Codex · idle' })).toBeInTheDocument();
  });

  it.each([false, true])('keeps crew management reachable when queue mode is %s', (queueMode) => {
    const data = sidebarData(true);
    const onManageCrew = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...data}
        crew={[{ id: 'fern' }, { id: 'sleeping' }]}
        queue={queueMode ? buildQueueBands(data.desktops) : null}
        onManageCrew={onManageCrew}
      />,
    );

    expect(screen.getByTestId('manage-crew')).toHaveTextContent('manage');
    fireEvent.click(screen.getByTestId('manage-crew'));
    expect(onManageCrew).toHaveBeenCalledOnce();
  });

  it('hides every harness logo while preserving queue row hover text', () => {
    const data = sidebarData(true);
    render(
      <Sidebar
        {...baseProps}
        {...data}
        crew={[{ id: 'fern' }, { id: 'sleeping' }]}
        queue={buildQueueBands(data.desktops)}
        agentListOpen
        harnessLogosEnabled={false}
      />,
    );

    expect(screen.getByTestId('queue-select-codex')).toHaveAttribute('title', expect.stringContaining('Codex'));
    expect(document.querySelector('.sidebar')).toHaveClass('sidebar--hide-harness-logos');
    expect(document.querySelectorAll('.sidebar-harness-icon')).not.toHaveLength(0);
  });
});
