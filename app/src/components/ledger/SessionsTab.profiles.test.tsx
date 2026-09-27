import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { SessionReopenAction } from '../../types/generated';
import { daemonSession, defaultProfile, DEFAULT_DESKTOP_ID, DEFAULT_PROFILE_ID } from '../../test/daemonFixtures';
import { closedEntry, entry } from '../../test/sessionLedgerFixtures';
import type { ScriptedDaemonOptions } from '../../test/scriptedDaemon';
import { openSessionsLedger, page, pages, rows, type LedgerAnswer } from './testSupport';

const row = (label: string) => rows().getByText(label).closest('.ledger-row') as HTMLElement;
const inspector = () => screen.getByRole('complementary', { name: 'Details' });

const WORK = 'profile-work';
const SIDE = 'profile-side';

function withProfiles(names: Record<string, string>, selected = DEFAULT_PROFILE_ID, sessions: string[] = []): ScriptedDaemonOptions {
  return {
    initialState: {
      profiles: Object.entries(names).map(([id, name]) => defaultProfile(DEFAULT_DESKTOP_ID, { id, name })),
      selected_profile_id: selected,
      sessions: sessions.map((id) => daemonSession(id, { state: 'idle' })),
    },
  };
}

const threeProfiles = { [DEFAULT_PROFILE_ID]: 'Default', [WORK]: 'Work', [SIDE]: 'Side' };

async function openLedger(answer: LedgerAnswer, options: ScriptedDaemonOptions) {
  const view = await openSessionsLedger(answer, options);
  view.daemon.on('session_move', (command) => ({ event: 'profile_action_result', action: 'session_move', request_id: command.request_id ?? '', success: true }));
  view.daemon.on('session_reopen', (command) => ({ event: 'session_reopen_result', success: true, result: { session_id: command.session_id, profile_id: command.profile_id ?? DEFAULT_PROFILE_ID, directory: '/tmp', action: command.action ?? 'reopen' } }));
  return view;
}

describe('SessionsTab profiles', () => {
  it('moves a live agent to the profile picked from its menu', async () => {
    const view = await openLedger(pages([page({ entries: [entry({ id: 's1' })] })]), withProfiles(threeProfiles, DEFAULT_PROFILE_ID, ['s1']));

    fireEvent.keyDown(row('run s1'), { key: '.' });
    fireEvent.keyDown(row('run s1'), { key: '2' });
    const picker = within(row('run s1')).getByRole('menu', { name: 'Move to' });
    expect(within(picker).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['1Side', '2Work']);
    fireEvent.keyDown(row('run s1'), { key: '2' });
    await view.daemon.idle();

    expect(view.daemon.sentOf('session_move').map(({ session_id, expected_profile_id, destination_profile_id }) =>
      [session_id, expected_profile_id, destination_profile_id])).toEqual([['s1', DEFAULT_PROFILE_ID, WORK]]);
    expect(within(row('run s1')).queryByRole('menu')).toBeNull();
  });

  it('leaves Enter on a Tab-focused choice to that choice', async () => {
    const view = await openLedger(pages([page({ entries: [entry({ id: 's1' })] })]), withProfiles(threeProfiles, DEFAULT_PROFILE_ID, ['s1']));

    fireEvent.keyDown(row('run s1'), { key: '.' });
    fireEvent.keyDown(row('run s1'), { key: '2' });
    const work = within(row('run s1')).getByRole('menuitem', { name: /Work/ });

    expect(fireEvent.keyDown(work, { key: 'Enter' })).toBe(true);
    expect(screen.getByRole('dialog', { name: 'Sessions and worktrees' })).toBeInTheDocument();
    fireEvent.click(work);
    await view.daemon.idle();
    expect(view.daemon.sentOf('session_move').map(({ destination_profile_id }) => destination_profile_id)).toEqual([WORK]);
  });

  it('keeps a refused move on the row', async () => {
    const view = await openLedger(pages([page({ entries: [entry({ id: 's1' })] })]), withProfiles(threeProfiles, DEFAULT_PROFILE_ID, ['s1']));
    view.daemon.on('session_move', (command) => ({
      event: 'profile_action_result', action: 'session_move', request_id: command.request_id ?? '', success: false,
      error: 'session s1 is in profile-work, not profile-default',
    }));

    fireEvent.click(within(inspector()).getByRole('button', { name: 'Move to…' }));
    fireEvent.click(within(row('run s1')).getByRole('menuitem', { name: /Work/ }));
    await view.daemon.idle();

    expect(within(row('run s1')).getByText(/not profile-default/)).toBeTruthy();
  });

  it('offers no move when the agent has nowhere else to go', async () => {
    await openLedger(pages([page({ entries: [entry({ id: 's1' })] })]), withProfiles({ [DEFAULT_PROFILE_ID]: 'Default' }, DEFAULT_PROFILE_ID, ['s1']));

    expect(row('run s1').getAttribute('data-verbs')).toBe('Focus');
    expect(within(inspector()).queryByRole('button', { name: 'Move to…' })).toBeNull();
  });

  it('asks where to reopen a session whose profile was deleted, current profile first', async () => {
    const view = await openLedger(
      pages([page({ entries: [closedEntry('s1', { profile_id: 'gone', profile_name: 'Old', profile_deleted: true })] })]),
      withProfiles(threeProfiles, WORK),
    );

    fireEvent.keyDown(row('run s1'), { key: 'Enter' });
    const picker = within(row('run s1')).getByRole('menu', { name: 'Reopen into' });
    expect(within(picker).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(['1Work', '2Default', '3Side']);
    expect(view.daemon.sentOf('session_reopen')).toEqual([]);

    fireEvent.keyDown(row('run s1'), { key: '1' });
    await view.daemon.idle();
    expect(view.daemon.sentOf('session_reopen').map(({ session_id, action, profile_id }) => [session_id, action, profile_id]))
      .toEqual([['s1', SessionReopenAction.Reopen, WORK]]);
  });

  it('reopens a session in a live profile without asking', async () => {
    const view = await openLedger(pages([page({ entries: [closedEntry('s1')] })]), withProfiles(threeProfiles));

    fireEvent.click(within(inspector()).getByRole('button', { name: /Reopen/ }));
    await view.daemon.idle();

    expect(view.daemon.sentOf('session_reopen').map(({ session_id, action, profile_id }) => [session_id, action, profile_id]))
      .toEqual([['s1', SessionReopenAction.Reopen, undefined]]);
  });
});
