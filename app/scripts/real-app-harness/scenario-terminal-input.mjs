#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { delay } from './platform.mjs';
import {
  captureSessionArtifacts,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const KEY = Object.freeze({
  BACKSPACE: { key: 'Backspace', code: 'Backspace' },
  F5: { key: 'F5', code: 'F5' },
  HOME: { key: 'Home', code: 'Home' },
  PAGE_UP: { key: 'PageUp', code: 'PageUp' },
  DELETE: { key: 'Delete', code: 'Delete' },
  END: { key: 'End', code: 'End' },
  PAGE_DOWN: { key: 'PageDown', code: 'PageDown' },
  LEFT: { key: 'ArrowLeft', code: 'ArrowLeft' },
  RIGHT: { key: 'ArrowRight', code: 'ArrowRight' },
  DOWN: { key: 'ArrowDown', code: 'ArrowDown' },
  UP: { key: 'ArrowUp', code: 'ArrowUp' },
  ENTER: { key: 'Enter', code: 'Enter' },
});

const NAVIGATION_HEX = [
  '1b5b48',       // Home: ESC [ H
  '1b5b46',       // End: ESC [ F
  '1b5b337e',     // Delete: ESC [ 3 ~
  '1b5b357e',     // Page Up: ESC [ 5 ~
  '1b5b367e',     // Page Down: ESC [ 6 ~
  '1b5b31357e',   // F5: ESC [ 15 ~
  '01',           // Ctrl+A
].join('');
const APPLICATION_CURSOR_HEX = '1b4f411b4f42'; // Up + Down: ESC O A, ESC O B
const KITTY_REPEAT_HEX = [
  '1b5b39373b3b393775',          // press:  CSI 97;;97u
  '1b5b39373b313a323b393775',    // repeat: CSI 97;1:2;97u
  '1b5b39373b313a3375',          // release: CSI 97;1:3u
].join('');

const AFTER_KEYS_SENTINEL = '\x1e';

const CAPTURE_PROGRAM = String.raw`
const label = process.argv[2];
const mode = process.argv[3] || 'normal';
const sentinel = Buffer.from([0x1e]);
if (!process.stdin.isTTY || typeof process.stdin.setRawMode !== 'function') {
  throw new Error('terminal input capture requires a TTY');
}
const start = mode === 'cursor'
  ? '\x1b[?1h'
  : mode === 'bracketed'
    ? '\x1b[?1l\x1b[?2004h'
    : mode === 'kitty'
      ? '\x1b[?1l\x1b[?2004l\x1b[>31u'
      : '\x1b[?1l\x1b[?2004l';
const reset = mode === 'cursor'
  ? '\x1b[?1l'
  : mode === 'bracketed'
    ? '\x1b[?2004l'
    : mode === 'kitty'
      ? '\x1b[<u'
      : '';
const chunks = [];
let done = false;
function finish(captured) {
  if (done) return;
  done = true;
  process.stdin.setRawMode(false);
  process.stdin.pause();
  const hex = captured.toString('hex');
  let receipt = '\r\n' + reset + 'INPUT_BEGIN_' + label + '\r\n';
  for (let offset = 0; offset < hex.length; offset += 20) {
    receipt += 'INPUT_CHUNK_' + label + '=' + hex.slice(offset, offset + 20) + '\r\n';
  }
  receipt += 'INPUT_END_' + label + '\r\n';
  process.stdout.write(receipt);
}
process.stdin.setRawMode(true);
process.stdin.resume();
process.stdin.on('data', (chunk) => {
  chunks.push(Buffer.from(chunk));
  const buffered = Buffer.concat(chunks);
  const end = buffered.indexOf(sentinel);
  if (end >= 0) finish(buffered.subarray(0, end));
});
process.stdout.write(start + '\r\nINPUT_READY_' + label + '\r\n');
`;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return {
    options: parseCommonArgs(args),
    help: args.includes('--help') || args.includes('-h'),
  };
}

