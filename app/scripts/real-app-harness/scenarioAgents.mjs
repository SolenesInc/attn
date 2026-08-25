import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  waitForFirstWorkspacePane,
  waitForPaneInputFocus,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';

function compact(text) {
  return text.replace(/\s+/g, '');
}

// Writes into the real ~/.claude.json. Safe only because the harness session dir
// is fresh per run, so the entry cannot shadow a user-curated trust decision.
export function preTrustClaudeFolder(folderPath) {
  // Claude keys projects by the realpath of cwd: an unresolved /var/folders path
  // sits next to Claude's canonical entry and is never read.
  const resolvedFolder = path.resolve(folderPath);
  const absoluteFolder = (() => {
    try {
      return fs.realpathSync(resolvedFolder);
    } catch {
      return resolvedFolder;
    }
  })();
  const configPath = path.join(os.homedir(), '.claude.json');

  let config = {};
  // Carry the existing mode forward: this config holds account metadata, and the
  // umask would widen a 0600 file to 0644.
  let fileMode = 0o600;
  try {
    const raw = fs.readFileSync(configPath, 'utf8');
    if (raw.trim()) {
      config = JSON.parse(raw);
    }
    fileMode = fs.statSync(configPath).mode & 0o777;
  } catch (error) {
    if (error.code !== 'ENOENT') {
      throw error;
    }
  }

  if (!config.projects || typeof config.projects !== 'object') {
    config.projects = {};
  }
  const existing = config.projects[absoluteFolder] && typeof config.projects[absoluteFolder] === 'object'
    ? config.projects[absoluteFolder]
    : {};
  config.projects[absoluteFolder] = {
    ...existing,
    hasTrustDialogAccepted: true,
    hasCompletedProjectOnboarding: true,
  };

  // Write atomically: writing through a temp file keeps the 90+KB user
  // config from being truncated if the harness is killed mid-write.
  const tmpPath = `${configPath}.harness-${process.pid}-${Date.now()}`;
  fs.writeFileSync(tmpPath, JSON.stringify(config, null, 2), { encoding: 'utf8', mode: fileMode });
  fs.renameSync(tmpPath, configPath);
  return absoluteFolder;
}

function hasTrustPrompt(text) {
  return (
    text.includes('Do you trust this folder?')
    || text.includes('Do you trust the contents of this directory?')
    || text.includes('Working with untrusted contents')
    || text.includes('Security guide')
  );
}

function hasClaudePrompt(text) {
  if (hasTrustPrompt(text)) {
    return false;
  }
  return /(^|\n)\s*❯(?:\s|$)/u.test(text);
}

function hasCodexPrompt(text) {
  if (hasTrustPrompt(text)) {
    return false;
  }
  if (hasCodexUpdatePrompt(text)) {
    return false;
  }
  return (
    text.includes('OpenAI Codex')
    || text.includes('/model to change')
    || text.includes('100% left')
  );
}

// An undismissed "Update available!" chooser blocks every hasCodexPrompt signal;
// choice 3 skips until the next version.
function hasCodexUpdatePrompt(text) {
  return (
    text.includes('Update available!')
    && text.includes('Skip until next version')
  );
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function ensureClaudeInitialPanePromptReady(client, sessionId, timeoutMs = 40_000) {
  const startedAt = Date.now();
  let trustHandled = false;

  while (Date.now() - startedAt < timeoutMs) {
    await client.request('select_session', { sessionId });
    const initialPane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for Claude session ${sessionId}`, 20_000);
    await waitForPaneVisible(client, sessionId, initialPane.paneId, 20_000);
    const pane = await client.request('read_pane_text', { sessionId, paneId: initialPane.paneId }, { timeoutMs: 20_000 });
    const text = pane?.text || '';

    if (hasTrustPrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
      await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: initialPane.paneId, text: '1' });
      await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: '\r', submit: false });
      trustHandled = true;
      await delay(500);
      continue;
    }

    if (hasClaudePrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
      await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
      return { trustHandled, paneId: initialPane.paneId, text };
    }
  }

  throw new Error(`Timed out waiting for Claude prompt readiness in session ${sessionId}`);
}

export async function ensureCodexInitialPanePromptReady(client, sessionId, timeoutMs = 40_000) {
  const startedAt = Date.now();
  let trustHandled = false;
  let updatePromptHandled = false;

  while (Date.now() - startedAt < timeoutMs) {
    await client.request('select_session', { sessionId });
    const initialPane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for Codex session ${sessionId}`, 20_000);
    await waitForPaneVisible(client, sessionId, initialPane.paneId, 20_000);
    const pane = await client.request('read_pane_text', { sessionId, paneId: initialPane.paneId }, { timeoutMs: 20_000 });
    const text = pane?.text || '';

    if (hasTrustPrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
      await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: initialPane.paneId, text: '1' });
      await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: '\r', submit: false });
      trustHandled = true;
      await delay(500);
      continue;
    }

    if (hasCodexUpdatePrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
      await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: initialPane.paneId, text: '3' });
      await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: '\r', submit: false });
      updatePromptHandled = true;
      await delay(500);
      continue;
    }

    if (hasCodexPrompt(text)) {
      await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
      await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
      return { trustHandled, updatePromptHandled, paneId: initialPane.paneId, text };
    }

    await delay(300);
  }

  throw new Error(`Timed out waiting for Codex prompt readiness in session ${sessionId}`);
}

