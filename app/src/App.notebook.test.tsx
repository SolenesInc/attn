import { act, fireEvent, screen, within } from '@testing-library/react';
import { EditorView } from '@codemirror/view';
import { describe, expect, it, onTestFinished, vi } from 'vitest';
import { stubTextLayout } from './test/appFixtures';
import { agentWorkspace, daemonEndpoint, daemonSession, type DaemonSession, type DaemonWorkspace } from './test/daemonFixtures';
import { stubNavigatorPlatform } from './test/platformStub';
import { pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

function documentPane() {
  return document.querySelector<HTMLElement>('.notebook-browser-document')!;
}

const NOTEBOOK_ROOT = '/notebook';

const INDEX = '---\ntype: journal\n---\n# Index\n\nbuy milk\n\n## Why\n\nbecause.';

type Entry = { path: string; name: string; is_dir: boolean; size: number };
const dir = (path: string): Entry => ({ path, name: path.split('/').pop()!, is_dir: true, size: 0 });
const file = (path: string): Entry => ({ path, name: path.split('/').pop()!, is_dir: false, size: 12 });

const TREE: Record<string, Entry[]> = {
  '': [dir('knowledge'), dir('journal'), file('notes.txt'), file('cover.png'), file('prototype.docx')],
  knowledge: [file('knowledge/index.md'), dir('knowledge/areas')],
  'knowledge/areas': [file('knowledge/areas/foo.md')],
  journal: [file('journal/2026-06-13.md')],
};

interface Vault {
  files: Record<string, string>;
  tree: Record<string, Entry[]>;
}

function freshVault(): Vault {
  return {
    tree: structuredClone(TREE),
    files: {
      'knowledge/index.md': INDEX,
      'knowledge/areas/foo.md': '# Foo\n\nfoo body',
      'notes.txt': 'plain notes',
      'journal/2026-06-13.md': '# Friday\n\nsee [index](/knowledge/index.md)',
    },
  };
}

const hashOf = (content: string) => `h${content.length}`;

function serveVault(daemon: ScriptedDaemon, vault: Vault) {
  daemon.on('fs_list', ({ path }) => ({ event: 'fs_list_result', success: true, entries: vault.tree[path ?? ''] ?? [] }));
  daemon.on('fs_read', ({ path }) => (
    path in vault.files
      ? { event: 'fs_read_result', success: true, result: { path, content: vault.files[path], hash: hashOf(vault.files[path]) } }
      : { event: 'fs_read_result', success: false, error: `fsdoc: ${path} not found` }
  ));
  daemon.on('fs_write', ({ path, content }) => {
    vault.files[path] = content;
    return { event: 'fs_write_result', success: true, result: { path, conflict: false, hash: hashOf(content) } };
  });
  daemon.on('notebook_backlinks', ({ path }) => ({
    event: 'notebook_backlinks_result',
    success: true,
    entries: path === 'knowledge/index.md' ? [{ path: 'journal/2026-06-13.md', title: '2026-06-13', size: 2 }] : [],
  }));
  daemon.on('fs_index', ({ root }) => ({ event: 'fs_index_result', success: true, root: root ?? NOTEBOOK_ROOT, files: Object.keys(vault.files), truncated: false }));
}

async function openVault({ vault = freshVault(), sessions = [] as DaemonSession[] } = {}) {
  const view = await renderApp({
    initialState: {
      settings: { 'notebook.root.effective': NOTEBOOK_ROOT },
      sessions,
      workspaces: sessions.map((session) => agentWorkspace(session.id)),
    },
  });
  serveVault(view.daemon, vault);
  fireEvent.click(screen.getByRole('button', { name: /Open Notebook/ }));
  await view.daemon.idle();
  return { ...view, vault };
}

function noteEditor() {
  return EditorView.findFromDOM(documentPane().querySelector<HTMLElement>('.cm-content')!)!;
}

function typeInNote(text: string) {
  const editor = noteEditor();
  act(() => editor.dispatch({ changes: { from: editor.state.doc.length, insert: text } }));
}

const noteTitle = () => documentPane().querySelector('h2')?.textContent;
const notebook = () => within(screen.getByRole('dialog', { name: 'Notebook' }));
const treeItem = (name: string) => notebook().getByRole('treeitem', { name });
const fsReadsOf = (daemon: ScriptedDaemon, path: string) => daemon.sentOf('fs_read').filter((read) => read.path === path);
const fsListsOf = (daemon: ScriptedDaemon, path: string) => daemon.sentOf('fs_list').filter((list) => (list.path ?? '') === path);

async function click(daemon: ScriptedDaemon, element: HTMLElement) {
  fireEvent.click(element);
  await daemon.idle();
}

function hold<C extends Parameters<ScriptedDaemon['on']>[0]>(daemon: ScriptedDaemon, cmd: C) {
  daemon.on(cmd, () => undefined);
}

async function notebookChanged(daemon: ScriptedDaemon, root = NOTEBOOK_ROOT) {
  daemon.emit({ event: 'fs_changed', root, origin: 'disk', paths: [] });
  await daemon.idle();
}

async function selectInNote(daemon: ScriptedDaemon, from: number, to: number) {
  const editor = noteEditor();
  act(() => editor.dispatch({ selection: { anchor: from, head: to } }));
  await daemon.idle();
}

describe('App notebook reading', () => {
  it('opens the preferred note with its kind, outline and the notes that link to it in a context rail the user can fold', async () => {
    const { daemon } = await openVault();

    expect(noteTitle()).toBe('index');
    expect(documentPane()).toHaveTextContent('buy milk');
    expect(documentPane().querySelector('.notebook-browser-kind-badge')).toHaveTextContent('journal');
    expect(daemon.sentOf('notebook_backlinks')).toEqual([expect.objectContaining({ path: 'knowledge/index.md' })]);
    expect(notebook().getByRole('button', { name: '2026-06-13' })).toHaveAttribute('title', 'journal/2026-06-13.md');
    expect(notebook().getByRole('button', { name: 'Why' })).toBeInTheDocument();
    fireEvent.click(notebook().getByRole('button', { name: /^Outline/ }));
    expect(notebook().queryByRole('button', { name: 'Why' })).toBeNull();

    fireEvent.click(notebook().getByRole('button', { name: 'Hide context rail' }));
    const rail = () => documentPane().parentElement!.querySelector('[aria-label="Context"]');
    expect(rail()).toHaveAttribute('inert');
    fireEvent.click(notebook().getByRole('button', { name: 'Show context rail' }));
    expect(rail()).not.toHaveAttribute('inert');

    await click(daemon, treeItem('notes.txt'));
    expect(notebook().getByRole('textbox', { name: 'File contents' })).toHaveValue('plain notes');
    expect(notebook().queryByRole('button', { name: 'Hide context rail' })).toBeNull();
  });

  it('shows images and unknown attachments as placeholders without reading them, even after reopening', async () => {
    const { daemon } = await openVault();

    await click(daemon, treeItem('prototype.docx'));
    expect(documentPane()).toHaveTextContent("prototype.docx can't be opened here yet.");
    await click(daemon, treeItem('cover.png'));
    expect(documentPane()).toHaveTextContent('Preview not available');
    expect(documentPane()).toHaveTextContent("cover.png can't be opened here yet.");

    fireEvent.keyDown(window, { key: 'Escape' });
    await daemon.idle();
    fireEvent.click(screen.getByRole('button', { name: /Open Notebook/ }));
    await daemon.idle();

    expect(documentPane()).toHaveTextContent("cover.png can't be opened here yet.");
    expect([...fsReadsOf(daemon, 'cover.png'), ...fsReadsOf(daemon, 'prototype.docx')]).toEqual([]);
  });

  it('opens a backlinked note before its own backlinks arrive, dropping the previous note’s backlinks at once', async () => {
    const { daemon } = await openVault();
    hold(daemon, 'notebook_backlinks');

    await click(daemon, notebook().getByRole('button', { name: '2026-06-13' }));

    expect(noteTitle()).toBe('2026-06-13');
    expect(notebook().queryByRole('button', { name: '2026-06-13' })).toBeNull();
    expect(notebook().getByText('Finding backlinks…')).toBeInTheDocument();

    const pending = daemon.sentOf('notebook_backlinks').slice(-1)[0];
    expect(pending.path).toBe('journal/2026-06-13.md');
    daemon.replyTo(pending, { event: 'notebook_backlinks_result', request_id: pending.request_id, success: true, entries: [{ path: 'knowledge/index.md', title: 'Index', size: 2 }] });
    await daemon.idle();
    expect(notebook().getByRole('button', { name: 'Index' })).toBeInTheDocument();
  });

  it('shows the empty state when the notebook has nothing in it', async () => {
    await openVault({ vault: { files: {}, tree: {} } });

    expect(documentPane()).toHaveTextContent('Nothing selected');
    expect(notebook().getByText('This folder is empty.')).toBeInTheDocument();
  });

  it('takes focus when it opens', async () => {
    await openVault();

    expect(screen.getByRole('dialog', { name: 'Notebook' })).toHaveFocus();
  });

  it('shows whether the chief is working, and nothing without a chief', async () => {
    const chief = (state: DaemonSession['state']) => [daemonSession('chief', { chief_of_staff: true, state })];
    for (const [sessions, pulse] of [[chief('working'), 'chief: active'], [chief('idle'), 'chief: idle'], [[], null]] as const) {
      const view = await openVault({ sessions: [...sessions] });
      expect(notebook().queryByText(/chief:/)?.textContent ?? null).toBe(pulse);
      view.unmount();
    }
  });
});

describe('App notebook tree', () => {
  it('lists the root, then a folder once however often the user folds it, marks the open file, and says why a folder cannot be listed', async () => {
    const { daemon, vault } = await openVault();
    expect(daemon.sentOf('fs_list')[0]).not.toHaveProperty('path');
    expect(treeItem('knowledge')).toHaveAttribute('aria-expanded', 'false');

    await click(daemon, treeItem('knowledge'));
    expect(treeItem('knowledge')).toHaveAttribute('aria-expanded', 'true');
    expect(treeItem('areas')).toBeInTheDocument();
    await click(daemon, treeItem('knowledge'));
    await click(daemon, treeItem('knowledge'));
    expect(fsListsOf(daemon, 'knowledge')).toHaveLength(1);
    expect(treeItem('index.md')).toHaveAttribute('aria-current', 'true');

    const listsBefore = daemon.sentOf('fs_list').length;
    await click(daemon, treeItem('notes.txt'));
    expect(treeItem('notes.txt')).toHaveAttribute('aria-current', 'true');
    expect(daemon.sentOf('fs_list')).toHaveLength(listsBefore);

    delete vault.tree.journal;
    daemon.on('fs_list', ({ path }) => (
      path === 'journal'
        ? { event: 'fs_list_result', success: false, error: 'fsdoc: permission denied' }
        : { event: 'fs_list_result', success: true, entries: vault.tree[path ?? ''] ?? [] }
    ));
    await click(daemon, treeItem('journal'));
    expect(notebook().getByText('fsdoc: permission denied')).toBeInTheDocument();
    expect(treeItem('knowledge')).toBeInTheDocument();
  });
});

describe('App notebook on-disk changes', () => {
  it.each([
    { event: 'the notebook root', root: NOTEBOOK_ROOT },
    { event: 'an empty root', root: '' },
  ])('re-lists the tree and its open folders and reloads the open note when $event changes', async ({ root }) => {
    const { daemon, vault } = await openVault();
    await click(daemon, treeItem('knowledge'));
    const rootLists = fsListsOf(daemon, '').length;
    vault.files['knowledge/index.md'] = '# Index\n\nwritten by an agent';

    await notebookChanged(daemon, root);

    expect(fsListsOf(daemon, '')).toHaveLength(rootLists + 1);
    expect(fsListsOf(daemon, 'knowledge')).toHaveLength(2);
    expect(treeItem('areas')).toBeInTheDocument();
    expect(documentPane()).toHaveTextContent('written by an agent');
  });

  it('leaves the notebook alone for a change under another root', async () => {
    const { daemon } = await openVault();
    const sent = daemon.sent.length;

    await notebookChanged(daemon, '/elsewhere');

    expect(daemon.sent.slice(sent).filter((command) => command.cmd === 'fs_list' || command.cmd === 'fs_read')).toEqual([]);
  });

  it('says why the open note is unavailable once a change finds it deleted', async () => {
    const { daemon, vault } = await openVault();
    delete vault.files['knowledge/index.md'];

    await notebookChanged(daemon);

    expect(documentPane()).toHaveTextContent('File unavailable');
    expect(documentPane()).toHaveTextContent('fsdoc: knowledge/index.md not found');
    expect(noteTitle()).toBe('File unavailable');
  });

  it('keeps the open note when the tree cannot be listed on refresh', async () => {
    const { daemon } = await openVault();
    daemon.on('fs_list', () => ({ event: 'fs_list_result', success: false, error: 'socket closed' }));

    await notebookChanged(daemon);

    expect(noteTitle()).toBe('index');
    expect(documentPane()).not.toHaveTextContent('File unavailable');
  });

  it('keeps unsaved edits instead of reloading the note', async () => {
    const { daemon } = await openVault();
    typeInNote('\n\nmy draft');
    const reads = fsReadsOf(daemon, 'knowledge/index.md').length;

    await notebookChanged(daemon);

    expect(fsReadsOf(daemon, 'knowledge/index.md')).toHaveLength(reads);
    expect(noteEditor().state.doc.toString()).toBe(`${INDEX}\n\nmy draft`);
  });
});

describe('App notebook saving', () => {
  it('saves an unsaved edit before opening the next note', async () => {
    const { daemon } = await openVault();
    typeInNote(' now');

    await click(daemon, treeItem('notes.txt'));

    const write = daemon.sent.findIndex((command) => command.cmd === 'fs_write');
    const read = daemon.sent.findIndex((command) => command.cmd === 'fs_read' && command.path === 'notes.txt');
    expect(daemon.sentOf('fs_write')).toEqual([expect.objectContaining({ path: 'knowledge/index.md', content: `${INDEX} now`, base_hash: hashOf(INDEX) })]);
    expect(write).toBeLessThan(read);
    expect(notebook().getByRole('textbox', { name: 'File contents' })).toHaveValue('plain notes');
  });

  it('saves an edit against the version it read, and overwrites the changed file only when the user says so', async () => {
    const { daemon } = await openVault();
    daemon.on('fs_write', ({ path, base_hash, content }) => (
      base_hash === hashOf(INDEX)
        ? { event: 'fs_write_result', success: true, result: { path, conflict: true, current_hash: 'hX' } }
        : { event: 'fs_write_result', success: true, result: { path, conflict: false, hash: hashOf(content) } }
    ));
    typeInNote(' and eggs');
    await act(() => vi.advanceTimersByTimeAsync(700));
    await daemon.idle();
    expect(documentPane()).toHaveTextContent('This file changed on disk since you opened it.');

    await click(daemon, notebook().getByRole('button', { name: 'Overwrite anyway' }));

    expect(daemon.sentOf('fs_write').map(({ path, content, base_hash }) => ({ path, content, base_hash }))).toEqual([
      { path: 'knowledge/index.md', content: `${INDEX} and eggs`, base_hash: hashOf(INDEX) },
      { path: 'knowledge/index.md', content: `${INDEX} and eggs`, base_hash: 'hX' },
    ]);
    expect(documentPane()).toHaveTextContent('Saved');
  });

  it('stays on a note whose save conflicts on the way out, and says so', async () => {
    const { daemon } = await openVault();
    daemon.on('fs_write', ({ path }) => ({ event: 'fs_write_result', success: true, result: { path, conflict: true, current_hash: 'hX' } }));
    typeInNote(' mine');

    await click(daemon, treeItem('notes.txt'));

    expect(noteTitle()).toBe('index');
    expect(documentPane()).toHaveTextContent('This file changed on disk since you opened it.');
    expect(noteEditor().state.doc.toString()).toBe(`${INDEX} mine`);
    expect(fsReadsOf(daemon, 'notes.txt')).toEqual([]);
  });

  it('never stamps a late reload onto the note the user moved to', async () => {
    const { daemon } = await openVault();
    daemon.on('fs_write', ({ path }) => ({ event: 'fs_write_result', success: true, result: { path, conflict: true, current_hash: 'hX' } }));
    typeInNote(' mine');
    await act(() => vi.advanceTimersByTimeAsync(700));
    await daemon.idle();
    hold(daemon, 'fs_read');
    fireEvent.click(notebook().getByRole('button', { name: 'Reload from disk' }));
    const [reload] = fsReadsOf(daemon, 'knowledge/index.md').slice(-1);
    serveVault(daemon, freshVault());

    await click(daemon, treeItem('notes.txt'));
    daemon.replyTo(reload, { event: 'fs_read_result', request_id: reload.request_id, success: true, result: { path: 'knowledge/index.md', content: '# reloaded index', hash: 'hX' } });
    await daemon.idle();

    expect(notebook().getByRole('textbox', { name: 'File contents' })).toHaveValue('plain notes');
  });

  it('never stamps a late autosave onto the note the user moved to', async () => {
    const { daemon } = await openVault();
    hold(daemon, 'fs_write');
    typeInNote(' A edited');
    await act(() => vi.advanceTimersByTimeAsync(700));
    await daemon.idle();
    const [autosave] = daemon.sentOf('fs_write');
    serveVault(daemon, freshVault());

    await click(daemon, treeItem('notes.txt'));
    daemon.replyTo(autosave, { event: 'fs_write_result', request_id: autosave.request_id, success: true, result: { path: autosave.path, conflict: false, hash: 'h2' } });
    await daemon.idle();

    expect(notebook().getByRole('textbox', { name: 'File contents' })).toHaveValue('plain notes');
    expect(documentPane()).not.toHaveTextContent('Saved');
  });

  it('shows Saved after an autosave, then lets it fade', async () => {
    const { daemon } = await openVault();
    typeInNote(' saved');

    await act(() => vi.advanceTimersByTimeAsync(700));
    await daemon.idle();
    expect(documentPane()).toHaveTextContent('Saved');

    await act(() => vi.advanceTimersByTimeAsync(2500));
    expect(documentPane()).not.toHaveTextContent('Saved');
  });

  it('bolds the word under the cursor with Ctrl+B on Linux', async () => {
    onTestFinished(stubNavigatorPlatform('Linux x86_64'));
    const vault = freshVault();
    vault.files['knowledge/index.md'] = 'buy milk';
    const { daemon } = await openVault({ vault });
    const editor = noteEditor();
    act(() => editor.dispatch({ selection: { anchor: 5 } }));

    fireEvent.keyDown(documentPane().querySelector('.cm-content')!, { key: 'b', code: 'KeyB', ctrlKey: true });
    await act(() => vi.advanceTimersByTimeAsync(700));
    await daemon.idle();

    expect(daemon.sentOf('fs_write').map((write) => write.content)).toEqual(['buy **milk**']);
  });
});

describe('App notebook send to chief', () => {
  it('sends the selected text with its note to the chief and says it landed', async () => {
    stubTextLayout();
    const { daemon } = await openVault();
    daemon.on('notebook_send_to_chief', () => ({ event: 'notebook_send_to_chief_result', success: true, result: { path: 'inbox.md', nudged: false } }));
    await selectInNote(daemon, 0, 0);
    expect(notebook().queryByRole('button', { name: 'Send to chief' })).toBeNull();

    await selectInNote(daemon, INDEX.indexOf('buy'), INDEX.indexOf('buy') + 'buy milk'.length);
    await click(daemon, notebook().getByRole('button', { name: 'Send to chief' }));

    expect(daemon.sentOf('notebook_send_to_chief')).toEqual([expect.objectContaining({ selection: 'buy milk', source_path: 'knowledge/index.md' })]);
    expect(notebook().getByText("Added to chief's inbox")).toBeInTheDocument();
    expect(notebook().queryByRole('button', { name: 'Send to chief' })).toBeNull();
  });

  it('says why the chief could not take it', async () => {
    stubTextLayout();
    const { daemon } = await openVault();
    daemon.on('notebook_send_to_chief', () => ({ event: 'notebook_send_to_chief_result', success: false, error: 'no chief reachable' }));

    await selectInNote(daemon, INDEX.indexOf('buy'), INDEX.indexOf('buy') + 3);
    await click(daemon, notebook().getByRole('button', { name: 'Send to chief' }));

    expect(notebook().getByText('no chief reachable')).toBeInTheDocument();
  });

  it('shows nothing on the next note when a send answers after the user moved on', async () => {
    stubTextLayout();
    const { daemon } = await openVault();
    hold(daemon, 'notebook_send_to_chief');
    await selectInNote(daemon, INDEX.indexOf('buy'), INDEX.indexOf('buy') + 3);
    await click(daemon, notebook().getByRole('button', { name: 'Send to chief' }));
    const [send] = daemon.sentOf('notebook_send_to_chief');

    await click(daemon, treeItem('notes.txt'));
    daemon.replyTo(send, { event: 'notebook_send_to_chief_result', request_id: send.request_id, success: true, result: { path: 'inbox.md', nudged: false } });
    await daemon.idle();

    expect(notebook().queryByText("Added to chief's inbox")).toBeNull();
  });
});

describe('App notebook finder', () => {
  it('lists the vault once when the notebook opens, and once more after a burst of changes', async () => {
    const { daemon, vault } = await openVault();
    expect(daemon.sentOf('fs_index')).toHaveLength(1);

    vault.files['knowledge/fresh.md'] = '# Fresh';
    for (let change = 0; change < 3; change += 1) await notebookChanged(daemon);
    await act(() => vi.advanceTimersByTimeAsync(299));
    await daemon.idle();
    expect(daemon.sentOf('fs_index')).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(1));
    await daemon.idle();
    expect(daemon.sentOf('fs_index')).toHaveLength(2);

    pressShortcut('file.open', screen.getByRole('dialog', { name: 'Notebook' }));
    await daemon.idle();
    expect(within(screen.getByRole('dialog', { name: 'Find a note' })).getAllByRole('option').map((option) => option.textContent)).toContainEqual(expect.stringContaining('knowledge/fresh.md'));
  });

  it('lists nothing while no notebook surface is open', async () => {
    const { daemon } = await renderApp({ initialState: { settings: { 'notebook.root.effective': NOTEBOOK_ROOT } } });
    await daemon.idle();

    expect(daemon.sentOf('fs_index')).toEqual([]);
  });

  it('opens the notebook’s finder, not the markdown opener, while focus is in the notebook, and the opener once it closes', async () => {
    const { daemon } = await openVault();
    daemon.on('recent_files', () => ({ event: 'recent_files_result', success: true, files: [] }));

    pressShortcut('file.open', documentPane());
    await daemon.idle();
    expect(screen.getByRole('dialog', { name: 'Find a note' })).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Open a markdown file' })).toBeNull();

    fireEvent.keyDown(window, { key: 'Escape' });
    fireEvent.keyDown(window, { key: 'Escape' });
    await daemon.idle();
    expect(screen.queryByRole('dialog', { name: 'Notebook' })).toBeNull();
    pressShortcut('file.open');
    await daemon.idle();

    expect(screen.getByRole('dialog', { name: 'Open a markdown file' })).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Find a note' })).toBeNull();
  });
});

