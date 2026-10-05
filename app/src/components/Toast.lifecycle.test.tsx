import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Toast, useToast } from './Toast';

vi.mock('../contexts/DaemonApiContext', () => ({ useDaemonApi: () => ({}) }));

afterEach(() => { vi.useRealTimers(); });

function Notifications() {
  const { showError } = useToast();
  return <><button onClick={() => showError('Could not save')}>Fail</button><button onClick={() => showError('Could not open')}>Fail again</button><Toast /></>;
}

describe('grouped error toast', () => {
  it('groups errors, restarts the fade and starts fresh after fading', () => {
    vi.useFakeTimers();
    render(<Notifications />);
    fireEvent.click(screen.getByText('Fail'));
    act(() => { vi.advanceTimersByTime(5000); });
    fireEvent.click(screen.getByText('Fail again'));
    expect(screen.getByRole('alert')).toHaveTextContent('2 notifications');
    expect(screen.getByRole('alert')).toHaveTextContent('Could not save');
    expect(screen.getByRole('alert')).toHaveTextContent('Could not open');
    act(() => { vi.advanceTimersByTime(5999); });
    expect(screen.getByRole('alert')).toHaveClass('visible');
    act(() => { vi.advanceTimersByTime(1); });
    expect(screen.getByRole('alert')).not.toHaveClass('visible');
    fireEvent.click(screen.getByText('Fail'));
    expect(screen.getByRole('alert')).toHaveClass('visible');
    expect(screen.getByRole('alert')).not.toHaveTextContent('Could not open');
    act(() => { vi.advanceTimersByTime(6000); });
    act(() => { vi.advanceTimersByTime(150); });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows the error without the attn subtitle or dismissal controls', () => {
    render(<Notifications />);
    fireEvent.click(screen.getByText('Fail'));
    const toast = screen.getByRole('alert');
    expect(toast).toHaveTextContent('Could not save');
    expect(toast).not.toHaveTextContent('attn');
    expect(toast).not.toHaveTextContent('fades');
    expect(screen.queryByRole('button', { name: 'Dismiss notifications' })).not.toBeInTheDocument();
  });

  it('pauses while focused and fades away after focus leaves', () => {
    vi.useFakeTimers();
    render(<Notifications />);
    fireEvent.click(screen.getByText('Fail'));
    const toast = screen.getByRole('alert');
    act(() => { toast.focus(); });
    expect(toast).toHaveFocus();
    act(() => { vi.advanceTimersByTime(12000); });
    expect(toast).toHaveClass('visible');
    expect(toast).not.toHaveTextContent('paused');
    act(() => { screen.getByText('Fail').focus(); });
    act(() => { vi.advanceTimersByTime(6000); });
    act(() => { vi.advanceTimersByTime(150); });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('pauses while hovered and fades away after leaving without showing pause text', () => {
    vi.useFakeTimers();
    render(<Notifications />);
    fireEvent.click(screen.getByText('Fail'));
    const toast = screen.getByRole('alert');
    fireEvent.mouseEnter(toast);
    act(() => { vi.advanceTimersByTime(12000); });
    expect(toast).toHaveClass('visible');
    expect(toast).not.toHaveTextContent('paused');
    fireEvent.mouseLeave(toast);
    act(() => { vi.advanceTimersByTime(6000); });
    act(() => { vi.advanceTimersByTime(150); });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});
