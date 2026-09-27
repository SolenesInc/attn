import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, soloDesktop, daemonSession, daemonDesktop, defaultProfile, splitDesktop } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

const renderedPaneSessions = () =>
  Array.from(document.querySelectorAll('[data-pane-session-id]')).map((pane) => pane.getAttribute('data-pane-session-id'));

const selectedSession = () => document.querySelector('.session-item.selected .session-label')?.textContent ?? null;

describe('App session and desktop sync', () => {
  it('lists shell sessions next to agents, as the daemon reports them', async () => {
    const shellPane = agentPane('sh1', 'desktop-1');
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('a1'), daemonSession('sh1', { agent: 'shell' })],
        desktops: [daemonDesktop('desktop-1', {
          root: { type: 'split', split_id: 'root', direction: 'vertical', ratio: 0.5, children: [{ type: 'pane', pane_id: 'pane-a1' }, { type: 'pane', pane_id: 'pane-sh1' }] },
          panes: [agentPane('a1', 'desktop-1'), shellPane],
        })],
      },
    });
    expect(screen.getByRole('button', { name: 'Open sh1' })).toBeInTheDocument();

    daemon.emit({ event: 'session_registered', session: daemonSession('sh2', { agent: 'shell' }) });
    daemon.arrangement.place('sh2', 'pane-sh2', 'desktop-1');
    daemon.emit(daemon.arrangement.changed());

    expect(screen.getByRole('button', { name: 'Open sh2' })).toBeInTheDocument();
  });

  it('updates a renamed session in place without duplicating the sidebar row', async () => {
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] } });

    daemon.emit({ event: 'session_state_changed', session: daemonSession('s1', { label: 'renamed' }) });

    expect(screen.getAllByRole('button', { name: 'Open renamed' })).toHaveLength(1);
    expect(screen.queryByRole('button', { name: 'Open s1' })).toBeNull();
  });

  it('keeps a desktop when the daemon stops listing its agent session, so the session can come back into it', async () => {
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')], profiles: [defaultProfile('desktop-s1')] } });

    daemon.emit({ event: 'sessions_updated', sessions: [] });
    daemon.emit({ event: 'sessions_updated', sessions: [daemonSession('s1')] });

    expect(renderedPaneSessions()).toEqual(['s1']);
  });

  it('moves to the session left on the desktop when the open one closes', async () => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('elsewhere'), daemonSession('root'), daemonSession('split')],
        desktops: [soloDesktop('elsewhere'), splitDesktop('ws', ['root', 'split'])],
      },
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open split' })));
    expect(selectedSession()).toBe('split');

    daemon.emit({ event: 'session_unregistered', session: daemonSession('split') });
    daemon.arrange((desktops) => desktops.map((desktop) => (desktop.id === 'ws' ? splitDesktop('ws', ['root'], { revision: desktop.revision + 1 }) : desktop)));
    await daemon.idle();

    expect(selectedSession()).toBe('root');
  });

  it.each([
    ['keeps a session still launching into its pane', 'launching' as const, true],
    ['drops a session that already ran', 'idle' as const, false],
  ])('%s when a daemon snapshot omits it', async (_, state, kept) => {
    const spawning = daemonDesktop('ws', { root: { type: 'pane', pane_id: 'pane-booting' }, panes: [{ ...agentPane('booting', 'ws'), status: 'spawning' }] });
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('booting', { state })], desktops: [spawning] } });

    await gesture(daemon, () => daemon.emit({ event: 'sessions_updated', sessions: [] }));

    expect(screen.queryByRole('button', { name: 'Open booting' }) !== null).toBe(kept);
  });
});
