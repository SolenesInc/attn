import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, onTestFinished, vi } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { stubNavigatorPlatform } from './test/platformStub';
import type { ScriptedDaemon } from './test/scriptedDaemon';

type Keystroke = ['keydown' | 'keyup', KeyboardEventInit, string[]?];

interface Launch {
  platform?: string;
  output?: string;
  settings?: Record<string, string>;
}

const LINUX = 'Linux x86_64';
const SESSIONS = ['s1', 's2', 's3'];

function keystroke(type: Keystroke[0], init: KeyboardEventInit, modifierStates: string[] = []): KeyboardEvent {
  const event = new KeyboardEvent(type, { bubbles: true, cancelable: true, ...init });
  Object.defineProperty(event, 'getModifierState', { value: (name: string) => modifierStates.includes(name) });
  return event;
}

async function openTerminal({ platform, output = '', settings = {} }: Launch = {}) {
  if (platform) onTestFinished(stubNavigatorPlatform(platform));
  const view = await openAttachedTerminals({
    sessions: SESSIONS.map((id) => daemonSession(id, { state: 'idle' })),
    workspaces: SESSIONS.map(agentWorkspace),
    initialState: { settings },
    output: output ? { s1: output } : {},
  });
  const terminal = screen.getByRole('textbox', { name: 'Terminal input' });
  const typed = async (...strokes: Keystroke[]) => {
    const before = view.daemon.sentOf('pty_input').length;
    for (const [type, init, states] of strokes) fireEvent(terminal, keystroke(type, init, states));
    await view.daemon.idle();
    return view.daemon.sentOf('pty_input').slice(before).map((command) => command.data);
  };
  return { ...view, terminal, typed };
}

const press = (init: KeyboardEventInit, states?: string[]): Keystroke => ['keydown', init, states];
const release = (init: KeyboardEventInit): Keystroke => ['keyup', init];

function pickerTitle() {
  return screen.queryByTestId('location-picker-title')?.textContent;
}

