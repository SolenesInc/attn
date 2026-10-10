import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import { crewMember, soloDesktop, daemonSeed, daemonSession, dockTiles } from './test/daemonFixtures';

const SEED = daemonSeed('s-7k3f9m', {
  title: 'Make seed IDs navigable',
  body: 'Recognize **valid** ids and [preview](https://example.test) them.',
  tender_member: 'trellis',
});

async function openTerminalShowing(output: string) {
  const view = await openAttachedTerminals({
    sessions: [daemonSession('s1', { state: 'idle' })],
    desktops: [soloDesktop('s1')],
    initialState: { seeds: [SEED], crew: [crewMember('trellis')] },
    output: { s1: output },
  });
  await act(() => vi.advanceTimersToNextFrame());
  return view;
}

function seedMarks() {
  return [...document.querySelectorAll<HTMLElement>('[data-terminal-seed-id]')].map((mark) => [
    mark.dataset.terminalSeedId,
    mark.style.left,
    mark.style.top,
    mark.style.width,
  ]);
}

describe('App terminal seed ids', () => {
  it('marks the seed ids the garden knows as whole words, on every row a wrapped one spans', async () => {
    await openTerminalShowing(`known s-7k3f9m; unknown s-2m8q4v; xs-7k3f9m s-7k3f9mx\r\n${' '.repeat(76)}s-7k3f9m`);

    expect(seedMarks()).toEqual([
      ['s-7k3f9m', '48px', '0px', '64px'],
      ['s-7k3f9m', '608px', '21px', '32px'],
      ['s-7k3f9m', '0px', '42px', '32px'],
    ]);
  });

  it('previews a marked seed on hover and opens it for the session', async () => {
    const { daemon } = await openTerminalShowing('known s-7k3f9m');

    fireEvent.mouseMove(document.querySelector('[data-pane-id="pane-s1"] canvas')!, { clientX: 70, clientY: 10 });
    await act(() => vi.advanceTimersByTimeAsync(200));
    const preview = screen.getByRole('dialog');

    expect(within(preview).getByRole('heading')).toHaveTextContent('Make seed IDs navigable');
    expect(preview).toHaveTextContent('Recognize valid ids and preview them.');
    expect(preview).toHaveTextContent('Trellis');

    fireEvent.click(within(preview).getByRole('button', { name: 'Open as tile' }));
    await daemon.idle();

    expect(daemon.sentOf('open_seed')).toEqual([expect.objectContaining({ seed_id: 's-7k3f9m', session_id: 's1' })]);
  });

  it('shows the seed tile it opened with one request and puts the keyboard in it', async () => {
    const { daemon } = await openTerminalShowing('known s-7k3f9m');
    daemon.on('open_seed', ({ request_id, seed_id }) => {
      daemon.arrangement.desktops = daemon.arrangement.desktops.map((desktop) => desktop.id !== 'desktop-s1' ? desktop : {
        ...desktop,
        tree_json: JSON.stringify(dockTiles({ type: 'pane', pane_id: 'pane-s1' }, [{ tile_id: 'tile-seed-s-7k3f9m', tile_kind: 'seed', tile_params: seed_id }])),
        revision: desktop.revision + 1,
      });
      return [
        { event: 'open_seed_result', request_id, success: true, seed_id, desktop_id: 'desktop-s1', tile_id: 'tile-seed-s-7k3f9m' },
        daemon.arrangement.changed(),
      ];
    });
    fireEvent.mouseMove(document.querySelector('[data-pane-id="pane-s1"] canvas')!, { clientX: 70, clientY: 10 });
    await act(() => vi.advanceTimersByTimeAsync(200));

    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Open as tile' }));
    await daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(100));

    expect(daemon.sentOf('desktop_show_leaf')).toEqual([expect.objectContaining({ desktop_id: 'desktop-s1', leaf_id: 'tile-seed-s-7k3f9m' })]);
    expect(daemon.sentOf('desktop_set_current')).toEqual([]);
    expect(document.activeElement?.closest('[data-pane-id]')?.getAttribute('data-pane-id')).toBe('tile-seed-s-7k3f9m');
  });

  it('opens the seed tile but leaves the keyboard on a control the user moved to while it opened', async () => {
    const { daemon } = await openTerminalShowing('known s-7k3f9m');
    const held: Array<Extract<(typeof daemon.sent)[number], { cmd: 'open_seed' }>> = [];
    daemon.on('open_seed', (command) => {
      held.push(command);
      return undefined;
    });
    fireEvent.mouseMove(document.querySelector('[data-pane-id="pane-s1"] canvas')!, { clientX: 70, clientY: 10 });
    await act(() => vi.advanceTimersByTimeAsync(200));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Open as tile' }));
    await daemon.idle();

    const home = screen.getByTestId('sidebar-home');
    act(() => home.focus());
    await act(async () => {
      for (const { request_id, seed_id } of held.splice(0)) {
        daemon.arrangement.desktops = daemon.arrangement.desktops.map((desktop) => desktop.id !== 'desktop-s1' ? desktop : {
          ...desktop,
          tree_json: JSON.stringify(dockTiles({ type: 'pane', pane_id: 'pane-s1' }, [{ tile_id: 'tile-seed-s-7k3f9m', tile_kind: 'seed', tile_params: seed_id }])),
          revision: desktop.revision + 1,
        });
        daemon.emit({ event: 'open_seed_result', request_id, success: true, seed_id, desktop_id: 'desktop-s1', tile_id: 'tile-seed-s-7k3f9m' });
        daemon.emit(daemon.arrangement.changed());
      }
    });
    await daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(100));

    expect(daemon.sentOf('desktop_show_leaf')).toEqual([expect.objectContaining({ leaf_id: 'tile-seed-s-7k3f9m' })]);
    expect(document.activeElement).toBe(home);
  });
});
