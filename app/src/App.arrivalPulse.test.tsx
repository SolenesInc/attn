import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, daemonDesktop, daemonSeed, daemonSession, defaultProfile, dockTiles, soloDesktop } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { openPicker, submitPath } from './test/locations';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const SEED_ID = 's-7k3f9m';
const SEED_TITLE = 'Arrival notes';
const TILE_ID = 'tile-seed';

async function pick(daemon: ScriptedDaemon, label: string) {
  await gesture(daemon, () => pressShortcut('ui.actionMenu'));
  const palette = screen.getByRole('dialog', { name: 'Agents' });
  const option = within(palette).getAllByRole('option').find((entry) => entry.textContent?.includes(label));
  expect(option, within(palette).getAllByRole('option').map((entry) => entry.textContent).join(' | ')).toBeDefined();
  await gesture(daemon, () => fireEvent.mouseDown(option!));
}

function visiblePane(id: string) {
  return document.querySelector<HTMLElement>(`[data-session-visible="1"] [data-pane-id="${id}"]`);
}

describe('App arrival pulse', () => {
  it('answers repeated tile and cross-desktop agent picks, but not another client’s selection', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [daemonSession('s1'), daemonSession('s2')],
      seeds: [daemonSeed(SEED_ID, { title: SEED_TITLE })],
      profiles: [defaultProfile('d1')],
      desktops: [
        daemonDesktop('d1', {
          root: dockTiles({ type: 'pane', pane_id: 'pane-s1' }, [
            { tile_id: TILE_ID, tile_kind: 'seed', tile_params: SEED_ID },
          ]),
          panes: [agentPane('s1', 'd1')],
        }, { active_pane_id: TILE_ID, shortcut_slot: 1 }),
        soloDesktop('s2', { id: 'd2', shortcut_slot: 2 }),
      ],
    } });

    await pick(daemon, SEED_TITLE);
    expect(visiblePane(TILE_ID)).toHaveClass('leaf-arrival');
    expect(daemon.sentOf('desktop_show_leaf')).toEqual([
      expect.objectContaining({ desktop_id: 'd1', leaf_id: TILE_ID }),
    ]);
    expect(daemon.sentOf('desktop_show_session')).toEqual([]);

    fireEvent.animationEnd(visiblePane(TILE_ID)!, { animationName: 'leaf-arrival-pulse' });
    expect(visiblePane(TILE_ID)).not.toHaveClass('leaf-arrival');
    await pick(daemon, SEED_TITLE);
    expect(visiblePane(TILE_ID)).toHaveClass('leaf-arrival');
    expect(daemon.sentOf('desktop_show_leaf')).toHaveLength(2);

    await pick(daemon, 's1');
    expect(visiblePane(TILE_ID)).not.toHaveClass('leaf-arrival');
    expect(visiblePane('pane-s1')).toHaveClass('leaf-arrival');

    await pick(daemon, 's2');
    expect(document.querySelector('[data-desktop-id="d1"] [data-pane-id="pane-s1"]')).not.toHaveClass('leaf-arrival');
    expect(visiblePane('pane-s2')).toHaveClass('leaf-arrival');
    expect(daemon.sentOf('desktop_show_session')).toEqual([
      expect.objectContaining({ session_id: 's1' }),
      expect.objectContaining({ session_id: 's2' }),
    ]);

    const departingPane = visiblePane('pane-s2')!;
    daemon.arrangement.show('d1', 'pane-s1');
    await gesture(daemon, () => daemon.emit(daemon.arrangement.changed()));
    expect(departingPane).not.toHaveClass('leaf-arrival');
    expect(visiblePane('pane-s1')).not.toHaveClass('leaf-arrival');
    expect(daemon.sentOf('desktop_show_leaf')).toHaveLength(2);
    expect(daemon.sentOf('desktop_show_session')).toHaveLength(2);

    await gesture(daemon, () => pressShortcut('desktop.select2'));
    expect(visiblePane('pane-s2')).not.toHaveClass('leaf-arrival');
  });

  it('announces history and ⌘J arrivals', async () => {
    const { daemon } = await renderApp({ initialState: {
      sessions: [
        daemonSession('s1', { turn_owed: true, turn_opened_at: '2026-08-03T09:00:00Z' }),
        daemonSession('s2'),
      ],
      profiles: [defaultProfile('d1')],
      desktops: [soloDesktop('s1', { id: 'd1' }), soloDesktop('s2', { id: 'd2' })],
      settings: { queue_mode_enabled: 'true' },
    } });

    await pick(daemon, 's1');
    fireEvent.animationEnd(visiblePane('pane-s1')!, { animationName: 'leaf-arrival-pulse' });
    await pick(daemon, 's2');
    fireEvent.animationEnd(visiblePane('pane-s2')!, { animationName: 'leaf-arrival-pulse' });

    await gesture(daemon, () => pressShortcut('session.historyBack'));
    expect(visiblePane('pane-s1')).toHaveClass('leaf-arrival');
    fireEvent.animationEnd(visiblePane('pane-s1')!, { animationName: 'leaf-arrival-pulse' });
    await gesture(daemon, () => pressShortcut('session.historyForward'));
    expect(visiblePane('pane-s2')).toHaveClass('leaf-arrival');
    fireEvent.animationEnd(visiblePane('pane-s2')!, { animationName: 'leaf-arrival-pulse' });
    await gesture(daemon, () => pressShortcut('session.jumpToWaiting'));
    expect(visiblePane('pane-s1')).toHaveClass('leaf-arrival');
    expect(daemon.sentOf('desktop_show_session')).toHaveLength(3);
    expect(daemon.sentOf('desktop_show_leaf')).toHaveLength(2);
  });

  it('does not announce a session created with ⌘N', async () => {
    const { daemon } = await openPicker({}, {
      sessions: [daemonSession('s1')],
      desktops: [soloDesktop('s1')],
    });
    await submitPath(daemon, '/home/me');
    const spawned = daemon.sentOf('spawn_session')[0];
    expect(spawned).toBeDefined();
    expect(visiblePane(`pane-${spawned.id}`)).not.toHaveClass('leaf-arrival');
    expect(daemon.sentOf('desktop_show_session')).toEqual([]);
  });
});
