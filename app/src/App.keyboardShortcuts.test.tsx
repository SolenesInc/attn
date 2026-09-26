import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, onTestFinished } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import { agentWorkspace, daemonSession, splitWorkspace, workspaceWithTiles, type DaemonSession, type DaemonWorkspace } from './test/daemonFixtures';
import { stubNavigatorPlatform } from './test/platformStub';

const LINUX = 'Linux x86_64';

function keybindings(overrides: Record<string, unknown>) {
  return { keybindings_config: JSON.stringify({ version: 1, overrides }) };
}

const BROWSER_TILE = { tile_id: 'tile-browser', tile_kind: 'browser', tile_params: 'https://example.test' };

async function openBrowserBesideAgent(settings: Record<string, string> = {}) {
  const view = await openWorkspace({
    settings,
    sessions: [daemonSession('s1', { state: 'idle', workspace_id: 'ws' }), daemonSession('s2', { state: 'idle' })],
    workspaces: [workspaceWithTiles([BROWSER_TILE]), agentWorkspace('s2')],
  });
  return { ...view, address: screen.getByRole('textbox', { name: 'Browser address' }) };
}

async function openWorkspace({
  platform,
  settings = {},
  sessions = [daemonSession('s1', { state: 'idle' })],
  workspaces = [agentWorkspace('s1')],
}: { platform?: string; settings?: Record<string, string>; sessions?: DaemonSession[]; workspaces?: DaemonWorkspace[] } = {}) {
  if (platform) onTestFinished(stubNavigatorPlatform(platform));
  const view = await openAttachedTerminals({ sessions, workspaces, initialState: { settings } });
  const terminal = (paneId = 'pane-s1') => document.querySelector<HTMLElement>(`[data-pane-id="${paneId}"] .terminal-container`)!;
  const press = async (target: Element, init: KeyboardEventInit) => {
    const delivered = fireEvent.keyDown(target, init);
    await view.daemon.idle();
    return delivered;
  };
  return { ...view, terminal, press };
}

function closedPanes(daemon: Awaited<ReturnType<typeof openWorkspace>>['daemon']) {
  return daemon.sentOf('workspace_layout_close_pane').map(({ pane_id }) => pane_id);
}

function ptyInput(daemon: Awaited<ReturnType<typeof openWorkspace>>['daemon']) {
  return daemon.sentOf('pty_input').map(({ data }) => data);
}

const zoomedPane = () => document.querySelector('.session-terminal-workspace[data-zoomed-pane-id]:not([data-zoomed-pane-id=""])')?.getAttribute('data-zoomed-pane-id') ?? null;
const chordHud = () => screen.queryByTestId('chord-leader-hud');

const ZOOM_CHORD = keybindings({ 'terminal.toggleZoom': { leader: { key: 'y', meta: true }, then: { key: 'z' } } });

