import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { ShortcutEditorModal } from './ShortcutEditorModal';
import { SettingsProvider } from '../contexts/SettingsContext';
import { KeybindingsProvider } from '../contexts/KeybindingsContext';
import { KEYBINDINGS_SETTING_KEY, setShortcutOverrides } from '../shortcuts/resolver';

afterEach(() => setShortcutOverrides({}));

function renderEditor(initial: Record<string, string> = {}) {
  const setSetting = vi.fn();
  const onClose = vi.fn();
  render(
    <SettingsProvider settings={initial} setSetting={setSetting}>
      <KeybindingsProvider>
        <ShortcutEditorModal isOpen onClose={onClose} />
      </KeybindingsProvider>
    </SettingsProvider>,
  );
  return { setSetting, onClose };
}

function row(label: string): HTMLElement {
  const el = screen.getByText(label).closest('.shortcut-editor-row');
  if (!el) throw new Error(`row not found: ${label}`);
  return el as HTMLElement;
}

function lastConfig(setSetting: ReturnType<typeof vi.fn>) {
  const calls = setSetting.mock.calls.filter(([k]) => k === KEYBINDINGS_SETTING_KEY);
  return JSON.parse(calls[calls.length - 1][1]);
}

describe('ShortcutEditorModal', () => {
  it('renders categories and current bindings', () => {
    renderEditor();
    expect(screen.getByRole('dialog', { name: 'Customize Shortcuts' })).toBeInTheDocument();
    expect(screen.getByText('Workspaces & Sessions')).toBeInTheDocument();
    expect(screen.getByText('Panes & Terminals')).toBeInTheDocument();

    const newSession = row('New session in this workspace');
    expect(newSession.textContent).toContain('⌘');
    expect(newSession.textContent).toContain('N');
  });

  it('marks protected shortcuts as required and hides their unbind control', () => {
    renderEditor();
    const settings = row('Settings');
    expect(within(settings).getByText('Required')).toBeInTheDocument();
    expect(within(settings).queryByTitle('Unbind')).toBeNull();

    const newSession = row('New session in this workspace');
    expect(within(newSession).getByTitle('Unbind')).toBeInTheDocument();
  });

  it('unbinds a shortcut and persists the override', () => {
    const { setSetting } = renderEditor();
    fireEvent.click(within(row('New session in this workspace')).getByTitle('Unbind'));

    expect(lastConfig(setSetting).overrides['session.new']).toBeNull();
    expect(row('New session in this workspace').textContent).toContain('Unassigned');
  });

  it('reassigns a conflicting combo, unbinding the previous holder', () => {
    const { setSetting } = renderEditor();

    const newSession = row('New session in this workspace');
    fireEvent.click(newSession.querySelector('.key-capture-button')!);
    fireEvent.keyDown(window, { key: 'd', code: 'KeyD', metaKey: true, shiftKey: true });

    const reassignBtn = screen.getByText('Reassign');
    expect(within(newSession).getByText(/Split pane sideways/)).toBeInTheDocument();
    fireEvent.click(reassignBtn);

    const cfg = lastConfig(setSetting);
    expect(cfg.overrides['session.new']).toEqual({ key: 'd', meta: true, shift: true });
    expect(cfg.overrides['terminal.splitHorizontal']).toBeNull();
  });

  it('runs conflict detection when resetting a shortcut whose default is now claimed', () => {
    const { setSetting } = renderEditor({
      [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
        version: 1,
        overrides: {
          'session.new': { key: 'j', meta: true },
          'terminal.splitHorizontal': { key: 'n', meta: true },
        },
      }),
    });

    fireEvent.click(within(row('New session in this workspace')).getByTitle('Reset to ⌘N'));

    const reassignBtn = screen.getByText('Reassign');
    expect(within(row('New session in this workspace')).getByText(/Split pane sideways/)).toBeInTheDocument();
    fireEvent.click(reassignBtn);

    const cfg = lastConfig(setSetting);
    expect('session.new' in cfg.overrides).toBe(false);
    expect(cfg.overrides['terminal.splitHorizontal']).toBeNull();
  });

  it('pins a shortcut to the dock from its row star', () => {
    const { setSetting } = renderEditor();
    const newSession = row('New session in this workspace');
    fireEvent.click(within(newSession).getByLabelText('Add to dock'));

    expect(lastConfig(setSetting).dock.items).toContain('session.new');
    expect(within(newSession).getByLabelText('Remove from dock')).toBeInTheDocument();
  });

  it('reorders dock items with the up/down controls', () => {
    const { setSetting } = renderEditor({
      [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
        version: 1,
        overrides: {},
        dock: { collapsed: false, items: ['terminal.toggleZoom', 'dock.attention'] },
      }),
    });

    fireEvent.click(screen.getByLabelText('Move Zoom active pane down'));

    expect(lastConfig(setSetting).dock.items).toEqual(['dock.attention', 'terminal.toggleZoom']);
  });

  it('removes a dock item from the dock section', () => {
    const { setSetting } = renderEditor({
      [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
        version: 1,
        overrides: {},
        dock: { collapsed: false, items: ['terminal.toggleZoom', 'dock.attention'] },
      }),
    });

    fireEvent.click(screen.getByLabelText('Remove Zoom active pane from dock'));

    expect(lastConfig(setSetting).dock.items).toEqual(['dock.attention']);
  });

  function recordChord(label: string, leader: KeyboardEventInit, follow: KeyboardEventInit) {
    fireEvent.click(within(row(label)).getByLabelText('Record a chord'));
    fireEvent.keyDown(window, leader);
    fireEvent.keyDown(window, follow);
  }

  it('records a chord on a row and persists it as the override', () => {
    const { setSetting } = renderEditor();
    recordChord('New session in this workspace', { key: 'y', metaKey: true }, { key: 'd' });
    expect(lastConfig(setSetting).overrides['session.new']).toEqual({
      leader: { key: 'y', meta: true },
      then: { key: 'd' },
    });
  });

  it('persists a chord whose leader equals the row’s own default combo', () => {
    const { setSetting } = renderEditor();
    recordChord('Action menu', { key: 'k', metaKey: true }, { key: 'd' });
    expect(lastConfig(setSetting).overrides['ui.actionMenu']).toEqual({
      leader: { key: 'k', meta: true },
      then: { key: 'd' },
    });
  });

  it('resets in-flight recording when the editor closes and reopens', () => {
    const setSetting = vi.fn();
    const tree = (open: boolean) => (
      <SettingsProvider settings={{}} setSetting={setSetting}>
        <KeybindingsProvider>
          <ShortcutEditorModal isOpen={open} onClose={() => {}} />
        </KeybindingsProvider>
      </SettingsProvider>
    );
    const { rerender } = render(tree(true));

    fireEvent.click(within(row('Action menu')).getByLabelText('Record a chord'));
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    expect(within(row('Action menu')).queryByLabelText('Record a chord')).toBeNull();

    rerender(tree(false));
    rerender(tree(true));
    expect(within(row('Action menu')).getByLabelText('Record a chord')).toBeInTheDocument();
  });

  const filterInput = () => screen.getByLabelText('Filter shortcuts') as HTMLInputElement;

  it('filters rows to matching labels and hides the dock while searching', () => {
    renderEditor();
    fireEvent.change(filterInput(), { target: { value: 'maximize' } });

    expect(screen.getByText('Maximize active pane')).toBeInTheDocument();
    expect(screen.queryByText('New session in this workspace')).toBeNull();
    expect(screen.queryByText('Dock')).toBeNull();
    expect(screen.queryByText('Workspaces & Sessions')).toBeNull();
    expect(screen.getByText('Panes & Terminals')).toBeInTheDocument();
  });

  it('filters rows by the displayed key string', () => {
    renderEditor();
    fireEvent.change(filterInput(), { target: { value: '⌘⇧n' } });
    expect(screen.getByText('New session, split sideways')).toBeInTheDocument();
    expect(screen.queryByText('New session in this workspace')).toBeNull();
  });

  it('shows an announced, trimmed no-matches message when nothing matches', () => {
    renderEditor();
    fireEvent.change(filterInput(), { target: { value: '  zzznope  ' } });
    expect(screen.getByRole('status')).toHaveTextContent(/^No shortcuts match .zzznope.$/);
    expect(screen.queryByText('Panes & Terminals')).toBeNull();
  });

  it('clears a stranded reassign prompt when the user starts filtering', () => {
    renderEditor();
    fireEvent.click(row('New session in this workspace').querySelector('.key-capture-button')!);
    fireEvent.keyDown(window, { key: 'd', code: 'KeyD', metaKey: true, shiftKey: true });
    expect(screen.getByText('Reassign')).toBeInTheDocument();

    fireEvent.change(filterInput(), { target: { value: 'new session' } });
    expect(within(row('New session in this workspace')).queryByText('Reassign')).toBeNull();
  });

  it('clears recording when the filter is focused, so the first keystroke is not captured as a binding', () => {
    const { setSetting } = renderEditor();
    fireEvent.click(row('Maximize active pane').querySelector('.key-capture-button')!);
    expect(within(row('Maximize active pane')).queryByLabelText('Record a chord')).toBeNull();

    // A recording row owns a capture-phase window keydown listener: focusing the filter
    // must tear it down BEFORE any keystroke, or the first character rebinds the row.
    fireEvent.focus(filterInput());
    expect(within(row('Maximize active pane')).getByLabelText('Record a chord')).toBeInTheDocument();

    fireEvent.change(filterInput(), { target: { value: 'zoom' } });
    expect(filterInput().value).toBe('zoom');
    expect(setSetting.mock.calls.filter(([k]) => k === KEYBINDINGS_SETTING_KEY)).toHaveLength(0);
  });

  it('clears the filter when the editor closes and reopens', () => {
    const setSetting = vi.fn();
    const tree = (open: boolean) => (
      <SettingsProvider settings={{}} setSetting={setSetting}>
        <KeybindingsProvider>
          <ShortcutEditorModal isOpen={open} onClose={() => {}} />
        </KeybindingsProvider>
      </SettingsProvider>
    );
    const { rerender } = render(tree(true));

    fireEvent.change(filterInput(), { target: { value: 'split' } });
    expect(filterInput().value).toBe('split');

    rerender(tree(false));
    rerender(tree(true));
    expect(filterInput().value).toBe('');
  });

  it('reset button tooltip names the default binding', () => {
    renderEditor({
      [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
        version: 1,
        overrides: { 'session.new': { key: 'm', meta: true } },
      }),
    });
    expect(
      within(row('New session in this workspace')).getByTitle('Reset to ⌘N'),
    ).toBeInTheDocument();
  });

  it('badges only the shortcuts gated behind an open terminal', () => {
    renderEditor();
    expect(within(row('Maximize active pane')).getByText('Needs terminal')).toBeInTheDocument();
    expect(within(row('New session in this workspace')).queryByText('Needs terminal')).toBeNull();
    expect(within(row('Collapse utility terminal')).queryByText('Needs terminal')).toBeNull();
  });

  it('shows both Customized and Needs terminal on an overridden gated row', () => {
    renderEditor({
      [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
        version: 1,
        overrides: { 'terminal.find': { key: 'y', meta: true } },
      }),
    });
    const r = row('Find in terminal');
    expect(within(r).getByText('Customized')).toBeInTheDocument();
    expect(within(r).getByText('Needs terminal')).toBeInTheDocument();
  });

  it('restores defaults', () => {
    const { setSetting } = renderEditor({
      [KEYBINDINGS_SETTING_KEY]: JSON.stringify({
        version: 1,
        overrides: { 'session.new': { key: 'm', meta: true } },
      }),
    });
    expect(within(row('New session in this workspace')).getByText('Customized')).toBeInTheDocument();

    fireEvent.click(screen.getByText('Restore Defaults'));
    expect(lastConfig(setSetting).overrides).toEqual({});
  });
});
