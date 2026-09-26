import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, onTestFinished } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { stubNavigatorPlatform } from './test/platformStub';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { serveSettings } from './test/settings';

const LINUX = 'Linux x86_64';
const KEY = 'keybindings_config';

interface Config {
  overrides?: Record<string, unknown>;
  dock?: { collapsed: boolean; items: string[] };
}

interface Launch {
  platform?: string;
  config?: Config;
  withSession?: boolean;
}

async function renderKeybindings({ platform, config, withSession = false }: Launch = {}) {
  if (platform) onTestFinished(stubNavigatorPlatform(platform));
  const settings: Record<string, string> = config ? { [KEY]: JSON.stringify({ version: 1, overrides: {}, ...config }) } : {};
  const view = await renderApp({
    initialState: {
      settings,
      ...(withSession ? { sessions: [daemonSession('s1')], workspaces: [agentWorkspace('s1')] } : {}),
    },
  });
  serveSettings(view.daemon, settings);
  return view;
}

async function openEditor(daemon: ScriptedDaemon) {
  await gesture(daemon, () => pressShortcut('ui.showShortcuts'));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Edit shortcuts' })));
  return screen.getByRole('dialog', { name: 'Customize Shortcuts' });
}

async function renderEditor(options: Launch = {}) {
  const view = await renderKeybindings(options);
  const editor = await openEditor(view.daemon);
  return { ...view, editor };
}

async function closeEditor(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Close shortcut editor' })));
}

function row(label: string): HTMLElement {
  const match = screen.getAllByText(label).map((element) => element.closest<HTMLElement>('.shortcut-editor-row')).find(Boolean);
  if (!match) throw new Error(`no editor row for ${label}`);
  return match;
}

function saved(daemon: ScriptedDaemon): Required<Config> {
  const writes = daemon.sentOf('set_setting').filter(({ key }) => key === KEY);
  return JSON.parse(writes[writes.length - 1].value);
}

function keybindingWrites(daemon: ScriptedDaemon) {
  return daemon.sentOf('set_setting').filter(({ key }) => key === KEY);
}

async function record(daemon: ScriptedDaemon, label: string, ...keys: KeyboardEventInit[]) {
  fireEvent.click(row(label).querySelector('.key-capture-button')!);
  for (const key of keys) fireEvent.keyDown(window, key);
  await daemon.idle();
}

async function recordChord(daemon: ScriptedDaemon, label: string, leader: KeyboardEventInit, follow: KeyboardEventInit) {
  fireEvent.click(within(row(label)).getByLabelText('Record a chord'));
  fireEvent.keyDown(window, leader);
  fireEvent.keyDown(window, follow);
  await daemon.idle();
}

const filter = () => screen.getByLabelText('Filter shortcuts') as HTMLInputElement;
const newSessionDialog = () => screen.queryByTestId('location-picker-overlay');