describe('App terminal input', () => {
  it.each<[string, Launch, Keystroke[], string[]]>([
    ['arrows in normal cursor mode', {}, [press({ key: 'ArrowUp', code: 'ArrowUp' }), press({ key: 'ArrowDown', code: 'ArrowDown' })], ['\x1b[A', '\x1b[B']],
    ['arrows in application cursor mode', { output: '\x1b[?1h' }, [press({ key: 'ArrowUp', code: 'ArrowUp' }), press({ key: 'ArrowDown', code: 'ArrowDown' })], ['\x1bOA', '\x1bOB']],
    ['layout text', {}, [press({ key: 'a', code: 'KeyA' }), press({ key: 'A', code: 'KeyA', shiftKey: true })], ['a', 'A']],
    [
      'reverse-tab for Shift+Tab, including WebKit’s ISO_Left_Tab, and a plain tab for Tab',
      {},
      [press({ key: 'Tab', code: 'Tab', shiftKey: true }), press({ key: 'ISO_Left_Tab', code: 'Tab', shiftKey: true }), press({ key: 'Tab', code: 'Tab' })],
      ['\x1b[Z', '\x1b[Z', '\t'],
    ],
    ['macOS Option as terminal Alt', {}, [press({ key: 'ƒ', code: 'KeyF', altKey: true })], ['\x1bf']],
    ['Option once the program turns Alt-prefixing off', { output: '\x1b[?1036l' }, [press({ key: 'ƒ', code: 'KeyF', altKey: true })], []],
    ['Option once the program turns Alt-prefixing back on', { output: '\x1b[?1036l\x1b[?1036h' }, [press({ key: 'ƒ', code: 'KeyF', altKey: true })], ['\x1bf']],
    ['modifyOtherKeys', { output: '\x1b[>4;2m' }, [press({ key: 'I', code: 'KeyI', ctrlKey: true, shiftKey: true })], ['\x1b[27;6;73~']],
    [
      'Kitty press, repeat and release',
      { output: '\x1b[>31u' },
      [press({ key: 'a', code: 'KeyA' }), press({ key: 'a', code: 'KeyA', repeat: true }), release({ key: 'a', code: 'KeyA' })],
      ['\x1b[97;;97u', '\x1b[97;1:2;97u', '\x1b[97;1:3u'],
    ],
    [
      'Kitty AltGraph repeat and right-side release',
      { output: '\x1b[>31u' },
      [
        press({ key: '@', code: 'AltRight', repeat: true, ctrlKey: true, altKey: true, location: 2 }, ['AltGraph', 'CapsLock', 'NumLock']),
        release({ key: 'Alt', code: 'AltRight', location: 2 }),
      ],
      ['\x1b[57449;199:2u', '\x1b[57449;1:3u'],
    ],
    ['numeric keypad', {}, [press({ key: '1', code: 'Numpad1' })], ['1']],
    ['application keypad', { output: '\x1b[?1035l\x1b=' }, [press({ key: '1', code: 'Numpad1' })], ['\x1bOq']],
    ['a bare Alt', {}, [press({ key: 'Alt', code: 'AltLeft', altKey: true })], []],
    ['macOS copy and paste chords', {}, [press({ key: 'c', code: 'KeyC', metaKey: true }), press({ key: 'v', code: 'KeyV', metaKey: true })], []],
    [
      'Linux Ctrl+C and Ctrl+V, keeping Ctrl+Shift+C for copy',
      { platform: LINUX },
      [
        press({ key: 'c', code: 'KeyC', ctrlKey: true }),
        press({ key: 'C', code: 'KeyC', ctrlKey: true, shiftKey: true }),
        press({ key: 'v', code: 'KeyV', ctrlKey: true }),
      ],
      ['\x03', '\x16'],
    ],
    ['the Mac clipboard chords on Linux', { platform: LINUX }, [press({ key: 'c', code: 'KeyC', metaKey: true }), press({ key: 'v', code: 'KeyV', metaKey: true })], []],
  ])('sends %s to the PTY as the program expects', async (_, options, strokes, bytes) => {
    const { typed } = await openTerminal(options);

    expect(await typed(...strokes)).toEqual(bytes);
  });

  it('tells the daemon who produced each byte: the program’s query answered by the terminal, or the user', async () => {
    const { daemon, typed } = await openTerminal();

    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa('\x1b[5n') });
    await daemon.idle();
    await typed(press({ key: 'a', code: 'KeyA' }));

    expect(daemon.sentOf('pty_input').map(({ data, source }) => ({ data, source }))).toEqual([
      { data: '\x1b[0n', source: 'response' },
      { data: 'a', source: 'user' },
    ]);
  });

  it('runs an attn shortcut typed in the terminal without leaking its press or release to the PTY', async () => {
    const { typed } = await openTerminal({ output: '\x1b[>31u' });

    expect(await typed(press({ key: 'k', code: 'KeyK', metaKey: true }), release({ key: 'k', code: 'KeyK' }))).toEqual([]);
    expect(screen.getByRole('dialog', { name: 'Action menu' })).toBeInTheDocument();
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
    const { daemon, typed } = await openTerminal();

    expect(await typed(press(init))).toEqual([]);

    effect(daemon);
  });

  it('runs a leader chord typed in the terminal, and swallows the leader and a stray follow key', async () => {
    const { typed } = await openTerminal({
      settings: {
        keybindings_config: JSON.stringify({
          version: 1,
          overrides: { 'ui.actionMenu': { leader: { key: 'e', meta: true }, then: { key: 'a' } } },
        }),
      },
    });

    expect(await typed(press({ key: 'e', metaKey: true }), press({ key: 'x' }))).toEqual([]);
    expect(screen.queryByRole('dialog', { name: 'Action menu' })).toBeNull();

    expect(await typed(press({ key: 'e', metaKey: true }), press({ key: 'a' }))).toEqual([]);
    expect(screen.getByRole('dialog', { name: 'Action menu' })).toBeInTheDocument();
  });

  it('sends dead-key composition once, as the composed character', async () => {
    const { daemon, terminal, typed } = await openTerminal();

    expect(await typed(press({ key: 'Dead', code: 'KeyU', altKey: true }))).toEqual([]);
    fireEvent(terminal, new CompositionEvent('compositionstart', { bubbles: true }));
    fireEvent.keyDown(terminal, { key: 'a', code: 'KeyA' });
    terminal.appendChild(document.createTextNode('preedit'));
    fireEvent(terminal, Object.defineProperty(new CompositionEvent('compositionend', { bubbles: true }), 'data', { value: 'å' }));
    await daemon.idle();

    expect(daemon.sentOf('pty_input').map((command) => command.data)).toEqual(['å']);
    expect(terminal.textContent).toBe('');
  });

  it.each([
    ['plain', '', 'one\rtwo\rthree'],
    ['bracketed', '\x1b[?2004h', '\x1b[200~one\rtwo\rthree\x1b[201~'],
  ])('pastes multi-line text into a %s-paste terminal with carriage returns', async (_, output, bytes) => {
    const { daemon, terminal } = await openTerminal({ output });

    fireEvent.paste(terminal, { clipboardData: { items: [], getData: () => 'one\r\ntwo\nthree' } });
    await daemon.idle();

    expect(daemon.sentOf('pty_input').map((command) => command.data)).toEqual([bytes]);
  });
});