describe('App notebook tile root', () => {
  it.each<[string, Array<Partial<DaemonWorkspace>>, string | undefined, string?]>([
    ['a workspace outside the notebook', [{ directory: '/tmp/project' }], '{"root":"/tmp/project"}'],
    ['a workspace padded with spaces', [{ directory: '  /tmp/project  ' }], '{"root":"/tmp/project"}'],
    ['the notebook itself', [{ directory: NOTEBOOK_ROOT }], undefined],
    ['a workspace without a directory', [{ directory: '' }], undefined],
    ['a remote workspace', [{ directory: '/srv/project', endpoint_id: 'ep-1' }], undefined, 'ep-1'],
    ['a workspace id a remote twin shares', [{ directory: '/tmp/project' }, { directory: '/srv/project', endpoint_id: 'ep-1' }], undefined],
  ])('roots a notebook tile opened in %s', async (_, records, tileParams, endpoint) => {
    const { daemon } = await renderApp({
      initialState: {
        settings: { 'notebook.root.effective': NOTEBOOK_ROOT },
        endpoints: [daemonEndpoint('ep-1')],
        sessions: [daemonSession('s1', { state: 'idle', ...(endpoint ? { endpoint_id: endpoint } : {}) })],
        workspaces: records.map((record) => ({ ...agentWorkspace('s1'), ...record })),
      },
    });
    fireEvent.click(screen.getAllByRole('button', { name: 'Open s1' })[0]);
    await daemon.idle();

    pressShortcut('notebook.openTile');
    await daemon.idle();

    const [dock] = daemon.sentOf('workspace_layout_dock_tile');
    expect(dock).toMatchObject({ workspace_id: 'workspace-s1', tile_kind: 'notebook' });
    expect(dock.tile_params).toBe(tileParams);
  });
});
