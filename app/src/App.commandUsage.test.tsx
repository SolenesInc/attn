import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openActionMenu, openSession } from './test/appFixtures';
import { DEFAULT_PROFILE_ID, daemonSession, defaultProfile, emptyDesktop, soloDesktop } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const entry = (command_id: string, score: number, last_used_at = '2026-10-04T12:00:00Z') => ({ command_id, score, last_used_at });
function titles() {
  return within(screen.getByRole('dialog', { name: 'Commands' })).queryAllByRole('option')
    .map((row) => row.querySelector('.unified-palette-name')?.firstChild?.textContent);
}
async function query(daemon: ScriptedDaemon, value: string) {
  await gesture(daemon, () => fireEvent.change(screen.getByRole('combobox'), { target: { value } }));
}
async function close(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Escape' }));
}
function answer(daemon: ScriptedDaemon, index: number, entries = [entry('settings', 5)]) {
  const request = daemon.sentOf('get_command_usage')[index];
  daemon.emit({ event: 'get_command_usage_result', request_id: request.request_id, profile_id: request.profile_id, success: true, entries });
}

describe('command palette history', () => {
  it('retains the loading query and waits for history before offering commands', async () => {
    const { daemon } = await renderApp({ script: (d) => d.on('get_command_usage', () => undefined) });
    await openActionMenu(daemon);
    expect(screen.getByText('Loading command history…')).toBeInTheDocument();
    await query(daemon, '>settings');
    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' }));
    expect(daemon.sentOf('record_command_usage')).toEqual([]);
    answer(daemon, 0);
    await daemon.idle();
    expect(screen.getByRole('combobox')).toHaveValue('>settings');
    expect(titles()).toEqual(['Settings']);
    expect(daemon.sentOf('get_command_usage')).toHaveLength(1);
    expect(daemon.sentOf('record_command_usage')).toEqual([]);
  });

  it('keeps history fixed until reopening, which fetches a fresh snapshot', async () => {
    const { daemon } = await renderApp({ script: (d) => d.on('get_command_usage', () => undefined) });
    await openActionMenu(daemon);
    answer(daemon, 0);
    await daemon.idle();
    expect(titles()[0]).toBe('Settings');
    expect(daemon.sentOf('get_command_usage')).toHaveLength(1);
    await close(daemon);
    await openActionMenu(daemon);
    expect(titles()).toEqual([]);
    answer(daemon, 1, [entry('keyboard-shortcuts', 8)]);
    await daemon.idle();
    expect(daemon.sentOf('get_command_usage')).toHaveLength(2);
    expect(titles()[0]).toBe('Keyboard shortcuts');
  });

  it.each(['Enter', 'pointer'])('records exactly one %s selection in both command entry modes', async (pick) => {
    const { daemon } = await renderApp();
    if (pick === 'Enter') await openActionMenu(daemon);
    else {
      await gesture(daemon, () => pressShortcut('ui.actionMenu'));
      await query(daemon, '>');
    }
    await query(daemon, '>countdown');
    await gesture(daemon, () => pick === 'Enter'
      ? fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' })
      : fireEvent.mouseDown(screen.getByRole('option')));
    expect(daemon.sentOf('record_command_usage')).toEqual([{
      cmd: 'record_command_usage', request_id: expect.any(String), profile_id: DEFAULT_PROFILE_ID, command_id: 'toggle-auto-settle',
    }]);
    await openActionMenu(daemon);
    await query(daemon, '>countdown');
    expect(titles()).toEqual(['Turn off auto-settle']);
    expect(daemon.sentOf('get_command_usage')).toHaveLength(2);
    expect(daemon.sentOf('record_command_usage')).toHaveLength(1);
  });

  it('keeps stronger matches ahead of pins and usage, and pins available agent actions on an empty query', async () => {
    const { daemon } = await renderApp({
      initialState: { sessions: [daemonSession('s1', { state: 'idle' })], desktops: [soloDesktop('s1')] },
      script: (d) => d.on('get_command_usage', ({ profile_id }) => ({
        event: 'get_command_usage_result', profile_id, success: true,
        entries: [entry('settings', 100), entry('set-session-context-cap', 2), entry('new-agent', 20)],
      })),
    });
    await openSession(daemon, 's1');
    await openActionMenu(daemon);
    const all = titles();
    expect(all).toContain("Cap s1's context window");
    expect(all).toContain('Reload this agent');
    expect(all.indexOf('Reload this agent')).toBeLessThan(all.indexOf('Settings'));
    expect(all.indexOf("Cap s1's context window")).toBeLessThan(all.indexOf('Settings'));
    expect(all.indexOf('Settings')).toBeLessThan(all.indexOf('New agent'));
    await query(daemon, '>agent');
    expect(titles().indexOf('New agent')).toBeLessThan(titles().indexOf("Cap s1's context window"));
    expect(daemon.sentOf('get_command_usage')).toHaveLength(1);
  });

  it('ignores late responses after closing and keeps read and write failures usable and visible', async () => {
    const { daemon } = await renderApp({ script: (d) => d.on('get_command_usage', () => undefined) });
    await openActionMenu(daemon);
    await close(daemon);
    await openActionMenu(daemon);
    answer(daemon, 0);
    await daemon.idle();
    expect(titles()).toEqual([]);
    const request = daemon.sentOf('get_command_usage')[1];
    daemon.emit({ event: 'get_command_usage_result', request_id: request.request_id, profile_id: request.profile_id, success: false, error: 'History unavailable', entries: [] });
    await daemon.idle();
    expect(titles()[0]).toBe('New agent');
    expect(screen.getByText('History unavailable')).toBeInTheDocument();
    daemon.on('record_command_usage', () => ({ event: 'record_command_usage_result', success: false, error: 'History write refused' }));
    await query(daemon, '>settings');
    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' }));
    expect(screen.getByTestId('settings-modal')).toBeInTheDocument();
    expect(screen.getByText('History write refused')).toBeInTheDocument();
    expect(daemon.sentOf('record_command_usage')).toHaveLength(1);
    expect(daemon.sentOf('get_command_usage')).toHaveLength(2);
  });

  it('discards the old profile response while retaining the query for the new profile', async () => {
    const side = defaultProfile('side-desktop', { id: 'profile-side', name: 'Side' });
    const { daemon } = await renderApp({
      initialState: {
        profiles: [defaultProfile('desktop-1'), side],
        desktops: [emptyDesktop('desktop-1'), emptyDesktop('side-desktop', { profile_id: side.id })],
      },
      script: (d) => d.on('get_command_usage', () => undefined),
    });
    await openActionMenu(daemon);
    await query(daemon, '>settings');
    daemon.emit({ event: 'profile_arrangement_changed', profile: side, desktops: daemon.arrangement.desktops });
    await daemon.idle();
    expect(daemon.sentOf('get_command_usage').map((request) => request.profile_id)).toEqual([DEFAULT_PROFILE_ID, side.id]);
    answer(daemon, 0);
    await daemon.idle();
    expect(titles()).toEqual([]);
    answer(daemon, 1);
    await daemon.idle();
    expect(screen.getByRole('combobox')).toHaveValue('>settings');
    expect(titles()).toEqual(['Settings']);
    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' }));
    expect(daemon.sentOf('record_command_usage').map((request) => request.profile_id)).toEqual([side.id]);
    expect(daemon.sentOf('get_command_usage')).toHaveLength(2);
  });

  it('records a profile-switch command for the source profile before selecting the destination', async () => {
    const side = defaultProfile('side-desktop', { id: 'profile-side', name: 'Side' });
    const { daemon } = await renderApp({
      initialState: {
        profiles: [defaultProfile('desktop-1'), side],
        desktops: [emptyDesktop('desktop-1'), emptyDesktop('side-desktop', { profile_id: side.id })],
      },
    });
    await openActionMenu(daemon);
    await query(daemon, '>Switch to Side');
    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' }));
    expect(daemon.sent.filter((request) => request.cmd === 'record_command_usage' || request.cmd === 'profile_select')).toEqual([
      { cmd: 'record_command_usage', request_id: expect.any(String), profile_id: DEFAULT_PROFILE_ID, command_id: 'profile-profile-side' },
      { cmd: 'profile_select', request_id: expect.any(String), profile_id: side.id },
    ]);
    expect(daemon.sentOf('get_command_usage')).toHaveLength(1);
  });

  it('keeps labels and availability live while preserving selection by command ID', async () => {
    const session = daemonSession('s1', { state: 'idle' });
    const { daemon } = await renderApp({
      initialState: { sessions: [session], desktops: [soloDesktop('s1')] },
      script: (d) => d.on('get_command_usage', ({ profile_id }) => ({
        event: 'get_command_usage_result', profile_id, success: true, entries: [entry('set-session-context-cap', 10)],
      })),
    });
    await openSession(daemon, 's1');
    await openActionMenu(daemon);
    const selectedTitle = () => screen.getByRole('option', { selected: true });
    expect(selectedTitle()).toHaveTextContent("Cap s1's context window");
    daemon.emit({ event: 'session_state_changed', session: { ...session, label: 'renamed' } });
    await daemon.idle();
    expect(selectedTitle()).toHaveTextContent("Cap renamed's context window");
    daemon.emit({ event: 'session_state_changed', session: { ...session, label: 'renamed', agent: 'pi' } });
    await daemon.idle();
    expect(titles()).not.toContain("Cap renamed's context window");
    expect(daemon.sentOf('get_command_usage')).toHaveLength(1);
    expect(daemon.sentOf('record_command_usage')).toEqual([]);
  });

  it('uses recency for equal usage and leaves untracked commands in declaration order', async () => {
    const { daemon } = await renderApp({
      script: (d) => d.on('get_command_usage', ({ profile_id }) => ({
        event: 'get_command_usage_result', profile_id, success: true,
        entries: [entry('settings', 2, '2026-10-01T12:00:00Z'), entry('keyboard-shortcuts', 2)],
      })),
    });
    await openActionMenu(daemon);
    expect(titles().slice(0, 4)).toEqual(['Keyboard shortcuts', 'Settings', 'New agent', 'Jump to the oldest turn']);
    await query(daemon, '>keyboard shortcuts');
    expect(titles()).toEqual(['Keyboard shortcuts', 'Customize keyboard shortcuts']);
    expect(daemon.sentOf('get_command_usage')).toHaveLength(1);
  });

  it('does not record direct shortcuts or agent navigation', async () => {
    const { daemon } = await renderApp({
      initialState: { sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] },
    });
    await gesture(daemon, () => pressShortcut('ui.showShortcuts'));
    expect(daemon.sentOf('record_command_usage')).toEqual([]);
    fireEvent.click(screen.getByRole('button', { name: 'Close keyboard shortcuts' }));
    await gesture(daemon, () => pressShortcut('ui.actionMenu'));
    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' }));
    expect(daemon.sentOf('record_command_usage')).toEqual([]);
    expect(daemon.sentOf('get_command_usage')).toHaveLength(1);
  });
});
