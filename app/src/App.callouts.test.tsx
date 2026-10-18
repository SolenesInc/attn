import { invoke, isTauri } from '@tauri-apps/api/core';
import { act, fireEvent, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { agentPane, daemonDesktop, daemonSession, soloDesktop } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import type { EventMessage } from './test/protocol';
import { serveSettings } from './test/settings';

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.classList.contains('callout-card')) return new DOMRect(0, 0, 300, 130);
    return new DOMRect(10, 10, 180, 30);
  });
  HTMLElement.prototype.scrollIntoView = vi.fn();
});

async function launch(queue = false) {
  const settings = { queue_mode_enabled: String(queue), callouts_enabled: 'true' };
  const view = await renderApp({ initialState: {
    sessions: [daemonSession('first'), daemonSession('second')],
    desktops: [soloDesktop('first', { shortcut_slot: 1 }), soloDesktop('second', { shortcut_slot: 2 })], settings,
  } });
  serveSettings(view.daemon, settings);
  view.daemon.on('callout_result', () => undefined);
  return view;
}

async function show(daemon: ScriptedDaemon, steps: EventMessage<'callout_show_request'>['steps'], requestId = 'callout-1') {
  daemon.emit({ event: 'callout_show_request', request_id: requestId, from: 'Fern', steps });
  await daemon.idle();
  return daemon.sentOf('callout_result').slice(-1)[0];
}

const card = () => screen.getByRole('dialog', { name: 'Callout from Fern' });

