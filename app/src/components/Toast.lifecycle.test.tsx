import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Toast, useToast } from './Toast';

vi.mock('../contexts/DaemonApiContext', () => ({ useDaemonApi: () => ({}) }));

afterEach(() => { vi.useRealTimers(); });

function Notifications() {
  const { showNotice, showError } = useToast();
  return <><button onClick={() => showNotice('Saved')}>Notify</button><button onClick={() => showError('Could not save')}>Fail</button><Toast /></>;
}

describe('grouped toast', () => {
  it('absorbs notices and errors, restarts the fade and starts fresh after dismissal', () => {
    vi.useFakeTimers();
    render(<Notifications />);
    fireEvent.click(screen.getByText('Notify'));
    act(() => { vi.advanceTimersByTime(5000); });
    fireEvent.click(screen.getByText('Fail'));
    expect(screen.getByRole('alert')).toHaveTextContent('2 notifications');
    expect(screen.getByRole('alert')).toHaveTextContent('Saved');
    act(() => { vi.advanceTimersByTime(5999); });
    expect(screen.getByRole('alert')).toHaveClass('visible');
    act(() => { vi.advanceTimersByTime(1); });
    expect(screen.getByRole('alert')).not.toHaveClass('visible');
    fireEvent.click(screen.getByText('Notify'));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Saved');
    fireEvent.click(screen.getByLabelText('Dismiss notifications'));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Fail'));
    expect(screen.getByRole('alert')).not.toHaveTextContent('Saved');
  });

  it('resets a paused group after dismissal', () => {
    vi.useFakeTimers();
    render(<Notifications />);
    fireEvent.click(screen.getByText('Notify'));
    fireEvent.mouseEnter(screen.getByRole('status'));
    fireEvent.click(screen.getByLabelText('Dismiss notifications'));
    fireEvent.click(screen.getByText('Fail'));
    act(() => { vi.advanceTimersByTime(6000); });
    expect(screen.getByRole('alert')).not.toHaveClass('visible');
  });

  it('pauses while hovered or focused, and fades away after leaving', () => {
    vi.useFakeTimers();
    render(<Notifications />);
    fireEvent.click(screen.getByText('Notify'));
    const toast = screen.getByRole('status');
    fireEvent.mouseEnter(toast);
    act(() => { vi.advanceTimersByTime(12000); });
    expect(toast).toHaveClass('visible');
    fireEvent.mouseLeave(toast);
    fireEvent.focus(screen.getByLabelText('Dismiss notifications'));
    act(() => { vi.advanceTimersByTime(12000); });
    expect(toast).toHaveClass('visible');
    fireEvent.blur(screen.getByLabelText('Dismiss notifications'));
    act(() => { vi.advanceTimersByTime(6000); });
    act(() => { vi.advanceTimersByTime(150); });
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});
