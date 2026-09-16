import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createRef } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BuiltinDelegationRole } from '../types/generated';
import { DelegationChainProvider, DelegationChainTrigger, SessionRoleIcon, type ChainSession, type DelegationChainHandle } from './DelegationChain';
import { DelegationRoleIcon } from './DelegationRoleIcon';
import { SessionLabel } from './SessionLabel';

const sessions: ChainSession[] = [
  { id: 'root', label: 'Coordinate identity', agent: 'claude', state: 'idle', delegation_role: { name: 'Orchestrator', builtin: BuiltinDelegationRole.Orchestrator } },
  { id: 'build', label: 'Build navigator', agent: 'codex', state: 'working', dispatcher_session_id: 'root', delegation_role: { name: 'Builder', builtin: BuiltinDelegationRole.Builder } },
  { id: 'review', label: 'Review behavior', agent: 'codex', state: 'idle', dispatcher_session_id: 'root' },
];

function setup() {
  const onSelect = vi.fn();
  const ref = createRef<DelegationChainHandle>();
  const view = render(
    <DelegationChainProvider ref={ref} sessions={sessions} onSelectSession={onSelect}>
      <input aria-label="Terminal" />
      {sessions.map((session) => <DelegationChainTrigger key={session.id} session={session} />)}
    </DelegationChainProvider>,
  );
  return { ...view, onSelect, ref };
}

afterEach(() => vi.useRealTimers());