describe('App callouts', () => {
  it('answers with visible anchors and renders the Card beside its target', async () => {
    const { daemon } = await launch();
    daemon.emit({ event: 'callout_anchors_request', request_id: 'list' });
    await daemon.idle();
    expect(daemon.sentOf('callout_result')).toEqual([{
      cmd: 'callout_result', request_id: 'list', on_screen: ['sidebar', 'home', 'commands', 'garden', 'desktops', 'settings-button'],
    }]);
    expect(await show(daemon, [{ anchor: 'garden', text: 'Work lives here' }])).toEqual({
      cmd: 'callout_result', request_id: 'callout-1', on_screen: ['sidebar', 'home', 'commands', 'garden', 'desktops', 'settings-button'],
    });
    expect(card()).toHaveTextContent('Work lives here');
    expect(card()).toHaveAttribute('data-side', 'right');
    expect(screen.getByTestId('callout-ring')).toHaveStyle({ left: '7px', top: '7px', width: '186px', height: '36px' });
  });

  it('takes unmodified Enter and right arrow, ends with Esc and outside clicks', async () => {
    const { daemon } = await launch();
    await show(daemon, [{ anchor: 'sidebar', text: 'First' }, { anchor: 'garden', text: 'Second' }, { anchor: 'home', text: 'Last' }]);
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Enter', shiftKey: true }));
    expect(card()).toHaveTextContent('First');
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Enter' }));
    expect(card()).toHaveTextContent('Second');
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'ArrowRight' }));
    expect(card()).toHaveTextContent('Last');
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(screen.queryByTestId('callout-overlay')).not.toBeInTheDocument();
    await show(daemon, [{ anchor: 'sidebar', text: 'Again' }]);
    await gesture(daemon, () => fireEvent.pointerDown(document.body));
    expect(screen.queryByTestId('callout-overlay')).not.toBeInTheDocument();
    await show(daemon, [{ anchor: 'sidebar', text: 'Finish' }]);
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'ArrowRight' }));
    expect(screen.queryByTestId('callout-overlay')).not.toBeInTheDocument();
  });

  it('refuses the whole callout for a missing fixed anchor and keeps the previous one', async () => {
    const { daemon } = await launch();
    await show(daemon, [{ anchor: 'garden', text: 'Keep me' }]);
    expect(await show(daemon, [{ anchor: 'garden', text: 'Not accepted' }, { anchor: 'queue', text: 'Missing' }], 'refused')).toMatchObject({
      request_id: 'refused', missing: ['queue'],
    });
    expect(card()).toHaveTextContent('Keep me');
    await show(daemon, [{ anchor: 'commands', text: 'Replacement' }], 'new');
    expect(screen.getAllByTestId('callout-overlay')).toHaveLength(1);
    expect(card()).toHaveTextContent('Replacement');
  });

  it('reveals an agent in queue flow and Enter uses normal agent navigation', async () => {
    const { daemon } = await launch(true);
    expect(screen.queryByTestId('queue-agent-list')).not.toBeInTheDocument();
    await show(daemon, [{ anchor: 'agent', session_id: 'second', label: 'Second agent', text: 'Needs you' }]);
    await act(() => vi.advanceTimersToNextFrame());
    await daemon.idle();
    expect(screen.getByTestId('queue-agent-list')).toBeInTheDocument();
    expect(screen.getByTestId('callout-ring')).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Enter' }));
    expect(daemon.sentOf('desktop_show_session')).toEqual([{ cmd: 'desktop_show_session', session_id: 'second', request_id: expect.any(String) }]);
    expect(screen.queryByTestId('callout-overlay')).not.toBeInTheDocument();
  });

  it('shows a dynamic fallback on the queue bar, skips with right arrow, and goes there with Enter', async () => {
    const { daemon } = await launch(true);
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    await show(daemon, [{ anchor: 'agent', session_id: 'second', label: 'Second agent', text: 'Needs you' }, { anchor: 'sidebar', text: 'Next' }]);
    expect(card()).toHaveTextContent("Second agent isn't on screen");
    expect(screen.queryByTestId('callout-ring')).not.toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'ArrowRight' }));
    expect(card()).toHaveTextContent('Next');
    expect(daemon.sentOf('desktop_show_session')).toEqual([]);
    await show(daemon, [{ anchor: 'agent', session_id: 'second', label: 'Second agent', text: 'Needs you' }], 'fallback');
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Enter' }));
    expect(daemon.sentOf('desktop_show_session')).toEqual([{ cmd: 'desktop_show_session', session_id: 'second', request_id: expect.any(String) }]);
  });

  it('lets shortcuts open another overlay and immediately gives way', async () => {
    const { daemon } = await launch();
    await show(daemon, [{ anchor: 'sidebar', text: 'Hello' }]);
    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    expect(screen.queryByTestId('callout-overlay')).not.toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Commands' })).toBeInTheDocument();
  });

  it('refuses a show request loudly while another overlay is already open', async () => {
    const { daemon } = await launch();
    await gesture(daemon, () => pressShortcut('ui.openSettings'));
    expect(await show(daemon, [
      { anchor: 'sidebar', text: 'Blocked' },
      { anchor: 'agent', session_id: 'second', text: 'Blocked agent' },
      { anchor: 'desktop', desktop_id: 'desktop-second', text: 'Blocked desktop' },
    ])).toEqual({ cmd: 'callout_result', request_id: 'callout-1', on_screen: [], missing: ['sidebar', 'agent', 'desktop'] });
    expect(screen.queryByTestId('callout-overlay')).not.toBeInTheDocument();
  });

  it('keeps a callout visible beside a drawer, including after the drawer has been used', async () => {
    const { daemon } = await launch();
    await gesture(daemon, () => pressShortcut('dock.attention'));
    expect(await show(daemon, [{ anchor: 'sidebar', text: 'Alongside a drawer' }])).not.toHaveProperty('missing');
    expect(card()).toHaveTextContent('Alongside a drawer');
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    await gesture(daemon, () => pressShortcut('dock.attention'));
    await show(daemon, [{ anchor: 'sidebar', text: 'After a drawer' }], 'after-drawer');
    expect(card()).toHaveTextContent('After a drawer');
  });

  it('updates the ring when its target grows without moving the Card', async () => {
    let sidebarHeight = 300;
    vi.mocked(HTMLElement.prototype.getBoundingClientRect).mockImplementation(function (this: HTMLElement) {
      if (this.classList.contains('callout-card')) return new DOMRect(0, 0, 300, 130);
      if (this.dataset.calloutAnchor === 'sidebar') return new DOMRect(10, 10, 180, sidebarHeight);
      return new DOMRect(0, 0, 1024, 768);
    });
    const { daemon } = await launch();
    await show(daemon, [{ anchor: 'sidebar', text: 'Growing target' }]);
    const before = card().getAttribute('style');
    expect(screen.getByTestId('callout-ring')).toHaveStyle({ height: '306px' });
    sidebarHeight = 400;
    await gesture(daemon, () => fireEvent(window, new Event('resize')));
    expect(card().getAttribute('style')).toBe(before);
    expect(screen.getByTestId('callout-ring')).toHaveStyle({ height: '406px' });
  });

  it.each([
    [false, false, ['sidebar', 'home', 'commands', 'garden', 'desktops', 'settings-button']],
    [false, true, ['sidebar', 'home', 'commands', 'garden', 'desktops', 'settings-button']],
    [true, false, ['sidebar', 'home', 'commands', 'garden', 'desktops', 'queue', 'settings-button']],
    [true, true, ['sidebar', 'desktops', 'queue', 'settings-button']],
  ])('lists anchors for queue=%s collapsed=%s', async (queue, collapsed, anchors) => {
    const { daemon } = await launch(queue);
    if (collapsed) await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    daemon.emit({ event: 'callout_anchors_request', request_id: 'shape' });
    await daemon.idle();
    expect(daemon.sentOf('callout_result')).toEqual([{ cmd: 'callout_result', request_id: 'shape', on_screen: anchors }]);
    await show(daemon, [{ anchor: 'sidebar', text: 'This shape' }]);
    expect(card()).toHaveAttribute('data-side', queue && collapsed ? 'below' : 'right');
  });

  it('does not list Settings on the hidden Home view and Enter goes to a desktop', async () => {
    const { daemon } = await launch();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open first' })));
    const result = await show(daemon, [{ anchor: 'settings-button', text: 'Hidden' }]);
    expect(result).toMatchObject({ missing: ['settings-button'] });
    expect(result?.on_screen).not.toContain('settings-button');
    expect(screen.queryByTestId('callout-overlay')).not.toBeInTheDocument();
    await show(daemon, [{ anchor: 'desktop', desktop_id: 'desktop-second', label: 'Other desktop', text: 'Go here' }]);
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Enter' }));
    expect(daemon.sentOf('desktop_set_current').map(({ desktop_id }) => desktop_id)).toEqual(['desktop-second']);
    expect(screen.queryByTestId('callout-overlay')).not.toBeInTheDocument();
  });

  it('hides the native browser tile until the callout ends', async () => {
    vi.mocked(isTauri).mockReturnValue(true);
    const desktop = daemonDesktop('ws', {
      root: { type: 'split', split_id: 'split', direction: 'vertical', ratio: 0.5, children: [
        { type: 'pane', pane_id: 'pane-first' },
        { type: 'tile', tile_id: 'web', tile_kind: 'browser', tile_params: 'https://example.com/' },
      ] }, panes: [agentPane('first', 'ws')],
    });
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('first')], desktops: [desktop] } });
    daemon.on('callout_result', () => undefined);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open first' })));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open example.com' })));
    const updates = () => vi.mocked(invoke).mock.calls.filter(([cmd]) => cmd === 'browser_host_update').map(([, args]) => args);
    expect(updates().slice(-1)[0]).toMatchObject({ geometry: { visible: true } });
    expect(await show(daemon, [{ anchor: 'sidebar', text: 'Browser hidden' }])).not.toHaveProperty('missing');
    expect(card()).toHaveTextContent('Browser hidden');
    expect(updates().slice(-1)[0]).toMatchObject({ geometry: { visible: false } });
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(updates().slice(-1)[0]).toMatchObject({ geometry: { visible: true } });
  });

  it('saves the Experimental opt-in', async () => {
    const { daemon } = await launch();
    await gesture(daemon, () => pressShortcut('ui.openSettings'));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Experimental/ })));
    const toggle = screen.getByRole('switch', { name: 'Agent callouts' });
    expect(toggle).toHaveAttribute('aria-checked', 'true');
    await gesture(daemon, () => fireEvent.click(toggle));
    expect(daemon.sentOf('set_setting').filter(({ key }) => key === 'callouts_enabled')).toEqual([{ cmd: 'set_setting', key: 'callouts_enabled', value: 'false', request_id: expect.any(String) }]);
  });
});
