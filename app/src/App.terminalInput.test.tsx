import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const SESSIONS = ['s1', 's2', 's3'];

async function openTerminal(settings: Record<string, string> = {}) {
  const view = await openAttachedTerminals({
    sessions: SESSIONS.map((id) => daemonSession(id, { state: 'idle' })),
    workspaces: SESSIONS.map(agentWorkspace),
    initialState: { settings },
  });
  const input = screen.getByRole('textbox', { name: 'Terminal input' });
  const type = async (init: KeyboardEventInit) => {
    fireEvent.keyDown(input, init);
    await view.daemon.idle();
  };
  return { ...view, type };
}

function typed(daemon: ScriptedDaemon) {
  return daemon.sentOf('pty_input').map(({ data, source }) => ({ data, source }));
}

function pickerTitle() {
  return screen.queryByTestId('location-picker-title')?.textContent;
}

describe('App terminal input', () => {
  it('tells the daemon who produced each byte: the program’s query answered by the terminal, or the user', async () => {
    const { daemon, type } = await openTerminal();

    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa('\x1b[5n') });
    await daemon.idle();
    await type({ key: 'a', code: 'KeyA' });

    expect(typed(daemon)).toEqual([
      { data: '\x1b[0n', source: 'response' },
      { data: 'a', source: 'user' },
    ]);
  });

  it('sends reverse-tab for Shift+Tab, including WebKit’s ISO_Left_Tab, and a plain tab for Tab', async () => {
    const { daemon, type } = await openTerminal();

    await type({ key: 'Tab', code: 'Tab', shiftKey: true });
    await type({ key: 'ISO_Left_Tab', code: 'Tab', shiftKey: true });
    await type({ key: 'Tab', code: 'Tab' });

    expect(typed(daemon).map(({ data }) => data)).toEqual(['\x1b[Z', '\x1b[Z', '\t']);
  });

  it.each([
    ['⌘T opens the new workspace picker', { key: 't', metaKey: true }, () => expect(pickerTitle()).toBe('New Workspace Location')],
    ['⌘⇧N opens the new session picker', { key: 'N', metaKey: true, shiftKey: true }, () => expect(pickerTitle()).toBe('New Session Location')],
    ['⌘/ opens the shortcut cheatsheet', { key: '/', metaKey: true }, () => expect(screen.getByRole('dialog')).toHaveTextContent('Keyboard Shortcuts')],
    ['⌘Q quits', { key: 'q', metaKey: true }, () => expect(window.close).toHaveBeenCalledOnce()],
    ['⌘2 jumps to the second workspace by its key code', { key: '™', code: 'Digit2', metaKey: true }, (daemon: ScriptedDaemon) => expect(daemon.sentOf('workspace_selected').slice(-1)[0]).toEqual({ cmd: 'workspace_selected', workspace_id: 'workspace-s2' })],
    ['⌘3 jumps to the third workspace by its key', { key: '3', metaKey: true }, (daemon: ScriptedDaemon) => expect(daemon.sentOf('workspace_selected').slice(-1)[0]).toEqual({ cmd: 'workspace_selected', workspace_id: 'workspace-s3' })],
    ['⌘W closes a lone session', { key: 'w', metaKey: true }, (daemon: ScriptedDaemon) => expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([{ cmd: 'workspace_layout_close_pane', workspace_id: 'workspace-s1', pane_id: 'pane-s1' }])],
  ])('%s from the terminal without typing it into the program', async (_, init, effect) => {
    vi.spyOn(window, 'close').mockImplementation(() => {});
    const { daemon, type } = await openTerminal();

    await type(init);

    effect(daemon);
    expect(typed(daemon)).toEqual([]);
  });

  it('runs a leader chord typed in the terminal, and swallows the leader and a stray follow key', async () => {
    const { daemon, type } = await openTerminal({
      keybindings_config: JSON.stringify({
        version: 1,
        overrides: { 'ui.actionMenu': { leader: { key: 'e', meta: true }, then: { key: 'a' } } },
      }),
    });

    await type({ key: 'e', metaKey: true });
    await type({ key: 'x' });
    expect(screen.queryByRole('dialog', { name: 'Action menu' })).toBeNull();

    await type({ key: 'e', metaKey: true });
    await type({ key: 'a' });
    expect(screen.getByRole('dialog', { name: 'Action menu' })).toBeInTheDocument();
    expect(typed(daemon)).toEqual([]);
  });
});