describe('App keyboard shortcuts', () => {
  it('leaves plain Ctrl+letter in a Linux terminal to the shell, even when rebound', async () => {
    const { daemon, terminal, press } = await openWorkspace({
      platform: LINUX,
      settings: keybindings({ 'session.close': { key: 'w', meta: true } }),
    });

    await press(terminal(), { key: 'w', code: 'KeyW', ctrlKey: true });

    expect(ptyInput(daemon)).toEqual(['\x17']);
    expect(closedPanes(daemon)).toEqual([]);
  });

  it('closes the focused pane with Ctrl+Shift+W in a Linux terminal', async () => {
    const { daemon, terminal, press } = await openWorkspace({
      platform: LINUX,
      sessions: [daemonSession('s1', { state: 'idle', workspace_id: 'ws' }), daemonSession('s2', { state: 'idle', workspace_id: 'ws' })],
      workspaces: [splitWorkspace('ws', ['s1', 's2'])],
    });

    fireEvent.mouseDown(terminal('pane-s2'));
    expect(await press(terminal('pane-s2'), { key: 'W', code: 'KeyW', ctrlKey: true, shiftKey: true })).toBe(false);

    expect(closedPanes(daemon)).toEqual(['pane-s2']);
    expect(ptyInput(daemon)).toEqual([]);
  });

  it('closes the focused pane of a split with ⌘W, not the selected session', async () => {
    const { daemon, terminal, press } = await openWorkspace({
      sessions: [daemonSession('s1', { state: 'idle', workspace_id: 'ws' }), daemonSession('s2', { state: 'idle', workspace_id: 'ws' })],
      workspaces: [splitWorkspace('ws', ['s1', 's2'])],
    });

    fireEvent.mouseDown(terminal('pane-s2'));
    await press(terminal('pane-s2'), { key: 'w', metaKey: true });

    expect(closedPanes(daemon)).toEqual(['pane-s2']);
  });

  it.each([
    ['in its terminal', true],
    ['outside its terminal', false],
  ])('closes a lone agent session with ⌘W pressed %s', async (_, inTerminal) => {
    const { daemon, terminal, press } = await openWorkspace();

    await press(inTerminal ? terminal() : document.body, { key: 'w', metaKey: true });

    expect(closedPanes(daemon)).toEqual(['pane-s1']);
  });

  it('closes the session when the native menu sends ⌘W', async () => {
    const { daemon } = await openWorkspace();

    window.dispatchEvent(new CustomEvent('attn:native-shortcut', { detail: 'session.close' }));
    await daemon.idle();

    expect(closedPanes(daemon)).toEqual(['pane-s1']);
  });

  it('keeps text-editing shortcuts in the browser address bar, and app shortcuts that are not', async () => {
    const { daemon, press, address } = await openBrowserBesideAgent();

    expect(await press(address, { key: 'z', metaKey: true, shiftKey: true })).toBe(true);
    expect(await press(address, { key: 'f', metaKey: true })).toBe(true);
    expect(zoomedPane()).toBeNull();
    expect(screen.queryByTestId('ghostty-find-input')).toBeNull();

    const selections = daemon.sentOf('workspace_selected').length;
    expect(await press(address, { key: '1', code: 'Digit1', metaKey: true })).toBe(false);
    expect(daemon.sentOf('workspace_selected').slice(selections)).toEqual([{ cmd: 'workspace_selected', workspace_id: 'workspace-s2' }]);
  });

  it('opens terminal find with ⌘F from the terminal', async () => {
    const { terminal, press } = await openWorkspace();

    await press(terminal(), { key: 'f', metaKey: true });

    expect(screen.getByTestId('ghostty-find-input')).toBeInTheDocument();
  });

  it.each([
    ['macOS', undefined, { key: '[', code: 'BracketLeft', metaKey: true }, { key: ']', code: 'BracketRight', metaKey: true }],
    ['Linux', LINUX, { key: '{', code: 'BracketLeft', ctrlKey: true, shiftKey: true }, { key: '}', code: 'BracketRight', ctrlKey: true, shiftKey: true }],
  ])('walks agent history from a focused terminal on %s', async (_, platform, back, forward) => {
    const { daemon, terminal, press } = await openWorkspace({
      platform,
      sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'idle' })],
      workspaces: [agentWorkspace('s1'), agentWorkspace('s2')],
    });
    fireEvent.click(screen.getByRole('button', { name: 'Open s2' }));
    await daemon.idle();
    const selected = () => document.querySelector('.session-item.selected .session-label')?.textContent;

    await press(terminal('pane-s2'), back);
    expect(selected()).toBe('s1');
    await press(terminal('pane-s1'), forward);
    expect(selected()).toBe('s2');
    expect(ptyInput(daemon)).toEqual([]);
  });

  describe('leader-key chords', () => {
    it('fires on the follow key without either keystroke reaching the terminal', async () => {
      const { daemon, terminal, press } = await openWorkspace({ settings: ZOOM_CHORD });

      await press(terminal(), { key: 'y', metaKey: true });
      expect(chordHud()).toHaveTextContent('⌘Ythen…');
      await press(terminal(), { key: 'z' });

      expect(zoomedPane()).toBe('pane-s1');
      expect(chordHud()).toBeNull();
      expect(ptyInput(daemon)).toEqual([]);
    });

    it.each([
      ['an unbound key', [{ key: 'x' }]],
      ['Escape', [{ key: 'Escape' }]],
    ])('cancels on %s without running anything or typing it', async (_, keys) => {
      const { daemon, terminal, press } = await openWorkspace({ settings: ZOOM_CHORD });

      await press(terminal(), { key: 'y', metaKey: true });
      for (const key of keys) await press(terminal(), key);

      expect(chordHud()).toBeNull();
      expect(zoomedPane()).toBeNull();
      expect(ptyInput(daemon)).toEqual([]);
    });

    it.each([
      ['a lone modifier', [{ key: 'Shift', shiftKey: true }]],
      ['the leader again', [{ key: 'y', metaKey: true }]],
      ['an auto-repeated leader', [{ key: 'y', metaKey: true, repeat: true }]],
    ])('stays armed through %s', async (_, between) => {
      const { daemon, terminal, press } = await openWorkspace({ settings: ZOOM_CHORD });

      await press(terminal(), { key: 'y', metaKey: true });
      for (const key of between) await press(terminal(), key);
      await press(terminal(), { key: 'z' });

      expect(zoomedPane()).toBe('pane-s1');
      expect(ptyInput(daemon)).toEqual([]);
    });

    it('matches a follow key with modifiers exactly, and fires only the chord', async () => {
      const chord = keybindings({ 'terminal.toggleZoom': { leader: { key: 'y', meta: true }, then: { key: '1', code: 'Digit1', meta: true } } });
      const { daemon, terminal, press } = await openWorkspace({ settings: chord });
      const selections = daemon.sentOf('workspace_selected').length;

      await press(terminal(), { key: 'y', metaKey: true });
      await press(terminal(), { key: '1', code: 'Digit1' });
      expect(zoomedPane()).toBeNull();

      await press(terminal(), { key: 'y', metaKey: true });
      await press(terminal(), { key: '1', code: 'Digit1', metaKey: true });
      expect(zoomedPane()).toBe('pane-s1');
      expect(daemon.sentOf('workspace_selected').slice(selections)).toEqual([]);
      expect(ptyInput(daemon)).toEqual([]);
    });

    it('does not arm inside the browser address bar', async () => {
      const { press, address } = await openBrowserBesideAgent(ZOOM_CHORD);

      expect(await press(address, { key: 'y', metaKey: true })).toBe(true);
      expect(chordHud()).toBeNull();
      await press(address, { key: 'z' });

      expect(zoomedPane()).toBeNull();
    });

    it('swallows the leader of a chord whose action this view does not offer', async () => {
      const { daemon, terminal, press } = await openWorkspace({
        settings: keybindings({ 'markdown.sendAnnotations': { leader: { key: 'y', meta: true }, then: { key: 's' } } }),
      });

      await press(terminal(), { key: 'y', metaKey: true });

      expect(chordHud()).toBeNull();
      expect(ptyInput(daemon)).toEqual([]);
    });
  });
});
