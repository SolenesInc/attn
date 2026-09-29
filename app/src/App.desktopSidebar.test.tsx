import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, daemonEndpoint, daemonSession, daemonDesktop, daemonSeed, defaultProfile, dockTiles, splitDesktop } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

function sidebar() {
  return Array.from(document.querySelectorAll('.desktop-row'), (row) => {
    const rows = Array.from(row.querySelectorAll('.session-item .session-label'), (label) => label.textContent);
    const placeholder = row.querySelector('[data-testid="desktop-neutral-indicator"]')?.getAttribute('title');
    return [row.querySelector('.desktop-label')?.textContent, rows, ...(placeholder ? [placeholder] : [])];
  });
}

describe('App desktop sidebar', () => {
  it('names tile kinds, shows a bound remote host, and opens a seed tile once', async () => {
    const { daemon } = await renderApp({
      initialState: {
        desktops: [daemonDesktop('d1', {
          root: dockTiles({ type: 'pane', pane_id: 'pane-remote' }, [
            { tile_id: 'tile-seed', tile_kind: 'seed', tile_params: 's-plan', tile_session_id: 'remote' },
            { tile_id: 'tile-web', tile_kind: 'browser', tile_params: 'https://docs.test/path' },
          ]),
          panes: [agentPane('remote', 'd1')],
        })],
        endpoints: [daemonEndpoint('ep-1')],
        sessions: [daemonSession('remote', { endpoint_id: 'ep-1' })],
        seeds: [daemonSeed('s-plan', { title: 'Native client plan' })],
        profiles: [defaultProfile('d1')],
      },
    });

    const seed = screen.getByTestId('sidebar-tile-d1-tile-seed');
    expect(seed).toHaveAttribute('title', 's-plan');
    expect(within(seed).getByText('Native client plan')).toBeInTheDocument();
    expect(within(seed).getByText('SEED')).toBeInTheDocument();
    expect(within(seed).getByText('gpu-box')).toBeInTheDocument();

    const browser = screen.getByTestId('sidebar-tile-d1-tile-web');
    expect(browser).toHaveAttribute('title', 'https://docs.test/path');
    expect(within(browser).getByText('WEB')).toBeInTheDocument();
    expect(within(browser).queryByText('gpu-box')).toBeNull();

    await gesture(daemon, () => fireEvent.click(within(seed).getByRole('button', { name: 'Open Native client plan' })));
    expect(daemon.sentOf('desktop_show_leaf')).toEqual([
      expect.objectContaining({ desktop_id: 'd1', leaf_id: 'tile-seed' }),
    ]);
  });

  it('lists desktops in order, each with its laid-out sessions and tiles in layout order', async () => {
    await renderApp({
      initialState: {
        desktops: [
          splitDesktop('c', ['c1'], { order_key: 'a2', name: 'c' }),
          daemonDesktop('a', {
            root: {
              type: 'split',
              split_id: 'split-a',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', pane_id: 'pane-a1' },
                { type: 'tile', tile_id: 'tile-docs', tile_kind: 'browser', tile_params: 'https://docs.test' },
                { type: 'pane', pane_id: 'pane-a2' },
              ],
            },
            panes: [agentPane('a1', 'a'), agentPane('a2', 'a')],
          }, { order_key: 'a0', name: 'a' }),
          splitDesktop('b', ['b1'], { order_key: 'a1', name: 'b' }),
        ],
        sessions: ['c1', 'a2', 'a1', 'unplaced', 'b1'].map((id) => daemonSession(id)),
        profiles: [defaultProfile('a')],
      },
    });

    expect(sidebar()).toEqual([
      ['a', ['a1', 'docs.test', 'a2']],
      ['b', ['b1']],
      ['c', ['c1']],
      ['Not on a desktop', ['unplaced']],
    ]);
  });

  it('marks a desktop whose pane is launching, failed or lost its agent, and keeps it', async () => {
    const { daemon } = await renderApp({
      initialState: {
        desktops: [
          daemonDesktop('launching', { root: { type: 'pane', pane_id: 'pane-l1' }, panes: [{ ...agentPane('l1', 'launching'), status: 'spawning' }] }, { name: 'launching' }),
          daemonDesktop('failed', { root: { type: 'pane', pane_id: 'pane-closed' }, panes: [{ ...agentPane('closed', 'failed'), status: 'failed' }] }, { name: 'failed' }),
          splitDesktop('stale', ['gone'], { name: 'stale' }),
          splitDesktop('live', ['live1'], { name: 'live' }),
        ],
        sessions: [daemonSession('live1')],
        profiles: [defaultProfile('live')],
      },
    });
    const unresolved = sidebar().filter(([, , placeholder]) => placeholder).map(([title]) => title);
    expect(sidebar().map(([title, rows]) => [title, rows])).toEqual([
      ['failed', []],
      ['launching', []],
      ['live', ['live1']],
      ['stale', []],
    ]);
    expect(unresolved).toEqual(['failed', 'launching', 'stale']);

    daemon.emit({ event: 'session_unregistered', session: daemonSession('live1') });
    await daemon.idle();

    expect(sidebar().map(([title, rows]) => [title, rows])).toEqual([
      ['failed', []],
      ['launching', []],
      ['live', []],
      ['stale', []],
    ]);
  });
});
