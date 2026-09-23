import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import type { Setup } from '../types/generated';
import { SetupSwitcher } from './SetupSwitcher';

function setup(id: string, lastUsedAt: string): Setup {
  return { id, name: id, current_desktop_id: `${id}-d1`, last_used_at: lastUsedAt, revision: 1 };
}

const WORK = setup('work', '2026-09-23T12:00:00Z');
const HOME = setup('home', '2026-09-23T11:00:00Z');
const SIDE = setup('side', '2026-09-23T10:00:00Z');

describe('SetupSwitcher', () => {
  afterEach(() => _resetEscapeStackForTest());

  it('selects the highlighted setup even after another client reorders the list', () => {
    const onSelect = vi.fn();
    const { rerender } = render(<SetupSwitcher setups={[WORK, HOME, SIDE]} selectedSetupId="work" onSelect={onSelect} onClose={vi.fn()} />);
    const menu = screen.getByRole('menu', { name: 'Switch profile' });

    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    rerender(
      <SetupSwitcher
        setups={[WORK, { ...HOME, last_used_at: '2026-09-23T09:00:00Z' }, SIDE]}
        selectedSetupId="work"
        onSelect={onSelect}
        onClose={vi.fn()}
      />,
    );
    fireEvent.keyDown(menu, { key: 'Enter' });

    expect(onSelect).toHaveBeenCalledWith('home');
  });

  it('falls back to the first setup when the highlighted one is deleted', () => {
    const onSelect = vi.fn();
    const { rerender } = render(<SetupSwitcher setups={[WORK, HOME, SIDE]} selectedSetupId="work" onSelect={onSelect} onClose={vi.fn()} />);
    const menu = screen.getByRole('menu', { name: 'Switch profile' });

    fireEvent.keyDown(menu, { key: 'ArrowUp' });
    rerender(<SetupSwitcher setups={[WORK, HOME]} selectedSetupId="work" onSelect={onSelect} onClose={vi.fn()} />);
    fireEvent.keyDown(menu, { key: 'Enter' });

    expect(onSelect).toHaveBeenCalledWith('work');
  });
});
