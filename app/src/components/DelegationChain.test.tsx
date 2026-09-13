import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { createRef } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BuiltinDelegationRole } from '../types/generated';
import { DelegationChainProvider, DelegationChainTrigger, type ChainSession, type DelegationChainHandle } from './DelegationChain';

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
  it('previews every connected agent without selecting a session or moving focus', () => {
    setup();
    const terminal = screen.getByLabelText('Terminal');
    terminal.focus();
    fireEvent.pointerEnter(screen.getByTestId('delegation-chain-trigger-build'));
    const popup = screen.getByRole('dialog', { name: 'Delegation chain' });
    expect(terminal).toHaveFocus();
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

  it('closes a hover preview on Escape without taking the terminal key', () => {
    setup();
    const terminal = screen.getByLabelText('Terminal');
    const receiveKey = vi.fn();
    terminal.addEventListener('keydown', receiveKey);
    terminal.focus();
    fireEvent.pointerEnter(screen.getByTestId('delegation-chain-trigger-build'));

    expect(fireEvent.keyDown(terminal, { key: 'Escape' })).toBe(true);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(terminal).toHaveFocus();
    expect(receiveKey).toHaveBeenCalledOnce();
  });

  it('opens from the action controller and navigates with arrows and clicks', () => {
    const { ref, onSelect } = setup();
    act(() => ref.current?.open('build'));
    const popup = screen.getByRole('dialog');
    const current = within(popup).getByRole('button', { name: /Build navigator/ });
    act(() => current.focus());
    fireEvent.keyDown(current, { key: 'ArrowUp' });
    expect(within(popup).getByRole('button', { name: /Coordinate identity/ })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: 'End' });
    const reviewer = within(popup).getByRole('button', { name: /Review behavior/ });
    expect(reviewer).toHaveFocus();
    fireEvent.click(reviewer);
    expect(onSelect).toHaveBeenCalledExactlyOnceWith('review');
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

  it('shows standalone assigned roles but leaves unrelated roleless sessions unmarked', () => {
    const { container } = render(<><DelegationChainTrigger session={sessions[0]} /><DelegationChainTrigger session={{ id: 'plain', label: 'plain' }} /></>);
    expect(screen.getByRole('button')).toHaveAccessibleName('Orchestrator · Show delegation chain for Coordinate identity');
    expect(container.querySelectorAll('button')).toHaveLength(1);
    expect(container.querySelector('[data-role="orchestrator"] svg')).toBeInTheDocument();
  });
});
