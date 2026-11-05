import { act, createEvent, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import { soloDesktop, daemonSeed, daemonSession, dockTiles, memberClaim, sessionClaim } from './test/daemonFixtures';
import type { DaemonSession, Seed as DaemonSeed } from './hooks/useDaemonSocket';
import type { EventMessage } from './test/protocol';

const SEED = daemonSeed('s-7k3f9m', {
  title: 'Make seed IDs navigable',
  body: 'Recognize **valid** ids and [preview](https://example.test) them.',
  ...memberClaim('trellis'),
});

async function openTerminalShowing(output: string) {
  const view = await openAttachedTerminals({
    sessions: [daemonSession('s1', { state: 'idle' })],
    desktops: [soloDesktop('s1')],
    initialState: { seeds: [SEED] },
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

const OUTPOST: EventMessage<'outposts_updated'>['outposts'][number] = {
  id: 'studio-id', name: 'studio', destination: 'studio:2200', state: 'connected', reach: 'reachable',
  state_since: '2026-10-18T18:00:00Z',
};

async function hoverSeedBesideAgent(seed: Partial<DaemonSeed>, agent: Partial<DaemonSession> = {}, outposts = false) {
  const view = await openAttachedTerminals({
    sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { label: 'Seed reviewer', state: 'working', ...agent })],
    desktops: [soloDesktop('s1'), soloDesktop('s2')],
    initialState: {
      seeds: [daemonSeed('s-7k3f9m', { title: 'Make seed IDs navigable', ...seed })],
      ...(outposts ? { outposts: [OUTPOST], settings: { outposts_enabled: 'true' } } : {}),
    },
    output: { s1: 'known s-7k3f9m' },
  });
  await act(() => vi.advanceTimersToNextFrame());
  fireEvent.mouseMove(document.querySelector('[data-pane-id="pane-s1"] canvas')!, { clientX: 70, clientY: 10 });
  await act(() => vi.advanceTimersByTimeAsync(200));
  return { ...view, preview: screen.getByRole('dialog') };
}

async function goesToAgent(seed: Partial<DaemonSeed>, agent: Partial<DaemonSession> = {}, outposts = false) {
  const { daemon, preview } = await hoverSeedBesideAgent(seed, agent, outposts);
  const showsBefore = daemon.sentOf('desktop_show_session').length;

  fireEvent.click(within(preview).getByRole('button', { name: 'Go to Seed reviewer' }));
  await daemon.idle();

  expect(daemon.sentOf('desktop_show_session').slice(showsBefore)).toEqual([expect.objectContaining({ session_id: 's2' })]);
  expect(document.querySelector('[data-session-visible="1"] [data-pane-id="pane-s2"]')).not.toBeNull();
  expect(screen.queryByRole('dialog')).toBeNull();
}

const OTHER_SEED = daemonSeed('s-2m8q4v', { title: 'Another seed' });

async function openWritingTerminal() {
  const view = await openAttachedTerminals({
    sessions: [daemonSession('s1', { state: 'idle' })],
    desktops: [soloDesktop('s1')],
    initialState: { seeds: [SEED, OTHER_SEED] },
    output: { s1: '' },
  });
  await act(() => vi.advanceTimersToNextFrame());
  let seq = 1;
  const write = async (text: string) => {
    seq += 1;
    view.daemon.emit({ event: 'pty_output', id: 's1', seq, data: btoa(String.fromCharCode(...new TextEncoder().encode(text))) });
    await view.daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(100));
  };
  return { ...view, write };
}

const blankLines = (count: number) => '\r\n'.repeat(count);

describe('App terminal seed ids', () => {
  it('follows the ids a writing terminal scrolls into and out of view, and hover opens the one under the pointer', async () => {
    const { write } = await openWritingTerminal();

    await write('first s-7k3f9m\r\n');
    expect(seedMarks()).toEqual([['s-7k3f9m', '48px', '0px', '64px']]);

    await write(`${blankLines(10)}second s-2m8q4v\r\n`);
    expect(seedMarks()).toEqual([
      ['s-7k3f9m', '48px', '0px', '64px'],
      ['s-2m8q4v', '56px', '231px', '64px'],
    ]);

    await write(blankLines(13));
    expect(seedMarks()).toEqual([['s-2m8q4v', '56px', '189px', '64px']]);

    await write(blankLines(10));
    expect(seedMarks()).toEqual([]);

    await write('third s-7k3f9m');
    expect(seedMarks()).toEqual([['s-7k3f9m', '48px', '483px', '64px']]);
    await act(() => vi.advanceTimersToNextFrame());
    expect(seedMarks()).toEqual([['s-7k3f9m', '48px', '483px', '64px']]);

    fireEvent.mouseMove(document.querySelector('[data-pane-id="pane-s1"] canvas')!, { clientX: 70, clientY: 490 });
    await act(() => vi.advanceTimersByTimeAsync(200));
    expect(within(screen.getByRole('dialog')).getByRole('heading')).toHaveTextContent('Make seed IDs navigable');
  });

  it('marks an id that wraps between its s and its dash', async () => {
    const { write } = await openWritingTerminal();

    await write(`${' '.repeat(79)}s-7k3f9m`);

    expect(seedMarks()).toEqual([
      ['s-7k3f9m', '632px', '0px', '8px'],
      ['s-7k3f9m', '0px', '21px', '56px'],
    ]);
  });

  it('marks an id glued to a letter with a combining mark, where the mark ends the word', async () => {
    const { write } = await openWritingTerminal();

    await write('e\u0301s-7k3f9m');

    expect(seedMarks().map(([id]) => id)).toEqual(['s-7k3f9m']);
  });

  it('marks an id deep in a line that wraps past the logical line cap', async () => {
    const { write } = await openWritingTerminal();

    await write(`${' '.repeat(80 * 7 - 3)}s-7k3f9m`);

    expect(seedMarks()).toContainEqual(['s-7k3f9m', '616px', '126px', '24px']);
    expect(seedMarks()).toContainEqual(['s-7k3f9m', '0px', '147px', '40px']);
  });

  it('marks ids in history while the user reads it and the terminal keeps writing', async () => {
    const { write } = await openWritingTerminal();
    await write(`old s-7k3f9m\r\n${blankLines(30)}`);
    expect(seedMarks()).toEqual([]);

    const surface = document.querySelector('[data-pane-id="pane-s1"] .terminal-container')!;
    await act(async () => {
      for (let turn = 0; turn < 10; turn += 1) {
        const wheel = createEvent.wheel(surface, { deltaY: -1, deltaMode: 1 });
        Object.defineProperties(wheel, { clientX: { value: 20 }, clientY: { value: 20 } });
        fireEvent(surface, wheel);
      }
    });
    await act(() => vi.advanceTimersToNextFrame());
    expect(seedMarks().map(([id]) => id)).toEqual(['s-7k3f9m']);

    await write('more output\r\n');
    expect(seedMarks().map(([id]) => id)).toEqual(['s-7k3f9m']);
  });

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

  it('goes from the seed card to the session tending the seed', async () => {
    await goesToAgent(sessionClaim('s2', { name: 'Seed reviewer' }));
  });

  it('goes from the card of a seed nobody tends to the last session that tended it while that session is open', async () => {
    await goesToAgent({ status: 'parked', ...sessionClaim('s2', { live: false }), last_execution_id: 's2' });
  });

  it('goes from the seed card to a tender on an outpost', async () => {
    await goesToAgent(sessionClaim('s2', { name: 'Seed reviewer' }), { host: OUTPOST.id }, true);
  });

  it.each([
    ['its last tender closed', { status: 'harvested', last_execution_id: 'closed-session' }],
    ['nobody tended it', { status: 'planted' }],
    ['its tender is the terminal showing it', sessionClaim('s1')],
  ])('offers no agent on the seed card when %s', async (_case, seed) => {
    const { preview } = await hoverSeedBesideAgent(seed);

    expect(within(preview).getAllByRole('button').map((button) => button.getAttribute('aria-label'))).toEqual(['Open as tile']);
  });
});