function shellQuote(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

function exactLineCount(text, expected) {
  return text.split('\n').filter((line) => line.trim() === expected).length;
}

async function readPane(client, sessionId, paneId) {
  return (await client.request('read_pane_text', { sessionId, paneId })).text || '';
}

async function waitForExactLine(client, sessionId, paneId, expected, minimum = 1) {
  return waitForPaneText(
    client,
    sessionId,
    paneId,
    (text) => exactLineCount(text, expected) >= minimum,
    `terminal line ${JSON.stringify(expected)} to appear ${minimum} time(s)`,
    15_000,
  );
}

async function waitForGrid(client, predicate, description, timeoutMs = 10_000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await client.request('grid_get_state').catch(() => null);
    if (predicate(last)) return last;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}. Last grid state: ${JSON.stringify(last)}`);
}

async function waitForGridText(client, runtimeId, predicate, description, timeoutMs = 15_000) {
  const deadline = Date.now() + timeoutMs;
  let last = '';
  while (Date.now() < deadline) {
    const response = await client.request('grid_get_tile_text', { runtimeId }).catch(() => null);
    last = response?.text || '';
    if (predicate(last)) return last;
    await delay(150);
  }
  throw new Error(`Timed out waiting for ${description}. Last grid text:\n${last}`);
}

async function waitForZoom(client, sessionId, expectedPaneId, description, timeoutMs = 8_000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    const state = await client.request('get_session_ui_state', { sessionId });
    last = state?.workspace?.view?.zoomedPaneId ?? null;
    if (last === expectedPaneId) return;
    await delay(120);
  }
  throw new Error(`Timed out waiting for ${description}. Last zoomed pane: ${JSON.stringify(last)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-input.mjs');
    return;
  }

  process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';

  const runner = createScenarioRunner(options, {
    scenarioId: 'TERMINAL-INPUT',
    tier: 'tier1-local-shell',
    prefix: 'terminal-input',
    metadata: {
      agent: 'shell',
      focus: 'background browser keyboard, clipboard, shortcut, IME, Kitty, and zoomed-grid input through libghostty',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath, backgroundLaunch: true });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const captureScript = path.join(runner.sessionDir, 'capture-terminal-input.cjs');
  const terminalSelector = '.terminal-wrapper.active .terminal-container';
  let sessionId = null;
  let pane = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session_panes', async () => {
    if (!sessionId) return;
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    for (const current of workspace?.panes || []) {
      await client.request('close_pane', { sessionId, paneId: current.paneId }).catch(() => {});
    }
  });
  runner.registerCleanup('restore_keybindings', () => (
    client.request('set_setting', { key: 'keybindings_config', value: '' }).catch(() => {})
  ));
  const focusPane = async () => {
    await client.request('focus_pane', { sessionId, paneId: pane.paneId });
  };

  const pressKey = async (spec, modifiers = {}, selector = terminalSelector, repeat = false) => {
    await client.request('dom_terminal_key', {
      selector,
      ...spec,
      modifiers,
      repeat,
    });
  };

  const waitForBinding = async (shortcutId, predicate, description, timeoutMs = 20_000) => {
    const deadline = Date.now() + timeoutMs;
    let last = null;
    while (Date.now() < deadline) {
      last = (await client.request('shortcut_binding', { shortcutId })).binding;
      if (predicate(last)) return last;
      await delay(100);
    }
    throw new Error(`Timed out waiting for ${description}. Last binding: ${JSON.stringify(last)}`);
  };

  const pressShortcut = async (shortcutId, selector = terminalSelector) => {
    const { binding } = await client.request('shortcut_binding', { shortcutId });
    runner.assert(Boolean(binding), `shortcut ${shortcutId} is unbound in the app`);
    const pressCombo = (combo) => {
      const key = combo.key;
      const code = combo.code
        || (/^[a-z]$/i.test(key) ? `Key${key.toUpperCase()}` : /^[0-9]$/.test(key) ? `Digit${key}` : key);
      return pressKey(
        { key, code },
        {
          meta: process.platform === 'darwin' && !!combo.meta,
          ctrl: !!combo.ctrl || (process.platform === 'linux' && !!combo.meta),
          shift: !!combo.shift,
          alt: !!combo.alt,
        },
        selector,
      );
    };
    if (binding.leader) await pressCombo(binding.leader);
    await pressCombo(binding.then || binding);
  };

  const pasteText = async (text, selector = terminalSelector) => {
    await client.request('dom_terminal_paste', { selector, text });
  };

  const beginCapture = async (label, mode = 'normal') => {
    await client.request('write_pane', {
      sessionId,
      paneId: pane.paneId,
      text: `${shellQuote(process.execPath)} ${shellQuote(captureScript)} ${shellQuote(label)} ${shellQuote(mode)}`,
    });
    await waitForPaneText(
      client,
      sessionId,
      pane.paneId,
      (text) => text.includes(`INPUT_READY_${label}`),
      `${label} input capture to become ready`,
      10_000,
    );
    await focusPane();
  };

  const finishCapture = async (label, expectedHex) => {
    await client.request('write_pane', {
      sessionId,
      paneId: pane.paneId,
      text: AFTER_KEYS_SENTINEL,
      submit: false,
    });
    const beginMarker = `INPUT_BEGIN_${label}`;
    const chunkMarker = `INPUT_CHUNK_${label}=`;
    const endMarker = `INPUT_END_${label}`;
    await waitForPaneText(
      client,
      sessionId,
      pane.paneId,
      (text) => text.split('\n').some((line) => line.trim() === endMarker),
      `${label} input capture to finish`,
      10_000,
    );
    const text = await readPane(client, sessionId, pane.paneId);
    const lines = text.split('\n').map((entry) => entry.trim());
    const end = lines.findLastIndex((entry) => entry === endMarker);
    const begin = lines.findLastIndex((entry, index) => index < end && entry === beginMarker);
    const actual = begin >= 0 && end > begin
      ? lines.slice(begin + 1, end)
        .filter((entry) => entry.startsWith(chunkMarker))
        .map((entry) => entry.slice(chunkMarker.length))
        .join('')
      : null;
    runner.assert(
      actual === expectedHex,
      `${label} bytes differ. Expected ${expectedHex || '<empty>'}, got ${actual || '<empty>'}. Pane:\n${text}`,
    );
    return actual;
  };

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('create_shell', async () => {
      fs.writeFileSync(captureScript, CAPTURE_PROGRAM);
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `terminal-input-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      await client.request('select_session', { sessionId });
      const workspace = await client.request('get_workspace', { sessionId });
      pane = workspace?.panes?.[0] ?? null;
      runner.assert(Boolean(pane?.paneId && pane?.runtimeId), `No live shell pane: ${JSON.stringify(workspace)}`);
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
      await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, pane.paneId, {
        timeoutMs: 20_000,
        description: 'terminal input shell prompt',
      });
      await focusPane();
    });

    await runner.step('history_and_line_editing', async () => {
      await pasteText('echo HISTORY_ONE');
      await pressKey(KEY.ENTER);
      await waitForExactLine(client, sessionId, pane.paneId, 'HISTORY_ONE');
      await pasteText('echo HISTORY_TWO');
      await pressKey(KEY.ENTER);
      await waitForExactLine(client, sessionId, pane.paneId, 'HISTORY_TWO');
      const before = exactLineCount(await readPane(client, sessionId, pane.paneId), 'HISTORY_TWO');
      await pressKey(KEY.UP);
      await pressKey(KEY.UP);
      await pressKey(KEY.DOWN);
      await pressKey(KEY.ENTER);
      await waitForExactLine(client, sessionId, pane.paneId, 'HISTORY_TWO', before + 1);

      await pasteText('echo RIGHXT');
      await pressKey(KEY.LEFT);
      await pressKey(KEY.LEFT);
      await pressKey(KEY.RIGHT);
      await pressKey(KEY.BACKSPACE);
      await pressKey(KEY.ENTER);
      await waitForExactLine(client, sessionId, pane.paneId, 'RIGHT');
    });

    await runner.step('navigation_function_and_modifier_bytes', async () => {
      await beginCapture('navigation');
      await pressKey(KEY.HOME);
      await pressKey(KEY.END);
      await pressKey(KEY.DELETE);
      await pressKey(KEY.PAGE_UP);
      await pressKey(KEY.PAGE_DOWN);
      await pressKey(KEY.F5);
      await pressKey({ key: 'a', code: 'KeyA' }, { ctrl: true });
      await finishCapture('navigation', NAVIGATION_HEX);
    });

    await runner.step('application_cursor_bytes', async () => {
      await beginCapture('application-cursor', 'cursor');
      await pressKey(KEY.UP);
      await pressKey(KEY.DOWN);
      await finishCapture('application-cursor', APPLICATION_CURSOR_HEX);
    });

    await runner.step('kitty_press_repeat_release_bytes', async () => {
      await beginCapture('kitty-repeat', 'kitty');
      await pressKey({ key: 'a', code: 'KeyA' }, {}, terminalSelector, true);
      await finishCapture('kitty-repeat', KITTY_REPEAT_HEX);
    });

    await runner.step('unicode_composition', async () => {
      const text = 'å🙂';
      await beginCapture('composition');
      await client.request('dom_compose_text', {
        selector: '.terminal-wrapper.active .terminal-container',
        text,
      });
      await finishCapture('composition', Buffer.from(text).toString('hex'));
    });

    await runner.step('bracketed_unicode_text_paste', async () => {
      const text = 'one\nå🙂';
      const normalized = 'one\rå🙂';
      const expected = Buffer.from(`\x1b[200~${normalized}\x1b[201~`).toString('hex');
      await beginCapture('bracketed-paste', 'bracketed');
      await pasteText(text);
      await finishCapture('bracketed-paste', expected);
    });

    await runner.step('image_paste_emits_ctrl_v', async () => {
      await beginCapture('image-paste');
      await client.request('dom_terminal_paste', { selector: terminalSelector, image: true });
      await finishCapture('image-paste', '16');
    });

    await runner.step('shortcuts_and_chords_never_reach_pty', async () => {
      await beginCapture('shortcut');
      await pressShortcut('terminal.toggleZoom');
      await waitForZoom(client, sessionId, pane.paneId, 'default zoom shortcut');
      await finishCapture('shortcut', '');

      const chordConfig = JSON.stringify({
        version: 1,
        overrides: {
          'terminal.toggleZoom': {
            leader: { key: 'y', meta: true },
            then: { key: 'z' },
          },
        },
      });
      await client.request('set_setting', { key: 'keybindings_config', value: chordConfig });
      await waitForBinding(
        'terminal.toggleZoom',
        (binding) => binding?.leader?.key === 'y',
        'the custom leader chord to reach the shortcut registry',
      );
      await beginCapture('chord');
      await pressShortcut('terminal.toggleZoom');
      await waitForZoom(client, sessionId, null, 'custom zoom chord');
      await finishCapture('chord', '');
      await client.request('set_setting', { key: 'keybindings_config', value: '' });
    });

    await runner.step('zoomed_grid_input', async () => {
      await focusPane();
      await pressShortcut('view.toggleGrid');
      await waitForGrid(client, (state) => state?.active === true, 'grid to open');
      await client.request('grid_zoom', { runtimeId: pane.runtimeId });
      await waitForGrid(client, (state) => state?.zoomedId === pane.runtimeId, 'shell tile to zoom');
      await client.request('dom_focus', { selector: '.grid-view-stage' });

      const token = `GRID_INPUT_${runner.runId}`;
      await pasteText(`echo ${token}`, '.grid-view-stage');
      await pressKey(KEY.ENTER, {}, '.grid-view-stage');
      let text = await waitForGridText(
        client,
        pane.runtimeId,
        (value) => exactLineCount(value, token) >= 1,
        'text input to reach the zoomed grid tile',
      );
      const before = exactLineCount(text, token);
      await pressKey(KEY.UP, {}, '.grid-view-stage');
      await pressKey(KEY.ENTER, {}, '.grid-view-stage');
      text = await waitForGridText(
        client,
        pane.runtimeId,
        (value) => exactLineCount(value, token) >= before + 1,
        'Up history input to reach the zoomed grid tile',
      );
      await pressShortcut('view.toggleGrid', '.grid-view-stage');
      await waitForGrid(client, (state) => state?.active === false, 'grid to close');
      runner.assert(exactLineCount(text, token) >= before + 1, 'grid history command did not rerun');
    });

    const result = await runner.finishSuccess({
      sessionId,
      paneId: pane.paneId,
      runtimeId: pane.runtimeId,
      covered: [
        'history-up-down',
        'left-right-backspace',
        'navigation-function-modifier',
        'application-cursor',
        'kitty-press-repeat-release',
        'unicode-composition',
        'bracketed-unicode-paste',
        'image-paste',
        'shortcut-chord-consumption',
        'zoomed-grid-input',
      ],
    });
    console.log('[verify] PASS — first-party terminal input matched packaged PTY bytes and state.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'terminal-input-failure', sessionId).catch(() => {});
    }
    const result = await runner.finishFailure(error, { sessionId, paneId: pane?.paneId ?? null });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    await client.request('set_setting', { key: 'keybindings_config', value: '' }).catch(() => {});
    if (sessionId) {
      const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
      for (const current of workspace?.panes || []) {
        await client.request('close_pane', { sessionId, paneId: current.paneId }).catch(() => {});
      }
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