// The helpers above gate on DOM input focus, which WebKit delivers only to the
// macOS key window; a parked app never satisfies it. These use write_pane.

async function answerPaneMenuViaPty(client, sessionId, paneId, choice) {
  await client.request('write_pane', { sessionId, paneId, text: `${choice}\r`, submit: false });
}

async function ensureAgentPromptReadyViaPty(client, sessionId, { label, isReady, gates, timeoutMs }) {
  const startedAt = Date.now();
  const handled = [];

  while (Date.now() - startedAt < timeoutMs) {
    // select_session changes the shown workspace without stealing OS focus.
    await client.request('select_session', { sessionId });
    const initialPane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for ${label} session ${sessionId}`, 20_000);
    await waitForPaneVisible(client, sessionId, initialPane.paneId, 20_000);
    const pane = await client.request('read_pane_text', { sessionId, paneId: initialPane.paneId }, { timeoutMs: 20_000 });
    const text = pane?.text || '';

    if (isReady(text)) {
      return { paneId: initialPane.paneId, text, handled };
    }

    const gate = gates.find((entry) => entry.match(text));
    if (gate) {
      await answerPaneMenuViaPty(client, sessionId, initialPane.paneId, gate.choice);
      handled.push(gate.name);
      await delay(500);
      continue;
    }

    await delay(300);
  }

  throw new Error(`Timed out waiting for ${label} prompt readiness in session ${sessionId} (focus-free / PTY)`);
}

export async function ensureClaudePromptReadyViaPty(client, sessionId, timeoutMs = 60_000) {
  return ensureAgentPromptReadyViaPty(client, sessionId, {
    label: 'Claude',
    isReady: hasClaudePrompt,
    gates: [{ name: 'trust', match: hasTrustPrompt, choice: '1' }],
    timeoutMs,
  });
}

export async function ensureCodexPromptReadyViaPty(client, sessionId, timeoutMs = 60_000) {
  return ensureAgentPromptReadyViaPty(client, sessionId, {
    label: 'Codex',
    isReady: hasCodexPrompt,
    gates: [
      { name: 'trust', match: hasTrustPrompt, choice: '1' },
      { name: 'update', match: hasCodexUpdatePrompt, choice: '3' },
    ],
    timeoutMs,
  });
}

export async function promptClaudeForStructuredBlock(client, sessionId, token, lineCount = 8) {
  const lines = Array.from({ length: lineCount }, (_, index) =>
    `${token} line ${index + 1} render width coverage verification payload ${index + 1} for split stability`
  );
  const prompt = [
    'Reply with exactly the following lines and nothing else.',
    'Preserve uppercase letters, digits, and spacing exactly.',
    'Do not add a preamble.',
    'Do not use a code block.',
    ...lines,
  ].join('\n');

  const initialPane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for Claude prompt ${sessionId}`, 20_000);
  await client.request('click_pane', { sessionId, paneId: initialPane.paneId });
  await waitForPaneInputFocus(client, sessionId, initialPane.paneId, 15_000);
  // Claude Code treats a rapid multi-line write_pane as a paste, so a trailing \r
  // inserts a newline instead of submitting.
  await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: prompt, submit: false });
  await delay(500);
  await client.request('write_pane', { sessionId, paneId: initialPane.paneId, text: '\r', submit: false });

  // The input box echoes the prompt, up to lineCount occurrences of token before
  // submit, so only a higher count means the reply rendered.
  const replyTimeoutMs = 45_000;
  const startedAt = Date.now();
  let lastText = '';
  while (Date.now() - startedAt < replyTimeoutMs) {
    const pane = await client.request('read_pane_text', { sessionId, paneId: initialPane.paneId }, { timeoutMs: 20_000 });
    lastText = pane?.text || '';
    const occurrences = lastText.split(token).length - 1;
    if (occurrences >= lineCount + 1) {
      return { prompt, expectedLines: lines, paneId: initialPane.paneId };
    }
    await delay(1_000);
  }

  throw new Error(`Timed out waiting for Claude structured block reply for ${token} in session ${sessionId}`);
}
