import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openActionMenu } from './test/appFixtures';
import { defaultProfile, emptyDesktop, DEFAULT_PROFILE_ID } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

const desktops = [
  emptyDesktop('unnamed2', { order_key: 'a' }),
  emptyDesktop('nine', { order_key: 'b', shortcut_slot: 9, name: 'aardvark' }),
  emptyDesktop('named10', { order_key: 'c', name: 'Review 10' }),
  emptyDesktop('one', { order_key: 'd', shortcut_slot: 1, name: 'Zebra' }),
  emptyDesktop('named2', { order_key: 'e', name: 'review 2' }),
  emptyDesktop('unnamed1', { order_key: 'f', name: '  ' }),
  emptyDesktop('alpha', { order_key: 'g', name: 'Alpha' }),
];
const sorted = ['one', 'nine', 'alpha', 'named2', 'named10', 'unnamed2', 'unnamed1'];

async function sort(queue: boolean, success = true) {
  const { daemon } = await renderApp({
    initialState: {
      profiles: [defaultProfile('one')], desktops,
      settings: { queue_mode_enabled: String(queue) },
    },
    script: (daemon) => {
      daemon.on('desktop_set_order', () => ({
        event: 'profile_action_result', action: 'desktop_set_order', success,
        ...(success ? {} : { error: 'Desktop list changed' }),
      }));
    },
  });
  const search = await openActionMenu(daemon);
  fireEvent.change(search, { target: { value: '>Sort desktops' } });
  await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));
  return daemon;
}

describe('App desktop ordering', () => {
  it.each([false, true])('sorts once and Undo sends the complete previous order (queue=%s)', async (queue) => {
    const daemon = await sort(queue);
    expect(daemon.sentOf('desktop_set_order')).toEqual([{
      cmd: 'desktop_set_order', request_id: expect.any(String), profile_id: DEFAULT_PROFILE_ID, desktop_ids: sorted,
    }]);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Desktops sorted Undo' })));
    expect(daemon.sentOf('desktop_set_order')).toEqual([
      { cmd: 'desktop_set_order', request_id: expect.any(String), profile_id: DEFAULT_PROFILE_ID, desktop_ids: sorted },
      { cmd: 'desktop_set_order', request_id: expect.any(String), profile_id: DEFAULT_PROFILE_ID, desktop_ids: desktops.map((desktop) => desktop.id) },
    ]);
    expect(screen.getByRole('button', { name: 'Desktops sorted ✓ Done' })).toBeDisabled();
  });

  it('shows a refused sort without offering Undo', async () => {
    await sort(false, false);
    expect(screen.getByText('Desktop list changed')).toBeVisible();
    expect(screen.queryByText('Desktops sorted')).toBeNull();
  });
});
