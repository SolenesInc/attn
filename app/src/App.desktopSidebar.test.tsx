import { describe, expect, it } from 'vitest';
import { agentPane, daemonSession, daemonDesktop, defaultProfile, splitDesktop } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';

function sidebar() {
  return Array.from(document.querySelectorAll('.desktop-row'), (row) => {
    const rows = Array.from(row.querySelectorAll('.session-item .session-label'), (label) => label.textContent);
    const placeholder = row.querySelector('[data-testid="desktop-neutral-indicator"]')?.getAttribute('title');
    return [row.querySelector('.desktop-label')?.textContent, rows, ...(placeholder ? [placeholder] : [])];
  });
}

describe('App desktop sidebar', () => {
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
