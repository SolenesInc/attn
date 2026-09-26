import { act, fireEvent, screen, within } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { downloadDir, homeDir } from '@tauri-apps/api/path';
import { save } from '@tauri-apps/plugin-dialog';
import { exists, readTextFile, writeTextFile } from '@tauri-apps/plugin-fs';
import { revealItemInDir } from '@tauri-apps/plugin-opener';
import { beforeEach, describe, expect, it, onTestFinished, vi } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import { agentPane, daemonSession, daemonWorkspace, type DaemonSession } from './test/daemonFixtures';
import { pressShortcut } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const FIRST_OUTPUT = 'first pane says hi';
const SECOND_OUTPUT = 'second pane output';

function splitWorkspace(id: string, sessionIds: string[]) {
  return daemonWorkspace(id, {
    root: sessionIds.length === 1
      ? { type: 'pane', pane_id: `pane-${sessionIds[0]}` }
      : { type: 'split', split_id: `split-${id}`, direction: 'vertical', ratio: 0.5, children: sessionIds.map((sid) => ({ type: 'pane', pane_id: `pane-${sid}` })) },
    panes: sessionIds.map((sid) => agentPane(sid, id)),
  }, { title: id });
}

function answerSnapshots(daemon: ScriptedDaemon) {
  daemon.on('support_snapshot', ({ endpoint_id }) => ({
    event: 'support_snapshot_result',
    ...(endpoint_id ? { endpoint_id } : {}),
    protocol_version: '1',
    daemon_instance_id: `daemon-${endpoint_id ?? 'home'}`,
    daemon_started_at_unix_ms: 1,
    captured_at_unix_ms: 2,
    backend: 'embedded',
    warning_codes: [],
    trace_capacity: 512,
    trace_total: 0,
    input_traces: [],
    runtimes: [],
  }));
}

async function openTerminals({
  sessions = [daemonSession('s1', { workspace_id: 'ws', state: 'idle' }), daemonSession('s2', { workspace_id: 'ws', state: 'idle' })] as DaemonSession[],
  workspaces = [splitWorkspace('ws', ['s1', 's2'])],
} = {}) {
  const endpoints = [...new Set(sessions.flatMap((session) => session.endpoint_id ? [session.endpoint_id] : []))]
    .map((id) => ({ id, name: id, ssh_target: `user@${id}`, status: 'connected', enabled: true }));
  return openAttachedTerminals({
    sessions,
    workspaces,
    initialState: endpoints.length ? { endpoints } : {},
    output: { s1: FIRST_OUTPUT, s2: SECOND_OUTPUT },
    script: answerSnapshots,
  });
}

const DIAGNOSTICS_LOG = 'debug/terminal-diagnostics.jsonl';

function diagnosticsLogOnDisk(persisted: string) {
  const files = new Map([[DIAGNOSTICS_LOG, persisted]]);
  let finishWrites = () => {};
  const writesHeld = new Promise<void>((resolve) => {
    finishWrites = resolve;
  });
  vi.mocked(isTauri).mockReturnValue(true);
  vi.mocked(invoke).mockRejectedValue(new Error('not in the desktop app'));
  vi.mocked(exists).mockImplementation(async (path) => files.has(String(path)));
  vi.mocked(readTextFile).mockImplementation(async (path) => files.get(String(path)) ?? '');
  vi.mocked(writeTextFile).mockImplementation(async (path, contents, options) => {
    if (path === DIAGNOSTICS_LOG) await writesHeld;
    files.set(String(path), `${options?.append ? files.get(String(path)) ?? '' : ''}${String(contents)}`);
  });
  onTestFinished(() => {
    for (const mock of [exists, readTextFile, writeTextFile]) vi.mocked(mock).mockReset();
  });
  return { finishWrites };
}

async function settleImports(daemon: ScriptedDaemon) {
  await act(() => vi.dynamicImportSettled());
  await daemon.idle();
}

