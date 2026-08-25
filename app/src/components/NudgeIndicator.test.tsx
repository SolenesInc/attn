import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { deriveNudgeMode, SidebarNudgeBar, HeaderNudgeIndicator } from './NudgeIndicator';

const FIRES_AT = '2999-01-01T00:00:00.000Z';

describe('deriveNudgeMode', () => {
  it('returns null when there is no unread activity and no countdown', () => {
    expect(deriveNudgeMode({ state: 'idle', isActive: false })).toBeNull();
    expect(deriveNudgeMode({ state: 'working', isActive: true })).toBeNull();
  });

  it('counts down for an inactive session with a running countdown', () => {
    expect(
      deriveNudgeMode({ ticketUnread: true, nudgeFiresAt: FIRES_AT, state: 'idle', isActive: false }),
    ).toBe('counting');
  });

  it('pauses the selected idle/waiting session even while a stale fires_at is in flight', () => {
    expect(
      deriveNudgeMode({ ticketUnread: true, nudgeFiresAt: FIRES_AT, state: 'idle', isActive: true }),
    ).toBe('paused');
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'waiting_input', isActive: true }),
    ).toBe('paused');
  });

  it('lets the user deliver on demand even on a working session they are focused on', () => {
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'working', isActive: true }),
    ).toBe('paused');
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'working', isActive: false }),
    ).toBe('marker');
  });

  it('keeps a pending_approval session as a non-clickable marker', () => {
    // The one state a click must never reach: the doorbell's trailing Enter could
    // answer a y/n approval prompt, so even the focused session stays a marker.
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'pending_approval', isActive: true }),
    ).toBe('marker');
  });

  it('offers the clickable paused chip for an at-rest unknown session the user is on', () => {
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'unknown', isActive: true }),
    ).toBe('paused');
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'unknown', isActive: false }),
    ).toBe('marker');
  });

  it('falls back to the marker for the post-fire transient (unread, idle, inactive, no fires_at)', () => {
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'idle', isActive: false }),
    ).toBe('marker');
  });
});

describe('SidebarNudgeBar', () => {
  it('renders a non-interactive bar when counting', () => {
    const { container } = render(<SidebarNudgeBar mode="counting" firesAt={FIRES_AT} />);
    expect(container.querySelector('.nudge-sidebar-bar')).not.toBeNull();
    expect(container.querySelector('button')).toBeNull();
  });

  it('renders a static marker bar', () => {
    const { container } = render(<SidebarNudgeBar mode="marker" />);
    expect(container.querySelector('.nudge-sidebar-bar--marker')).not.toBeNull();
    expect(container.querySelector('button')).toBeNull();
  });

  it('renders a clickable strip when paused and triggers without bubbling to the row', () => {
    const onTrigger = vi.fn();
    const onRowClick = vi.fn();
    const onRowPointerDown = vi.fn();
    render(
      <div onClick={onRowClick} onPointerDown={onRowPointerDown}>
        <SidebarNudgeBar mode="paused" onTrigger={onTrigger} />
      </div>,
    );
    const button = screen.getByRole('button');
    fireEvent.pointerDown(button);
    expect(onRowPointerDown).not.toHaveBeenCalled();
    fireEvent.click(button);
    expect(onTrigger).toHaveBeenCalledTimes(1);
    expect(onRowClick).not.toHaveBeenCalled();
  });
});

describe('HeaderNudgeIndicator', () => {
  it('shows an incoming-nudge chip when counting, carrying the key that stops it', () => {
    const { container } = render(<HeaderNudgeIndicator mode="counting" firesAt={FIRES_AT} />);
    expect(container.querySelector('.nudge-header--counting')).not.toBeNull();
    expect(container.querySelector('.nudge-header-track')).not.toBeNull();
    expect(screen.getByText('Incoming ticket nudge…')).toBeTruthy();
    expect(container.querySelector('.countdown-cancel-hint-key')?.textContent).toBe('⌘.');
    expect(screen.getByText('stop')).toBeTruthy();
  });

  it('cancels the countdown on click without bubbling to the pane header', () => {
    const onCancel = vi.fn();
    const onPaneClick = vi.fn();
    const onPanePointerDown = vi.fn();
    render(
      <div onClick={onPaneClick} onPointerDown={onPanePointerDown}>
        <HeaderNudgeIndicator mode="counting" firesAt={FIRES_AT} onCancel={onCancel} />
      </div>,
    );
    const button = screen.getByRole('button', { name: /incoming ticket nudge/i });
    fireEvent.pointerDown(button);
    expect(onPanePointerDown).not.toHaveBeenCalled();
    fireEvent.click(button);
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onPaneClick).not.toHaveBeenCalled();
  });

  it('keeps counting and paused apart: a counting chip never delivers the nudge', () => {
    const onTrigger = vi.fn();
    const onCancel = vi.fn();
    render(
      <HeaderNudgeIndicator
        mode="counting"
        firesAt={FIRES_AT}
        onTrigger={onTrigger}
        onCancel={onCancel}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: /incoming ticket nudge/i }));
    expect(onTrigger).not.toHaveBeenCalled();
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('shows an unread-activity marker', () => {
    const { container } = render(<HeaderNudgeIndicator mode="marker" />);
    expect(container.querySelector('.nudge-header--marker')).not.toBeNull();
    expect(screen.getByText('Unread ticket activity')).toBeTruthy();
  });

  it('shows a deliver-now button when paused and triggers without bubbling to the pane', () => {
    const onTrigger = vi.fn();
    const onPaneClick = vi.fn();
    const onPanePointerDown = vi.fn();
    render(
      <div onClick={onPaneClick} onPointerDown={onPanePointerDown}>
        <HeaderNudgeIndicator mode="paused" onTrigger={onTrigger} />
      </div>,
    );
    const button = screen.getByRole('button', { name: /deliver/i });
    fireEvent.pointerDown(button);
    expect(onPanePointerDown).not.toHaveBeenCalled();
    fireEvent.click(button);
    expect(onTrigger).toHaveBeenCalledTimes(1);
    expect(onPaneClick).not.toHaveBeenCalled();
  });
});
