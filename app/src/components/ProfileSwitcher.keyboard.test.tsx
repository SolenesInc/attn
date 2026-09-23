import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import type { Profile } from '../types/generated';
import { ProfileSwitcher } from './ProfileSwitcher';

function profile(id: string, lastUsedAt: string): Profile {
  return { id, name: id, current_desktop_id: `${id}-d1`, last_used_at: lastUsedAt, revision: 1 };
}

const WORK = profile('work', '2026-09-23T12:00:00Z');
const HOME = profile('home', '2026-09-23T11:00:00Z');
const SIDE = profile('side', '2026-09-23T10:00:00Z');

describe('ProfileSwitcher', () => {
  afterEach(() => _resetEscapeStackForTest());

  it('selects the highlighted profile even after another client reorders the list', () => {
    const onSelect = vi.fn();
    const { rerender } = render(<ProfileSwitcher profiles={[WORK, HOME, SIDE]} selectedProfileId="work" onSelect={onSelect} onClose={vi.fn()} />);
    const menu = screen.getByRole('menu', { name: 'Switch profile' });

    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    rerender(
      <ProfileSwitcher
        profiles={[WORK, { ...HOME, last_used_at: '2026-09-23T09:00:00Z' }, SIDE]}
        selectedProfileId="work"
        onSelect={onSelect}
        onClose={vi.fn()}
      />,
    );
    fireEvent.keyDown(menu, { key: 'Enter' });

    expect(onSelect).toHaveBeenCalledWith('home');
  });

  it('falls back to the first profile when the highlighted one is deleted', () => {
    const onSelect = vi.fn();
    const { rerender } = render(<ProfileSwitcher profiles={[WORK, HOME, SIDE]} selectedProfileId="work" onSelect={onSelect} onClose={vi.fn()} />);
    const menu = screen.getByRole('menu', { name: 'Switch profile' });

    fireEvent.keyDown(menu, { key: 'ArrowUp' });
    rerender(<ProfileSwitcher profiles={[WORK, HOME]} selectedProfileId="work" onSelect={onSelect} onClose={vi.fn()} />);
    fireEvent.keyDown(menu, { key: 'Enter' });

    expect(onSelect).toHaveBeenCalledWith('work');
  });
});