describe('App shortcut editor', () => {
  it('lists every category with the current bindings, and protects the required ones', async () => {
    const { editor } = await renderEditor();

    expect(within(editor).getByRole('heading', { name: 'Workspaces & Sessions' })).toBeInTheDocument();
    expect(within(editor).getByRole('heading', { name: 'Panes & Terminals' })).toBeInTheDocument();
    expect(row('New session in this workspace')).toHaveTextContent('⌘N');
    for (const label of ['Previous workspace', 'Next workspace', 'Back through agent history', 'Forward through agent history']) {
      expect(within(row(label)).getByTitle('Unbind')).toBeInTheDocument();
    }
    expect(within(row('Settings')).getByText('Required')).toBeInTheDocument();
    expect(within(row('Settings')).queryByTitle('Unbind')).toBeNull();
  });

  it('shows Linux defaults on Linux, and resetting there removes the override', async () => {
    const { daemon } = await renderEditor({ platform: LINUX, config: { overrides: { 'session.new': { key: 'm', meta: true, shift: true } } } });
    expect(row('New session in this workspace')).toHaveTextContent('CtrlShiftM');

    await gesture(daemon, () => fireEvent.click(within(row('New session in this workspace')).getByTitle('Reset to Ctrl+Shift+N')));

    expect(saved(daemon).overrides).toEqual({});
  });

  it('marks a rebound shortcut as customized and offers a reset to its default', async () => {
    await renderEditor({ config: { overrides: { 'session.new': { key: 'm', meta: true } } } });

    expect(within(row('New session in this workspace')).getByText('Customized')).toBeInTheDocument();
    expect(within(row('New session in this workspace')).getByTitle('Reset to ⌘N')).toBeInTheDocument();
  });

  it('badges the shortcuts that need an open terminal, customized or not', async () => {
    await renderEditor({ config: { overrides: { 'terminal.find': { key: 'y', meta: true } } } });

    expect(within(row('Focus active pane')).getByText('Needs terminal')).toBeInTheDocument();
    expect(within(row('New session in this workspace')).queryByText('Needs terminal')).toBeNull();
    expect(within(row('Collapse utility terminal')).queryByText('Needs terminal')).toBeNull();
    expect(within(row('Find in terminal')).getByText('Customized')).toBeInTheDocument();
    expect(within(row('Find in terminal')).getByText('Needs terminal')).toBeInTheDocument();
  });

  it('unbinds a shortcut, which frees its keys for another action', async () => {
    const { daemon } = await renderEditor();

    await gesture(daemon, () => fireEvent.click(within(row('New session in this workspace')).getByTitle('Unbind')));
    expect(saved(daemon).overrides['session.new']).toBeNull();
    expect(row('New session in this workspace')).toHaveTextContent('Unassigned');

    await record(daemon, 'Focus active pane', { key: 'n', code: 'KeyN', metaKey: true });
    expect(screen.queryByRole('button', { name: 'Reassign' })).toBeNull();
    expect(saved(daemon).overrides['terminal.toggleMaximize']).toEqual({ key: 'n', meta: true });
  });

  it.each([
    ['a shortcut on the same keys', { key: 'p', code: 'KeyP', metaKey: true, shiftKey: true }, 'PRs drawer'],
    ['a shortcut on the same physical key', { key: '&', code: 'Digit1', metaKey: true }, 'Jump to workspace 1'],
  ])('asks before taking the keys of %s', async (_, keys, holder) => {
    const { daemon } = await renderEditor();

    await record(daemon, 'New session in this workspace', keys);

    expect(row('New session in this workspace')).toHaveTextContent(`is “${holder}”.`);
    expect(keybindingWrites(daemon)).toEqual([]);
  });

  it('reassigns claimed keys, unbinding their previous holder', async () => {
    const { daemon } = await renderEditor();

    await record(daemon, 'New session in this workspace', { key: 'd', code: 'KeyD', metaKey: true, shiftKey: true });
    expect(row('New session in this workspace')).toHaveTextContent('Split pane sideways');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Reassign' })));

    expect(saved(daemon).overrides).toEqual({
      'session.new': { key: 'd', meta: true, shift: true },
      'terminal.splitHorizontal': null,
    });
  });

  it('asks before resetting a shortcut onto a default another action now holds', async () => {
    const { daemon } = await renderEditor({
      config: { overrides: { 'session.new': { key: 'j', meta: true }, 'terminal.splitHorizontal': { key: 'n', meta: true } } },
    });

    await gesture(daemon, () => fireEvent.click(within(row('New session in this workspace')).getByTitle('Reset to ⌘N')));
    expect(row('New session in this workspace')).toHaveTextContent('Split pane sideways');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Reassign' })));

    expect(saved(daemon).overrides).toEqual({ 'terminal.splitHorizontal': null });
  });

  it('lets Close session keep ⌘W, which it shares with closing a pane', async () => {
    const { daemon } = await renderEditor({ config: { overrides: { 'session.close': { key: 'j', meta: true, alt: true } } } });

    await record(daemon, 'Close session (or focused pane)', { key: 'w', code: 'KeyW', metaKey: true });

    expect(screen.queryByRole('button', { name: 'Reassign' })).toBeNull();
    expect(saved(daemon).overrides).toEqual({});
  });

  it.each([
    ['macOS', undefined, { key: 'm', code: 'KeyM', metaKey: true, shiftKey: true }, { key: 'm', meta: true, shift: true }],
    ['Linux', LINUX, { key: 'k', code: 'KeyK', ctrlKey: true }, { key: 'k', meta: true }],
  ])('records keys as the %s accelerator, waiting past a lone modifier', async (_, platform, keys, binding) => {
    const { daemon } = await renderEditor({ platform });

    await record(daemon, 'Focus active pane', { key: 'Meta', code: 'MetaLeft', metaKey: true }, keys);

    expect(saved(daemon).overrides['terminal.toggleMaximize']).toEqual(binding);
  });

  it('refuses Control as a modifier on macOS', async () => {
    const { daemon } = await renderEditor();

    await record(daemon, 'Focus active pane', { key: 'k', code: 'KeyK', ctrlKey: true });

    expect(row('Focus active pane')).toHaveTextContent('Control isn’t available as a shortcut modifier on macOS.');
    expect(keybindingWrites(daemon)).toEqual([]);
  });

  it('warns about keys without an accelerator, for shortcuts and chord leaders alike', async () => {
    const { daemon } = await renderEditor();

    await record(daemon, 'Focus active pane', { key: 'q', code: 'KeyQ' });
    expect(row('Focus active pane').querySelector('.key-capture-button')).toHaveAttribute('title', 'No ⌘/⌥ modifier — this may collide with typing in the terminal');

    fireEvent.click(within(row('Zoom active pane')).getByLabelText('Record a chord'));
    fireEvent.keyDown(window, { key: 'y', code: 'KeyY' });
    await daemon.idle();
    expect(row('Zoom active pane')).toHaveTextContent('A chord leader needs a ⌘ or ⌥ modifier.');
  });

  it('saves a recorded chord, which fires once the daemon echoes the setting back', async () => {
    const { daemon } = await renderEditor();

    await recordChord(daemon, 'Toggle grid view', { key: 'y', code: 'KeyY', metaKey: true }, { key: 'g', code: 'KeyG' });
    expect(saved(daemon).overrides['view.toggleGrid']).toEqual({ leader: { key: 'y', meta: true }, then: { key: 'g' } });
    await closeEditor(daemon);

    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'y', code: 'KeyY', metaKey: true }));
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'g', code: 'KeyG' }));
    expect(screen.getByRole('region', { name: 'Session grid' })).toBeInTheDocument();
  });

  it('saves a chord whose leader is the row’s own default keys', async () => {
    const { daemon } = await renderEditor();

    await recordChord(daemon, 'Action menu', { key: 'k', code: 'KeyK', metaKey: true }, { key: 'd', code: 'KeyD' });

    expect(saved(daemon).overrides['ui.actionMenu']).toEqual({ leader: { key: 'k', meta: true }, then: { key: 'd' } });
  });

  it('asks before a chord takes a leader another shortcut uses', async () => {
    const { daemon } = await renderEditor();

    await recordChord(daemon, 'Zoom active pane', { key: 'g', code: 'KeyG', metaKey: true }, { key: 'x', code: 'KeyX' });

    expect(row('Zoom active pane')).toHaveTextContent('is “Toggle grid view”.');
  });

  it('pins, reorders and removes dock shortcuts, and the sidebar dock follows', async () => {
    const { daemon } = await renderEditor({ withSession: true, config: { dock: { collapsed: false, items: ['terminal.toggleZoom', 'dock.attention'] } } });
    const dock = () => Array.from(document.querySelectorAll('.sidebar-dock-items .shortcut-hint'), (item) => item.getAttribute('title') ?? item.textContent);

    await gesture(daemon, () => fireEvent.click(within(row('New session in this workspace')).getByLabelText('Add to dock')));
    expect(saved(daemon).dock.items).toEqual(['terminal.toggleZoom', 'dock.attention', 'session.new']);
    expect(within(row('New session in this workspace')).getByLabelText('Remove from dock')).toBeInTheDocument();

    await gesture(daemon, () => fireEvent.click(screen.getByLabelText('Move Zoom active pane down')));
    expect(saved(daemon).dock.items).toEqual(['dock.attention', 'terminal.toggleZoom', 'session.new']);

    await gesture(daemon, () => fireEvent.click(screen.getByLabelText('Remove Zoom active pane from dock')));
    expect(saved(daemon).dock.items).toEqual(['dock.attention', 'session.new']);
    expect(dock()).toHaveLength(2);
    expect(dock()[1]).toContain('New session');
  });

  it('restores every default', async () => {
    const { daemon } = await renderEditor({ config: { overrides: { 'session.new': { key: 'm', meta: true } } } });

    await gesture(daemon, () => fireEvent.click(screen.getByText('Restore Defaults')));

    expect(saved(daemon).overrides).toEqual({});
  });

  it.each([
    ['a label', 'focus active', ['Focus active pane'], ['New session in this workspace']],
    ['the displayed keys', '⌘⇧n', ['New session, split sideways'], ['New session in this workspace']],
  ])('filters by %s and hides the dock while filtering', async (_, query, shown, hidden) => {
    await renderEditor();

    fireEvent.change(filter(), { target: { value: query } });

    for (const label of shown) expect(screen.getByText(label)).toBeInTheDocument();
    for (const label of hidden) expect(screen.queryByText(label)).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Dock' })).toBeNull();
  });

  it('announces a filter that matches nothing', async () => {
    await renderEditor();

    fireEvent.change(filter(), { target: { value: '  zzznope  ' } });

    expect(screen.getByRole('status')).toHaveTextContent(/^No shortcuts match .zzznope.$/);
    expect(screen.queryByRole('heading', { name: 'Panes & Terminals' })).toBeNull();
  });

  it('never records keys typed into the filter, nor keeps a pending reassign', async () => {
    const { daemon } = await renderEditor();

    await record(daemon, 'New session in this workspace', { key: 'd', code: 'KeyD', metaKey: true, shiftKey: true });
    fireEvent.change(filter(), { target: { value: 'new session' } });
    expect(screen.queryByRole('button', { name: 'Reassign' })).toBeNull();

    fireEvent.change(filter(), { target: { value: '' } });
    fireEvent.click(row('Focus active pane').querySelector('.key-capture-button')!);
    fireEvent.focus(filter());
    fireEvent.keyDown(filter(), { key: 'z', code: 'KeyZ' });
    fireEvent.change(filter(), { target: { value: 'zoom' } });
    await daemon.idle();

    expect(filter().value).toBe('zoom');
    expect(keybindingWrites(daemon)).toEqual([]);
  });

  it('reopens with no filter and nothing recording', async () => {
    const { daemon } = await renderEditor();
    fireEvent.change(filter(), { target: { value: 'action' } });
    fireEvent.click(within(row('Action menu')).getByLabelText('Record a chord'));
    fireEvent.keyDown(window, { key: 'k', code: 'KeyK', metaKey: true });

    await closeEditor(daemon);
    await openEditor(daemon);

    expect(filter().value).toBe('');
    expect(within(row('Action menu')).getByLabelText('Record a chord')).toBeInTheDocument();
  });

  it.each([
    ['macOS', undefined, { key: 'n', metaKey: true }],
    ['Linux', LINUX, { key: 'N', ctrlKey: true, shiftKey: true }],
  ])('opens a new session with the %s default', async (_, platform, keys) => {
    const { daemon } = await renderKeybindings({ platform });

    await gesture(daemon, () => fireEvent.keyDown(window, keys));

    expect(newSessionDialog()).toBeInTheDocument();
  });

  it('follows a saved rebinding', async () => {
    const { daemon } = await renderKeybindings({ config: { overrides: { 'session.new': { key: 'm', meta: true } } } });
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'n', metaKey: true }));
    expect(newSessionDialog()).toBeNull();
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'm', metaKey: true }));
    expect(newSessionDialog()).toBeInTheDocument();
  });

  it('follows a saved unbinding', async () => {
    const { daemon } = await renderKeybindings({ config: { overrides: { 'session.new': null } } });
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'n', metaKey: true }));
    expect(newSessionDialog()).toBeNull();
  });
});
