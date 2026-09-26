import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { SessionReopenAction } from '../../types/generated';
import { closedEntry, judged, listing, liveEntry, page, renderSessionsTab, rows } from './testSupport';

const row = (label: string) => rows().getByText(label).closest('.ledger-row') as HTMLElement;
const inspector = () => screen.getByRole('complementary', { name: 'Details' });
const profileNames = { 'profile-1': 'Default', 'profile-2': 'Work', 'profile-3': 'Side' };

describe('SessionsTab profiles', () => {
  it('moves a live agent to the profile picked from its menu', async () => {
    const onMoveSession = vi.fn(async () => undefined);
    const { list } = listing([page({ entries: [liveEntry('s1')] })]);
    renderSessionsTab({ listSessions: list, profileNames, currentProfileId: 'profile-1', onMoveSession });

    await rows().findByText('run s1');
    fireEvent.keyDown(row('run s1'), { key: '.' });
    fireEvent.keyDown(row('run s1'), { key: '2' });
    const picker = within(row('run s1')).getByRole('menu', { name: 'Move to' });
    expect(within(picker).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['1Side', '2Work']);
    fireEvent.keyDown(row('run s1'), { key: '2' });

    expect(onMoveSession).toHaveBeenCalledWith('s1', 'profile-1', 'profile-2');
    await waitFor(() => expect(within(row('run s1')).queryByRole('menu')).toBeNull());
  });

  it('keeps a refused move on the row', async () => {
    const onMoveSession = vi.fn(async () => { throw new Error('session s1 is in profile-2, not profile-1'); });
    const { list } = listing([page({ entries: [liveEntry('s1')] })]);
    renderSessionsTab({ listSessions: list, profileNames, onMoveSession });

    await rows().findByText('run s1');
    fireEvent.click(within(inspector()).getByRole('button', { name: 'Move to…' }));
    fireEvent.click(within(row('run s1')).getByRole('menuitem', { name: /Work/ }));

    expect(await within(row('run s1')).findByText(/not profile-1/)).toBeTruthy();
  });

  it('offers no move when the agent has nowhere else to go', async () => {
    const { list } = listing([page({ entries: [liveEntry('s1')] })]);
    renderSessionsTab({ listSessions: list, profileNames: { 'profile-1': 'Default' }, onMoveSession: vi.fn() });

    await rows().findByText('run s1');
    expect(row('run s1').getAttribute('data-verbs')).toBe('Focus');
    expect(within(inspector()).queryByRole('button', { name: 'Move to…' })).toBeNull();
  });

  it('asks where to reopen a session whose profile was deleted, current profile first', async () => {
    const onReopen = vi.fn();
    const { list } = listing([page({
      entries: [closedEntry('s1', { profile_id: 'gone', profile_name: 'Old', profile_deleted: true })],
      reopen: [judged('s1', { profile_id: 'gone', profile_deleted: true })],
    })]);
    renderSessionsTab({ listSessions: list, profileNames, currentProfileId: 'profile-2', onReopen });

    await rows().findByText('run s1');
    fireEvent.keyDown(row('run s1'), { key: 'Enter' });
    const picker = within(row('run s1')).getByRole('menu', { name: 'Reopen into' });
    expect(within(picker).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['1Work', '2Default', '3Side']);
    expect(onReopen).not.toHaveBeenCalled();

    fireEvent.keyDown(row('run s1'), { key: '1' });
    expect(onReopen).toHaveBeenCalledWith('s1', SessionReopenAction.Reopen, 'profile-2');
  });

  it('reopens a session in a live profile without asking', async () => {
    const onReopen = vi.fn();
    const { list } = listing([page({ entries: [closedEntry('s1')], reopen: [judged('s1')] })]);
    renderSessionsTab({ listSessions: list, profileNames, onReopen });

    await rows().findByText('run s1');
    fireEvent.click(within(inspector()).getByRole('button', { name: /Reopen/ }));

    expect(onReopen).toHaveBeenCalledWith('s1', SessionReopenAction.Reopen, undefined);
  });
});
