import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openActionMenu, openSession } from './test/appFixtures';
import { agentPane, daemonDesktop, daemonSession, emptyDesktop, soloDesktop } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

describe('close desktop', () => {
  it('closes an empty desktop with Cmd+W while its launcher owns focus', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [daemonSession('s1')],
      desktops: [soloDesktop('s1', { shortcut_slot: 1 }), emptyDesktop('empty', { shortcut_slot: 2 })],
    } });
    daemon.on('desktop_close', (command) => {
      daemon.arrangement.profile.current_desktop_id = 'desktop-s1';
      daemon.arrange((desktops) => desktops.filter((desktop) => desktop.id !== command.desktop_id));
      return { event: 'profile_action_result', action: 'desktop_close', success: true };
    });
    await openSession(daemon, 's1');
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    expect(screen.getByTestId('location-picker-path-input')).toHaveFocus();

    await gesture(daemon, () => pressShortcut('session.close'));

    expect(daemon.sentOf('desktop_close')).toEqual([expect.objectContaining({ desktop_id: 'empty', expected_revision: 1 })]);
    expect(screen.queryByRole('dialog', { name: /Close/ })).toBeNull();
    expect(daemon.sentOf('unregister')).toEqual([]);
    expect(daemon.arrangement.desktops.map((desktop) => desktop.id)).not.toContain('empty');
  });

  it.each(['sidebar', 'desktop palette', 'queue palette'] as const)('asks once with agent, shell and tile counts from %s', async (entry) => {
    const root = { type: 'split', split_id: 'a', direction: 'vertical', ratio: 0.5, children: [
      { type: 'pane', pane_id: 'pane-agent' },
      { type: 'split', split_id: 'b', direction: 'vertical', ratio: 0.5, children: [
        { type: 'pane', pane_id: 'pane-shell' }, { type: 'tile', tile_id: 'reference', tile_kind: 'browser', tile_params: 'https://example.com' },
      ] },
    ] };
    const { daemon } = await renderApp({ initialState: {
      settings: { queue_mode_enabled: entry === 'queue palette' },
      sessions: [daemonSession('agent'), daemonSession('shell', { agent: 'shell' })],
      desktops: [daemonDesktop('work', { root, panes: [agentPane('agent', 'work'), agentPane('shell', 'work')] }, { name: 'Work' }), emptyDesktop('other')],
    } });
    daemon.on('desktop_close', () => ({ event: 'profile_action_result', action: 'desktop_close', success: true }));
    await openSession(daemon, 'agent');
    const request = async () => {
      if (entry === 'sidebar') await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Close Work' })));
      else {
        const input = await openActionMenu(daemon);
        await gesture(daemon, () => fireEvent.change(input, { target: { value: '>Close desktop' } }));
        await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));
      }
    };
    await request();
    expect(screen.getByRole('dialog', { name: 'Close Work?' })).toHaveTextContent('1 agent, 1 shell and 1 tile');
    expect(daemon.sentOf('desktop_close')).toEqual([]);
    await gesture(daemon, () => pressShortcut('session.close'));
    expect(daemon.sentOf('unregister')).toEqual([]);
    expect(daemon.sentOf('desktop_close')).toEqual([]);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Cancel' })));
    expect(daemon.sentOf('desktop_close')).toEqual([]);
    await request();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Close desktop' })));
    expect(daemon.sentOf('desktop_close')).toEqual([expect.objectContaining({ desktop_id: 'work', expected_revision: 1 })]);
  });

  it.each([{ chief: true }, { crew_member: 'alder' }])('names protected sessions and sends nothing', async (protection) => {
    const { daemon } = await renderApp({ initialState: {
      settings: { queue_mode_enabled: false },
      sessions: [daemonSession('s1', { label: 'Coordinator', ...protection })],
      desktops: [soloDesktop('s1', { name: 'Work' }), emptyDesktop('other')],
    } });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Close Work' })));
    expect(screen.getByRole('alert')).toHaveTextContent('move these protected sessions first: Coordinator');
    expect(screen.queryByRole('dialog', { name: /Close Work/ })).toBeNull();
    expect(daemon.sentOf('desktop_close')).toEqual([]);
  });
});
