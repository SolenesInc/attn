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
  view.daemon.on('session_reopen', (command) => ({ event: 'session_reopen_result', success: true, result: { session_id: command.session_id, profile_id: DEFAULT_PROFILE_ID, directory: '/tmp', action: command.action ?? 'reopen' } }));
  return view;
}

describe('SessionsTab profiles', () => {
  it('offers no profile move even when other profiles exist', async () => {
    await openLedger(pages([page({ entries: [entry({ id: 's1' })] })]), withProfiles(threeProfiles, DEFAULT_PROFILE_ID, ['s1']));

    expect(row('run s1').getAttribute('data-verbs')).toBe('Focus');
    expect(within(inspector()).queryByRole('button', { name: 'Move to…' })).toBeNull();
  });

  it('offers no reopening for a deleted profile', async () => {
    const view = await openLedger(pages([page({ entries: [closedEntry('s1', { profile_deleted: true, profile_name: 'Deleted' })] })]), withProfiles(threeProfiles));
    expect(row('run s1').getAttribute('data-verbs')).not.toContain('Reopen');
    expect(within(inspector()).queryByRole('button', { name: /Reopen/ })).toBeNull();
    expect(within(inspector()).getByText(/This session cannot be reopened/)).toBeInTheDocument();
    fireEvent.keyDown(row('run s1'), { key: 'Enter' });
    await view.daemon.idle();
    expect(view.daemon.sentOf('session_reopen')).toEqual([]);
  });

  it('reopens a session in a live profile without asking', async () => {
    const view = await openLedger(pages([page({ entries: [closedEntry('s1')] })]), withProfiles(threeProfiles));

    fireEvent.click(within(inspector()).getByRole('button', { name: /Reopen/ }));
    await view.daemon.idle();

    expect(view.daemon.sentOf('session_reopen').map(({ session_id, action }) => [session_id, action]))
      .toEqual([['s1', SessionReopenAction.Reopen]]);
  });
});
