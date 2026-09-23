import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import type { Desktop } from '../types/generated';
import { DesktopOverview } from './DesktopOverview';

function desktop(id: string, overrides: Partial<Desktop> = {}): Desktop {
  return {
    id,
    setup_id: 'set-default',
    name: '',
    order_key: id,
    tree_json: '',
    active_pane_id: '',
    panes: [],
    revision: 1,
    ...overrides,
  };
}

const TWO_PANES = JSON.stringify({
  type: 'split',
  split_id: 's1',
  direction: 'vertical',
  ratio: 0.5,
  children: [
    { type: 'pane', pane_id: 'p1' },
    { type: 'tile', tile_id: 't1', tile_kind: 'markdown' },
  ],
});

const DESKTOPS = [
  desktop('d10', { order_key: 'a' }),
  desktop('d2', { shortcut_slot: 2 }),
  desktop('d1', {
    shortcut_slot: 1,
    tree_json: TWO_PANES,
    active_pane_id: 'p1',
    panes: [{ desktop_id: 'd1', pane_id: 'p1', kind: 'agent' as never, session_id: 's1', status: 'ready' as never, title: 'reviewer' }],
  }),
  desktop('d11', { order_key: 'b' }),
];

function renderOverview(overrides: Partial<Parameters<typeof DesktopOverview>[0]> = {}) {
  const props = {
    setupName: 'Default',
    desktops: DESKTOPS,
    currentDesktopId: 'd1',
    canSendActivePane: true,
    onSwitch: vi.fn(),
    onSendActivePane: vi.fn(),
    onDelete: vi.fn(),
    onGiveShortcutSlot: vi.fn(),
    onCreate: vi.fn(),
    onClose: vi.fn(),
    ...overrides,
  };
  render(<DesktopOverview {...props} />);
  return { props, dialog: screen.getByRole('dialog', { name: 'Desktop overview' }) };
}

function cardNames(): string[] {
  return Array.from(document.querySelectorAll('[data-desktop-id]')).map(
    (card) => card.querySelector('.desktop-overview-name')?.textContent ?? '',
  );
}

describe('DesktopOverview', () => {
  afterEach(() => _resetEscapeStackForTest());

  it('lists shortcut desktops by slot, then extras counting on past nine', () => {
    renderOverview();

    expect(cardNames()).toEqual(['Desktop 1', 'Desktop 2', 'Desktop 10', 'Desktop 11']);
    expect(screen.getByText('More desktops · no shortcut')).toBeTruthy();
    expect(screen.getByText('reviewer')).toBeTruthy();
    expect(screen.getByText('markdown')).toBeTruthy();
  });

  it('switches to the desktop the arrows land on', () => {
    const { props, dialog } = renderOverview();

    fireEvent.keyDown(dialog, { key: 'ArrowRight' });
    fireEvent.keyDown(dialog, { key: 'ArrowRight' });
    fireEvent.keyDown(dialog, { key: 'Enter' });

    expect(props.onSwitch).toHaveBeenCalledWith('d10');
    expect(props.onClose).toHaveBeenCalled();
  });

  it('sends the focused pane to the highlighted desktop on shift-enter', () => {
    const { props, dialog } = renderOverview();

    fireEvent.keyDown(dialog, { key: 'ArrowDown' });
    fireEvent.keyDown(dialog, { key: 'Enter', shiftKey: true });

    expect(props.onSendActivePane).toHaveBeenCalledWith('d11');
    expect(props.onSwitch).not.toHaveBeenCalled();
  });

  it('does not send to the current desktop', () => {
    const { props, dialog } = renderOverview();

    fireEvent.keyDown(dialog, { key: 'Enter', shiftKey: true });

    expect(props.onSendActivePane).not.toHaveBeenCalled();
    expect(props.onClose).not.toHaveBeenCalled();
  });

  it('switches by digit', () => {
    const { props, dialog } = renderOverview();

    fireEvent.keyDown(dialog, { key: '2', code: 'Digit2' });

    expect(props.onSwitch).toHaveBeenCalledWith('d2');
  });

  it('deletes the highlighted empty desktop but never the current one', () => {
    const { props, dialog } = renderOverview();

    fireEvent.keyDown(dialog, { key: 'Delete' });
    fireEvent.keyDown(dialog, { key: 'ArrowRight' });
    fireEvent.keyDown(dialog, { key: 'Delete' });

    expect(props.onDelete.mock.calls).toEqual([['d2']]);
  });

  it('offers a shortcut slot only to extra desktops', () => {
    const { props } = renderOverview();

    const giveButtons = screen.getAllByRole('button', { name: 'Give a shortcut' });
    expect(giveButtons).toHaveLength(2);
    fireEvent.click(giveButtons[0]);

    expect(props.onGiveShortcutSlot).toHaveBeenCalledWith('d10');
    expect(props.onSwitch).not.toHaveBeenCalled();
  });

  it('closes on Escape', () => {
    const { props } = renderOverview();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(props.onClose).toHaveBeenCalled();
  });
});