async function createReport(daemon: ScriptedDaemon) {
  pressShortcut('ui.actionMenu');
  await daemon.idle();
  fireEvent.click(screen.getByText('Create diagnostic report'));
  await settleImports(daemon);
}

async function settleSave(daemon: ScriptedDaemon) {
  await settleImports(daemon);
  await act(() => vi.advanceTimersByTimeAsync(0));
  await daemon.idle();
}

async function saveReport(daemon: ScriptedDaemon) {
  fireEvent.click(prompt().getByRole('button', { name: 'Save report' }));
  await settleSave(daemon);
}

const prompt = () => within(screen.getByRole('dialog', { name: 'Create diagnostic report' }));
const paneChoice = (title: string) => prompt().getByRole('checkbox', { name: new RegExp(`^${title}`) });
const reportWrites = () => vi.mocked(writeTextFile).mock.calls.filter(([, contents]) => String(contents).includes('"schema": "attn.support-report'));
const savedAt = () => String(reportWrites().slice(-1)[0][0]);
const savedText = () => String(reportWrites().slice(-1)[0][1]);
const savedReport = () => JSON.parse(savedText()) as Record<string, unknown> & {
  diagnostics: { input: { frontend: { events: Array<{ traceId: string; stage: string }> }; historical: { total: number; events: Array<{ pane: string }> } } };
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe('App diagnostic report', () => {
  it('preselects the pane the user was in, and saves the output of exactly the panes they tick', async () => {
    const { daemon } = await openTerminals();
    await createReport(daemon);

    expect(paneChoice('s1')).toBeChecked();
    expect(paneChoice('s2')).not.toBeChecked();
    fireEvent.click(paneChoice('s2'));
    await saveReport(daemon);

    const panes = savedReport().paneContent as Array<{ paneId: string; text: string }>;
    expect(panes.map((pane) => [pane.paneId, pane.text.trim()])).toEqual([
      ['pane-s1', FIRST_OUTPUT],
      ['pane-s2', SECOND_OUTPUT],
    ]);
  });

  it('saves only metadata once the user clears the output, and says it saved', async () => {
    const { daemon } = await openTerminals();
    await createReport(daemon);

    fireEvent.click(prompt().getByRole('button', { name: 'Clear' }));
    await saveReport(daemon);

    expect(savedReport().paneContent).toEqual([]);
    expect(savedText()).not.toContain(FIRST_OUTPUT);
    expect(savedText()).not.toContain(SECOND_OUTPUT);
    expect(screen.getByText('Diagnostic report saved')).toBeInTheDocument();
  });

  it('marks a ticked pane that left the screen before saving as unavailable', async () => {
    const { daemon } = await openTerminals();
    await createReport(daemon);
    fireEvent.click(paneChoice('s2'));

    daemon.emit({ event: 'workspace_layout_updated', workspace_layout: splitWorkspace('ws', ['s1']).layout! });
    await daemon.idle();
    await saveReport(daemon);

    const report = savedReport();
    expect(report.paneContent).toEqual([
      expect.objectContaining({ paneId: 'pane-s1', available: true }),
      { paneId: 'pane-s2', runtimeId: 's2', available: false },
    ]);
    expect(report.omissions).toEqual(expect.arrayContaining([{ section: 'paneContent:pane-s2', reason: 'pane_not_mounted' }]));
  });

  it('asks the home daemon and each remote endpoint once for a snapshot of their own runtimes', async () => {
    const { daemon } = await openTerminals({
      sessions: [
        daemonSession('s1', { workspace_id: 'ws', state: 'idle' }),
        daemonSession('r1', { workspace_id: 'wr', state: 'idle', endpoint_id: 'ep-1' }),
        daemonSession('r2', { workspace_id: 'wr2', state: 'idle', endpoint_id: 'ep-1' }),
        daemonSession('r3', { workspace_id: 'wr3', state: 'idle', endpoint_id: 'ep-2' }),
      ],
      workspaces: [splitWorkspace('ws', ['s1']), { ...splitWorkspace('wr', ['r1']), endpoint_id: 'ep-1' }],
    });

    await createReport(daemon);

    expect(daemon.sentOf('support_snapshot').map(({ endpoint_id, runtime_ids }) => ({ endpoint_id, runtime_ids }))).toEqual([
      { endpoint_id: undefined, runtime_ids: ['s1'] },
      { endpoint_id: 'ep-1', runtime_ids: ['r1'] },
      { endpoint_id: 'ep-2', runtime_ids: [] },
    ]);
  });

  it('saves a partial report naming what it could not collect when the daemon never answers', async () => {
    const { daemon } = await openTerminals();
    daemon.on('support_snapshot', () => undefined);
    await createReport(daemon);

    fireEvent.click(prompt().getByRole('button', { name: 'Save report' }));
    await settleImports(daemon);
    await act(() => vi.advanceTimersByTimeAsync(3000));
    await settleImports(daemon);

    const report = savedReport();
    expect(report.daemons).toEqual([]);
    expect(report.omissions).toEqual(expect.arrayContaining([
      { section: 'daemons', reason: 'snapshots_unavailable' },
      { section: 'daemons:local', reason: 'snapshot_unavailable' },
      { section: 'nativeInput', reason: 'unsupported_or_unavailable' },
      { section: 'historicalInput', reason: 'no_persisted_samples' },
    ]));
  });
});

describe('App diagnostic report file', () => {
  it('saves under the next free name in Downloads and reveals it', async () => {
    const { daemon } = await openTerminals();
    await createReport(daemon);
    vi.mocked(writeTextFile).mockRejectedValueOnce(new Error('already exists'));
    vi.mocked(exists).mockResolvedValueOnce(true);

    await saveReport(daemon);

    expect(savedAt()).toMatch(/^\/Users\/me\/Downloads\/attn-\d{8}-\d{6}Z\.attn-report-2\.json$/);
    expect(vi.mocked(writeTextFile)).toHaveBeenLastCalledWith(savedAt(), expect.any(String), { createNew: true });
    expect(revealItemInDir).toHaveBeenCalledWith(savedAt());
  });

  it('asks where to save when Downloads cannot be written', async () => {
    const { daemon } = await openTerminals();
    await createReport(daemon);
    vi.mocked(writeTextFile).mockRejectedValueOnce(new Error('downloads unavailable'));
    vi.mocked(save).mockResolvedValueOnce('/chosen/report.json');

    await saveReport(daemon);

    expect(save).toHaveBeenCalledOnce();
    expect(savedAt()).toBe('/chosen/report.json');
    expect(revealItemInDir).toHaveBeenCalledWith('/chosen/report.json');
  });

  it('uses the Downloads folder in the home folder when the platform names none', async () => {
    const { daemon } = await openTerminals();
    await createReport(daemon);
    vi.mocked(downloadDir).mockRejectedValueOnce(new Error('download directory is unavailable'));
    vi.mocked(homeDir).mockResolvedValueOnce('/home/me');

    await saveReport(daemon);

    expect(savedAt()).toMatch(/^\/home\/me\/Downloads\/attn-\d{8}-\d{6}Z\.attn-report\.json$/);
    expect(save).not.toHaveBeenCalled();
  });

  it('says the report is saved without waiting for the desktop to reveal it', async () => {
    const { daemon } = await openTerminals();
    await createReport(daemon);
    vi.mocked(revealItemInDir).mockReturnValueOnce(new Promise(() => {}));

    await saveReport(daemon);

    expect(screen.getByText('Diagnostic report saved')).toBeInTheDocument();
  });
});

describe('App diagnostic report privacy', () => {
  it('traces a key press from the document to the socket under one id, without the key', async () => {
    const { daemon } = await openTerminals();
    fireEvent.keyDown(within(document.querySelector<HTMLElement>('[data-pane-id="pane-s1"]')!).getByRole('textbox', { name: 'Terminal input' }), { key: 'ẞ', code: 'IntlRo' });
    await daemon.idle();

    await createReport(daemon);
    fireEvent.click(prompt().getByRole('button', { name: 'Clear' }));
    await saveReport(daemon);

    const events = savedReport().diagnostics.input.frontend.events;
    const { traceId } = events.filter((event) => event.stage === 'document').slice(-1)[0];
    expect(events.filter((event) => event.traceId === traceId).map((event) => event.stage)).toEqual(['document', 'terminal', 'transport']);
    expect(savedText()).not.toContain('ẞ');
    expect(savedText()).not.toContain('IntlRo');
  });

  it('keeps what the user typed into the find field, composed, or pasted out of the report', async () => {
    const { daemon } = await openTerminals();
    const terminalInput = within(document.querySelector<HTMLElement>('[data-pane-id="pane-s1"]')!).getByRole('textbox', { name: 'Terminal input' });
    terminalInput.focus();
    pressShortcut('terminal.find', terminalInput);
    await daemon.idle();
    const find = screen.getByTestId('ghostty-find-input');
    fireEvent.keyDown(find, { key: 'S', code: 'KeyS' });
    fireEvent.change(find, { target: { value: 'SECRET_FIND' } });
    terminalInput.dispatchEvent(new CompositionEvent('compositionstart', { data: 'SECRET_PREEDIT', bubbles: true }));
    terminalInput.dispatchEvent(new CompositionEvent('compositionend', { data: 'SECRET_PREEDIT', bubbles: true }));
    const paste = new Event('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(paste, 'clipboardData', {
      value: { types: ['text/plain'], items: [], files: [], getData: (type: string) => (type === 'text/plain' ? 'SECRET_CLIPBOARD' : '') },
    });
    terminalInput.dispatchEvent(paste);
    await act(() => vi.advanceTimersByTimeAsync(2000));
    await daemon.idle();

    await createReport(daemon);
    fireEvent.click(prompt().getByRole('button', { name: 'Clear' }));
    await saveReport(daemon);

    expect(savedText()).not.toMatch(/SECRET_(FIND|PREEDIT|CLIPBOARD)/);
  });

  it('carries the input records persisted on disk and those still being written, and nothing else from that log', async () => {
    const { daemon } = await openTerminals();
    const log = diagnosticsLogOnDisk([
      '{"kind":"input","pane":"before-restart"}',
      '{"kind":"paint","text":"PRIVATE_OUTPUT","context":[{"kind":"input"}]}',
      '{"kind":"input",',
      'null',
      '',
    ].join('\n'));
    await act(() => vi.advanceTimersByTimeAsync(30_000));
    fireEvent.keyDown(within(document.querySelector<HTMLElement>('[data-pane-id="pane-s1"]')!).getByRole('textbox', { name: 'Terminal input' }), { key: 'a', code: 'KeyA' });
    await createReport(daemon);
    fireEvent.click(prompt().getByRole('button', { name: 'Clear' }));
    fireEvent.click(prompt().getByRole('button', { name: 'Save report' }));
    await settleImports(daemon);

    log.finishWrites();
    await settleSave(daemon);

    const panes = savedReport().diagnostics.input.historical.events.map((event) => event.pane);
    expect(panes[0]).toBe('before-restart');
    expect(panes).toContain('pane-s1');
    expect(savedText()).not.toContain('PRIVATE_OUTPUT');
  });

  it('saves an empty input history when there is no log on disk', async () => {
    const { daemon } = await openTerminals();
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockRejectedValue(new Error('not in the desktop app'));
    vi.mocked(exists).mockResolvedValue(false);
    await createReport(daemon);

    await saveReport(daemon);

    expect(savedReport().diagnostics.input.historical).toMatchObject({ total: 0, events: [] });
    expect(savedReport().omissions).toEqual(expect.arrayContaining([{ section: 'historicalInput', reason: 'no_persisted_samples' }]));
  });
});
