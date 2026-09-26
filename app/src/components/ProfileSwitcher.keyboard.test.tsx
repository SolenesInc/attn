import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ComponentProps } from 'react';
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

type Props = ComponentProps<typeof ProfileSwitcher>;

function props(overrides: Partial<Props> = {}): Props {
  return {
    profiles: [WORK, HOME, SIDE],
    selectedProfileId: 'work',
    onSelect: vi.fn(),
    onCreate: vi.fn(async () => undefined),
    onRename: vi.fn(async () => undefined),
    onDelete: vi.fn(async () => undefined),
    onClose: vi.fn(),
    ...overrides,
  };
}

const menu = () => screen.getByRole('menu', { name: 'Switch profile' });

describe('ProfileSwitcher', () => {
  afterEach(() => _resetEscapeStackForTest());

  it('selects the highlighted profile even after another client reorders the list', () => {
    const onSelect = vi.fn();
    const { rerender } = render(<ProfileSwitcher {...props({ onSelect })} />);

    fireEvent.keyDown(menu(), { key: 'ArrowDown' });
    rerender(<ProfileSwitcher {...props({ onSelect, profiles: [WORK, { ...HOME, last_used_at: '2026-09-23T09:00:00Z' }, SIDE] })} />);
    fireEvent.keyDown(menu(), { key: 'Enter' });

    expect(onSelect).toHaveBeenCalledWith('home');
  });

  it('leaves Enter on a Tab-focused profile to that profile\'s own button', () => {
    const onSelect = vi.fn();
    render(<ProfileSwitcher {...props({ onSelect })} />);
    const home = screen.getByRole('menuitem', { name: 'home' });

    const notCancelled = fireEvent.keyDown(home, { key: 'Enter' });
    expect(notCancelled).toBe(true);
    expect(onSelect).not.toHaveBeenCalled();

    fireEvent.click(home);
    expect(onSelect).toHaveBeenCalledWith('home');
  });

  it('falls back to the first profile when the highlighted one is deleted', () => {
    const onSelect = vi.fn();
    const { rerender } = render(<ProfileSwitcher {...props({ onSelect })} />);

    fireEvent.keyDown(menu(), { key: 'ArrowUp' });
    rerender(<ProfileSwitcher {...props({ onSelect, profiles: [WORK, HOME] })} />);
    fireEvent.keyDown(menu(), { key: 'Enter' });

    expect(onSelect).toHaveBeenCalledWith('work');
  });

  it('creates a profile from N, then closes', async () => {
    const onCreate = vi.fn(async () => undefined);
    const onClose = vi.fn();
    render(<ProfileSwitcher {...props({ onCreate, onClose })} />);

    fireEvent.keyDown(menu(), { key: 'n' });
    fireEvent.change(screen.getByLabelText('New profile name'), { target: { value: 'Personal' } });
    fireEvent.submit(screen.getByLabelText('New profile name'));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(onCreate).toHaveBeenCalledWith('Personal');
  });

  it('shows a refused rename in place and keeps the draft', async () => {
    const onRename = vi.fn(async () => { throw new Error('a live profile is already named home'); });
    render(<ProfileSwitcher {...props({ onRename })} />);

    fireEvent.keyDown(menu(), { key: 'r' });
    const input = screen.getByLabelText('Rename work');
    fireEvent.change(input, { target: { value: 'home' } });
    fireEvent.submit(input);

    expect(await screen.findByRole('alert')).toHaveTextContent('already named home');
    expect(onRename).toHaveBeenCalledWith('work', 'home');
    expect((screen.getByLabelText('Rename work') as HTMLInputElement).value).toBe('home');
  });

  it('deletes the highlighted profile into the destination picked with the arrows', async () => {
    const onDelete = vi.fn(async () => undefined);
    render(<ProfileSwitcher {...props({ onDelete })} />);

    fireEvent.keyDown(menu(), { key: 'Backspace' });
    expect(screen.getByText(/Delete work\. Its agents, crew and automations move to:/)).toBeTruthy();
    expect(screen.getByRole('menuitemradio', { name: 'home' }).getAttribute('aria-checked')).toBe('true');
    fireEvent.keyDown(menu(), { key: 'ArrowDown' });
    fireEvent.keyDown(menu(), { key: 'Enter' });

    await waitFor(() => expect(onDelete).toHaveBeenCalledWith('work', 'side'));
    await screen.findByRole('menuitem', { name: /work/ });
  });

  it('says why the last profile cannot be deleted', () => {
    const onDelete = vi.fn();
    render(<ProfileSwitcher {...props({ profiles: [WORK], onDelete })} />);

    fireEvent.keyDown(menu(), { key: 'Delete' });

    expect(screen.getByRole('alert')).toHaveTextContent('work is the last profile, and attn always keeps one.');
    expect(onDelete).not.toHaveBeenCalled();
  });
});