describe('delegation chain', () => {
  it('uses the maintained Reviewer icon for legacy snapshots and preserves explicit icons', () => {
    const role = { name: 'Reviewer', builtin: BuiltinDelegationRole.Reviewer };
    expect(renderToStaticMarkup(<SessionRoleIcon role={role} />))
      .toBe(renderToStaticMarkup(<DelegationRoleIcon icon="list" name="Reviewer" />));
    expect(renderToStaticMarkup(<SessionRoleIcon role={{ ...role, icon: 'diamond' }} />))
      .toBe(renderToStaticMarkup(<DelegationRoleIcon icon="diamond" name="Reviewer" />));
  });

  it('focuses the current agent immediately on hover without selecting a session', () => {
    const { onSelect } = setup();
    const terminal = screen.getByLabelText('Terminal');
    terminal.focus();
    fireEvent.pointerEnter(screen.getByTestId('delegation-chain-trigger-build'));
    const popup = screen.getByRole('dialog', { name: 'Delegation chain' });
    expect(within(popup).getByRole('button', { name: /Build navigator/ })).toHaveFocus();
    expect(onSelect).not.toHaveBeenCalled();
    expect(within(popup).getByText('Orchestrator')).toBeInTheDocument();
    expect(within(popup).getByText('Builder')).toBeInTheDocument();
    expect(within(popup).getByRole('button', { name: /Review behavior/ })).toHaveTextContent('Codex');
    expect(within(popup).getByRole('button', { name: /Build navigator/ })).toHaveAttribute('aria-current', 'true');
  });

  it('keeps the hover card reachable and pins it on click', () => {
    vi.useFakeTimers();
    setup();
    const trigger = screen.getByTestId('delegation-chain-trigger-build');
    fireEvent.pointerEnter(trigger);
    fireEvent.pointerLeave(trigger);
    fireEvent.pointerEnter(screen.getByRole('dialog'));
    act(() => vi.runOnlyPendingTimers());
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    fireEvent.click(trigger);
    fireEvent.pointerLeave(screen.getByRole('dialog'));
    act(() => vi.runOnlyPendingTimers());
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('keeps a pinned trigger attached across callback updates and uses the latest callback', () => {
    const first = vi.fn();
    const latest = vi.fn();
    const content = (onOpen: () => void) => (
      <DelegationChainProvider sessions={sessions} onSelectSession={vi.fn()}>
        <DelegationChainTrigger session={sessions[1]} variant="header" onOpen={onOpen} />
      </DelegationChainProvider>
    );
    const { rerender } = render(content(first));
    const trigger = screen.getByTestId('delegation-chain-trigger-build');
    fireEvent.click(trigger);
    const popup = screen.getByRole('dialog');
    rerender(content(latest));
    expect(screen.getByRole('dialog')).toBe(popup);
    fireEvent.click(trigger);
    expect(first).toHaveBeenCalledOnce();
    expect(latest).toHaveBeenCalledOnce();
  });

  it('consumes Escape from a hover card and restores the terminal', async () => {
    setup();
    const terminal = screen.getByLabelText('Terminal');
    const receiveKey = vi.fn();
    terminal.addEventListener('keydown', receiveKey);
    terminal.focus();
    fireEvent.pointerEnter(screen.getByTestId('delegation-chain-trigger-build'));

    expect(fireEvent.keyDown(document.activeElement!, { key: 'Escape' })).toBe(false);
    expect(screen.queryByRole('dialog')).toBeNull();
    await waitFor(() => expect(terminal).toHaveFocus());
    expect(receiveKey).not.toHaveBeenCalled();
  });

  it('opens from the action controller and navigates with arrows and clicks', async () => {
    const { ref, onSelect } = setup();
    act(() => ref.current?.open('build'));
    const popup = screen.getByRole('dialog');
    const current = within(popup).getByRole('button', { name: /Build navigator/ });
    expect(current).toHaveFocus();
    fireEvent.keyDown(current, { key: 'ArrowUp' });
    expect(within(popup).getByRole('button', { name: /Coordinate identity/ })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: 'End' });
    const reviewer = within(popup).getByRole('button', { name: /Review behavior/ });
    expect(reviewer).toHaveFocus();
    fireEvent.click(reviewer);
    await waitFor(() => expect(onSelect).toHaveBeenCalledExactlyOnceWith('review'));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('closes on outside pointer down, including targets that stop propagation', () => {
    setup();
    fireEvent.click(screen.getByTestId('delegation-chain-trigger-root'));
    const terminal = screen.getByLabelText('Terminal');
    terminal.addEventListener('pointerdown', (event) => event.stopPropagation());
    fireEvent.pointerDown(terminal);
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it.each(['hover', 'click', 'command'] as const)('restores focus after outside scrolling from %s entry', async (entry) => {
    const { ref } = setup();
    const terminal = screen.getByLabelText('Terminal');
    const trigger = screen.getByTestId('delegation-chain-trigger-build');
    terminal.focus();
    if (entry === 'hover') fireEvent.pointerEnter(trigger);
    else if (entry === 'click') fireEvent.click(trigger);
    else act(() => ref.current?.open('build', terminal));
    const popup = screen.getByRole('dialog');
    fireEvent.scroll(within(popup).getByRole('list'));
    expect(popup).toBeInTheDocument();
    expect(within(popup).getByRole('button', { name: /Build navigator/ })).toHaveFocus();
    fireEvent.scroll(terminal);
    expect(screen.queryByRole('dialog')).toBeNull();
    await waitFor(() => expect(entry === 'click' ? trigger : terminal).toHaveFocus());
  });

  it('shows standalone assigned roles but leaves unrelated roleless sessions unmarked', () => {
    const { container } = render(<><DelegationChainTrigger session={sessions[0]} /><DelegationChainTrigger session={{ id: 'plain', label: 'plain' }} /></>);
    expect(screen.getByRole('button')).toHaveAccessibleName('Orchestrator · Show delegation chain for Coordinate identity');
    expect(container.querySelectorAll('button')).toHaveLength(1);
    expect(container.querySelector('[data-role="orchestrator"] svg')).toBeInTheDocument();
  });

  it('uses one full-title and chain card for the row and role cue', () => {
    render(
      <DelegationChainProvider sessions={sessions} onSelectSession={vi.fn()}>
        <div className="sidebar">
          <div className="session-item" data-testid="row">
            <SessionLabel label={sessions[1].label} session={sessions[1]} />
            <DelegationChainTrigger session={sessions[1]} />
          </div>
        </div>
      </DelegationChainProvider>,
    );
    const label = screen.getByTestId('row').querySelector('.session-label')!;
    Object.defineProperty(label, 'scrollWidth', { value: 420 });
    Object.defineProperty(label, 'clientWidth', { value: 80 });
    fireEvent.pointerEnter(screen.getByTestId('row'));
    const popup = screen.getByRole('dialog');
    expect(popup.querySelector('.delegation-chain-heading')).toHaveTextContent(sessions[1].label);
    expect(within(popup).getByText('Orchestrator')).toBeInTheDocument();
    expect(screen.queryByTestId('session-label-reveal')).toBeNull();
    fireEvent.pointerEnter(screen.getByTestId('delegation-chain-trigger-build'));
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    expect(screen.queryByTestId('session-label-reveal')).toBeNull();
  });

  it('keeps header clicks and popup clicks out of the terminal pane mouse handler', () => {
    const focusPane = vi.fn();
    render(
      <DelegationChainProvider sessions={sessions} onSelectSession={vi.fn()}>
        <div onMouseDown={focusPane}>
          <DelegationChainTrigger session={sessions[1]} variant="header" />
        </div>
      </DelegationChainProvider>,
    );
    const trigger = screen.getByTestId('delegation-chain-trigger-build');
    fireEvent.mouseDown(trigger);
    fireEvent.click(trigger);
    const current = within(screen.getByRole('dialog')).getByRole('button', { name: /Build navigator/ });
    expect(current).toHaveFocus();
    fireEvent.mouseDown(current);
    expect(focusPane).not.toHaveBeenCalled();
  });

  it('does not reopen under a stationary pointer after an explicit dismissal', () => {
    setup();
    const trigger = screen.getByTestId('delegation-chain-trigger-build');
    fireEvent.pointerMove(window, { clientX: 80, clientY: 40 });
    fireEvent.pointerEnter(trigger);
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    fireEvent.pointerEnter(trigger);
    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.pointerMove(window, { clientX: 81, clientY: 40 });
    fireEvent.pointerEnter(trigger);
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('allows a new hover when pointer entry arrives before pointer movement', () => {
    setup();
    fireEvent.pointerMove(window, { clientX: 80, clientY: 40 });
    fireEvent.pointerEnter(screen.getByTestId('delegation-chain-trigger-build'));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    const next = screen.getByTestId('delegation-chain-trigger-root');
    fireEvent.pointerOut(screen.getByTestId('delegation-chain-trigger-build'), { clientX: 180, clientY: 40, relatedTarget: next });
    fireEvent.pointerEnter(next, { clientX: 180, clientY: 40 });
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: /Coordinate identity/ })).toHaveFocus();
  });

  it.each([false, true])('keeps a header hover open after its old row leaves (dismissed first: %s)', (dismissFirst) => {
    vi.useFakeTimers();
    render(
      <DelegationChainProvider sessions={sessions} onSelectSession={vi.fn()}>
        <div className="session-item" data-testid="row">
          <SessionLabel label={sessions[1].label} session={sessions[1]} />
        </div>
        <DelegationChainTrigger session={sessions[1]} variant="header" />
      </DelegationChainProvider>,
    );
    const row = screen.getByTestId('row');
    const header = screen.getByTestId('delegation-chain-trigger-build');
    fireEvent.pointerEnter(row, { clientX: 80, clientY: 40 });
    if (dismissFirst) fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    fireEvent.pointerOut(row, { clientX: 180, clientY: 40, relatedTarget: header });
    fireEvent.pointerEnter(header, { clientX: 180, clientY: 40, relatedTarget: row });
    fireEvent(row, new PointerEvent('pointerleave', { clientX: 180, clientY: 40, relatedTarget: header }));
    act(() => vi.runOnlyPendingTimers());
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: /Build navigator/ })).toHaveFocus();
    fireEvent.pointerLeave(header);
    act(() => vi.runOnlyPendingTimers());
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it.each(['role cue', 'row label'] as const)('dismisses when a queue update replaces the %s anchor', async (entry) => {
    const fallback = vi.fn(() => screen.getByLabelText('Terminal').focus());
    const content = (band: string) => (
      <DelegationChainProvider sessions={sessions} onSelectSession={vi.fn()} onRestoreFocusFallback={fallback}>
        <input aria-label="Terminal" />
        <div key={band} className="session-item" data-testid="row">
          <SessionLabel label={sessions[1].label} session={sessions[1]} />
          <DelegationChainTrigger session={sessions[1]} />
        </div>
      </DelegationChainProvider>
    );
    const view = render(content('your-turn'));
    screen.getByLabelText('Terminal').focus();
    if (entry === 'role cue') fireEvent.click(screen.getByTestId('delegation-chain-trigger-build'));
    else fireEvent.pointerEnter(screen.getByTestId('row'));
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    view.rerender(content('settled'));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(screen.getByLabelText('Terminal')).toHaveFocus());
    expect(screen.getByTestId('delegation-chain-trigger-build')).toHaveAttribute('aria-expanded', 'false');
    expect(fallback).toHaveBeenCalledTimes(entry === 'role cue' ? 1 : 0);
  });

  it.each(['navigation', 'overlay'] as const)('dismisses on %s without restoring stale focus or reopening afterward', async (change) => {
    const ref = createRef<DelegationChainHandle>();
    const fallback = vi.fn();
    const content = (navigationKey: string, blocked: boolean) => (
      <DelegationChainProvider ref={ref} sessions={sessions} onSelectSession={vi.fn()} navigationKey={navigationKey} blocked={blocked} onRestoreFocusFallback={fallback}>
        <input aria-label="Previous terminal" />
        <input aria-label="New focus target" />
        {navigationKey === 'session:build' && !blocked && <DelegationChainTrigger session={sessions[1]} />}
      </DelegationChainProvider>
    );
    const view = render(content('session:build', false));
    const previous = screen.getByLabelText('Previous terminal');
    previous.focus();
    act(() => ref.current?.open('build', previous));
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: /Build navigator/ })).toHaveFocus();
    const stealFocus = vi.fn();
    previous.addEventListener('focus', stealFocus);
    view.rerender(content(change === 'navigation' ? 'session:root' : 'session:build', change === 'overlay'));
    const target = screen.getByLabelText('New focus target');
    target.focus();
    expect(screen.queryByRole('dialog')).toBeNull();
    await waitFor(() => expect(target).toHaveFocus());
    view.rerender(content('session:build', false));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(stealFocus).not.toHaveBeenCalled();
    expect(fallback).not.toHaveBeenCalled();
  });
});
