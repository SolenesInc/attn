import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonSession, defaultProfile, emptyDesktop, soloDesktop, splitDesktop } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

async function setup(sameDesktop = true, queue = false) {
  const running = await renderApp({ initialState: {
    settings: { queue_mode_enabled: String(queue) },
    sessions: [daemonSession('root'), daemonSession('delegate', { dispatcher_session_id: 'root' })],
    profiles: [defaultProfile('source')],
    desktops: [
      sameDesktop ? splitDesktop('source', ['root', 'delegate'], { name: 'Source', shortcut_slot: 1 }) : soloDesktop('root', { id: 'source', name: 'Source', shortcut_slot: 1 }),
      sameDesktop ? emptyDesktop('target', { name: 'Target', shortcut_slot: 2 }) : soloDesktop('delegate', { id: 'target', name: 'Target', shortcut_slot: 2 }),
    ],
  } });
  await gesture(running.daemon, () => pressShortcut('desktop.select1'));
  return running;
}

describe('moving with delegates', () => {
  for (const queue of [false, true]) {
    it(`offers the palette move in ${queue ? 'queue' : 'desktop'} flow and sends one flagged request`, async () => {
      const { daemon } = await setup(true, queue);
      await gesture(daemon, () => pressShortcut('ui.commandPalette'));
      const palette = screen.getByRole('dialog');
      fireEvent.change(within(palette).getByRole('combobox'), { target: { value: '>Move with delegates' } });
      const option = within(palette).getByRole('option');
      expect(option).toHaveTextContent('Move with delegates to Target');
      await gesture(daemon, () => fireEvent.mouseDown(option));
      expect(daemon.sentOf('desktop_move_leaf')).toEqual([expect.objectContaining({
        source_desktop_id: 'source', target_desktop_id: 'target', leaf_id: 'pane-root', with_delegates: true,
      })]);
    });
  }

  it('hides the command when the only delegate is on another desktop', async () => {
    const { daemon } = await setup(false);
    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    const palette = screen.getByRole('dialog');
    fireEvent.change(within(palette).getByRole('combobox'), { target: { value: '>Move with delegates' } });
    expect(within(palette).queryByRole('option')).toBeNull();
    expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);
  });

  for (const altKey of [false, true]) {
    it(`reads Option at sidebar drop (${altKey})`, async () => {
      const { daemon } = await setup();
      const row = document.querySelector('[data-select-key="source/session:root"]')!;
      fireEvent.pointerDown(row, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 30, clientY: 30 });
      await daemon.idle();
      await gesture(daemon, () => fireEvent.pointerUp(screen.getByTestId('sidebar-desktop-target'), { pointerId: 1, altKey }));
      const requests = daemon.sentOf('desktop_move_leaf');
      expect(requests).toHaveLength(1);
      expect(requests[0].with_delegates).toBe(altKey ? true : undefined);
      expect(requests[0]).toMatchObject({ source_desktop_id: 'source', target_desktop_id: 'target', leaf_id: 'pane-root' });
    });
  }
});
