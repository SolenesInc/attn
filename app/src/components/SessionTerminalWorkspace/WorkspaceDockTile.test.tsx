import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, within } from '@testing-library/react';
import { EditorView } from '@codemirror/view';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { open } from '@tauri-apps/plugin-dialog';
import { openUrl } from '@tauri-apps/plugin-opener';
import { resolveMarkdownTarget } from '../MarkdownReader/markdownLinks';
import { normalizeBrowserAddress } from './browserAddress';
import { deriveTileTitle } from '../../utils/tilePresentation';
import { serializeNotebookTileParams, type TileLeaf } from '../../types/workspace';
import type { WireAnnotation } from '../MarkdownReader/annotations/types';
import { annotationToWire } from '../MarkdownReader/annotations/types';
import { createAnchor, extractBlockTexts } from '../MarkdownReader/anchoring';
import { gesture, renderApp } from '../../test/renderApp';
import {
  agentPane,
  daemonSeed,
  daemonSession,
  daemonWorkspace,
  dockTiles,
  seedDocument,
  type DaemonSeed,
  type DaemonSeedDocument,
  type DaemonTile,
  type DaemonWorkspace,
} from '../../test/daemonFixtures';
import type { CommandMessage } from '../../test/protocol';
import type { Reply, ScriptedDaemon } from '../../test/scriptedDaemon';

const mermaidMock = vi.hoisted(() => ({
  render: vi.fn(async () => ({ svg: '<svg data-testid="mermaid-svg"></svg>' })),
  initialize: vi.fn(),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: mermaidMock.initialize,
    render: mermaidMock.render,
  },
}));

const invokeMock = vi.mocked(invoke);

const WORKSPACE_ID = 'workspace-1';
const WORKSPACE_DIRECTORY = '/Users/victor/code/attn';

const SESSIONS = [
  daemonSession('sess-a', { label: 'alpha', state: 'working', workspace_id: WORKSPACE_ID }),
  daemonSession('sess-b', { label: 'beta', state: 'pending_approval', workspace_id: WORKSPACE_ID }),
];

const AGENT_PANES = {
  type: 'split',
  split_id: 'split-agents',
  direction: 'vertical',
  ratio: 0.5,
  children: [{ type: 'pane', pane_id: 'pane-sess-a' }, { type: 'pane', pane_id: 'pane-sess-b' }],
};

function workspaceWith(tiles: DaemonTile[], overrides: Partial<DaemonWorkspace>): DaemonWorkspace {
  return daemonWorkspace(
    WORKSPACE_ID,
    { root: dockTiles(AGENT_PANES, tiles), panes: [agentPane('sess-a', WORKSPACE_ID), agentPane('sess-b', WORKSPACE_ID)] },
    { title: 'attn', directory: WORKSPACE_DIRECTORY, ...overrides },
  );
}

interface WorkspaceOptions {
  workspace?: Partial<DaemonWorkspace>;
  seeds?: DaemonSeed[];
  settings?: Record<string, string>;
  script?: (daemon: ScriptedDaemon) => void;
}

function serveTileUpdates(daemon: ScriptedDaemon, current: () => DaemonTile[], overrides: Partial<DaemonWorkspace>) {
  let tiles = current;
  daemon.on('workspace_layout_update_tile', ({ workspace_id, tile_id, tile_params, tile_session_id }) => {
    const updated = tiles().map((tile) => (
      tile.tile_id === tile_id ? { ...tile, tile_params, ...(tile_session_id ? { tile_session_id } : {}) } : tile
    ));
    tiles = () => updated;
    return [
      { event: 'workspace_layout_action_result', action: 'workspace_layout_update_tile', workspace_id, tile_id, success: true },
      { event: 'workspace_layout_updated', workspace_layout: workspaceWith(updated, overrides).layout! },
    ];
  });
}

async function openWorkspace(tiles: DaemonTile[], { workspace = {}, seeds = [], settings = {}, script = () => {} }: WorkspaceOptions = {}) {
  const view = await renderApp({
    initialState: {
      sessions: SESSIONS.map((session) => ({ ...session, endpoint_id: workspace.endpoint_id })),
      workspaces: [workspaceWith([], workspace)],
      seeds,
      settings,
    },
  });
  let shown = tiles;
  serveTileUpdates(view.daemon, () => shown, workspace);
  script(view.daemon);
  await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open alpha' })));
  const layout = async (next: DaemonTile[]) => {
    shown = next;
    await gesture(view.daemon, () => view.daemon.emit({
      event: 'workspace_layout_updated',
      workspace_layout: workspaceWith(next, workspace).layout!,
    }));
  };
  await layout(tiles);
  return {
    ...view,
    layout,
    tileUpdates: () => view.daemon.sentOf('workspace_layout_update_tile').map(({ tile_id, tile_params, tile_session_id }) => ({
      tile_id,
      tile_params,
      ...(tile_session_id !== undefined && { tile_session_id }),
    })),
  };
}

function tileElement(tileId: string): HTMLElement {
  return document.querySelector<HTMLElement>(`[data-pane-id="${tileId}"]`)!;
}

function inTile(tileId: string) {
  return within(tileElement(tileId));
}

function tileBody(tileId: string): HTMLElement {
  return tileElement(tileId).querySelector<HTMLElement>('.workspace-dock-tile-body')!;
}

const MARKDOWN_PATH = '/tmp/project/README.md';

function serveTileContent(content: string) {
  return (daemon: ScriptedDaemon) => {
    daemon.on('workspace_tile_content_get', ({ workspace_id, tile_id }) => ({
      event: 'workspace_tile_content',
      workspace_id,
      tile_id,
      tile_kind: 'markdown',
      path: MARKDOWN_PATH,
      content,
    }));
  };
}

function openMarkdown(content: string, workspace: Partial<DaemonWorkspace> = {}) {
  return openWorkspace(
    [{ tile_id: 'tile-markdown', tile_kind: 'markdown', tile_params: MARKDOWN_PATH }],
    { workspace, script: serveTileContent(content) },
  );
}

describe('WorkspaceDockTile Markdown rendering', () => {
  beforeEach(() => {
    invokeMock.mockResolvedValue(undefined);
    vi.mocked(openUrl).mockClear();
  });

  it('resolves local Markdown targets relative to the opened document', () => {
    expect(resolveMarkdownTarget('/tmp/project/README.md', 'docs/setup.md')).toEqual({
      kind: 'local',
      value: '/tmp/project/docs/setup.md',
    });
    expect(resolveMarkdownTarget('/tmp/project/README.md', 'https://example.test/guide')).toEqual({
      kind: 'external',
      value: 'https://example.test/guide',
    });
    expect(resolveMarkdownTarget('/tmp/project/README.md', 'javascript:alert(1)')).toBeNull();
  });

  it('blocks automatic remote image loads', async () => {
    await openMarkdown('![tracking](https://example.test/pixel?id=123)');

    expect(tileElement('tile-markdown').querySelector('img')).toBeNull();
    expect(inTile('tile-markdown').getByText('[blocked image: tracking]')).toBeInTheDocument();
    expect(openUrl).not.toHaveBeenCalled();
  });

  it('renders relative local images inline via the asset protocol', async () => {
    await openMarkdown('![diagram](docs/diagram.png)');

    const img = tileElement('tile-markdown').querySelector('img.md-reader-image');
    expect(img).toHaveAttribute('src', 'asset://localhost//tmp/project/docs/diagram.png');
    expect(invokeMock).not.toHaveBeenCalledWith('open_safe_markdown_target', expect.anything());
  });

  it('opens relative and external links through the Tauri opener', async () => {
    await openMarkdown('[guide](docs/setup.md) [site](https://example.test/docs)');

    fireEvent.click(inTile('tile-markdown').getByRole('link', { name: 'guide' }));
    expect(invokeMock).toHaveBeenCalledWith('open_safe_markdown_target', {
      path: '/tmp/project/docs/setup.md',
    });

    fireEvent.click(inTile('tile-markdown').getByRole('link', { name: 'site' }));
    expect(openUrl).toHaveBeenCalledWith('https://example.test/docs');
  });

  it('disables local targets for remote workspace content', async () => {
    await openMarkdown(
      '[guide](docs/setup.md) ![diagram](docs/diagram.png) [site](https://example.test/docs)',
      { endpoint_id: 'endpoint-1' },
    );
    const tile = inTile('tile-markdown');

    expect(tile.queryByRole('link', { name: 'guide' })).toBeNull();
    expect(tile.getByText('[blocked image: diagram]')).toBeInTheDocument();
    expect(tileElement('tile-markdown').querySelector('img.md-reader-image')).toBeNull();

    fireEvent.click(tile.getByRole('link', { name: 'site' }));
    expect(invokeMock).not.toHaveBeenCalledWith('open_safe_markdown_target', expect.anything());
    expect(openUrl).toHaveBeenCalledWith('https://example.test/docs');
  });

  it('blocks executable-associated local targets from repository Markdown', async () => {
    await openMarkdown('[guide](scripts/setup.command) ![diagram](scripts/setup.command)');
    const tile = inTile('tile-markdown');

    expect(tile.queryByRole('link', { name: 'guide' })).toBeNull();
    expect(tile.getByText('guide')).toHaveAttribute(
      'title',
      'Blocked local target: /tmp/project/scripts/setup.command',
    );
    expect(tile.getByText('[blocked image: diagram]')).toBeInTheDocument();
    expect(invokeMock).not.toHaveBeenCalledWith('open_safe_markdown_target', expect.anything());
  });

  it('adds duplicate-safe heading ids for fragment links', async () => {
    await openMarkdown('[Jump](#setup)\n\n## Setup\n\n## Setup');
    const tile = inTile('tile-markdown');

    expect(tile.getByRole('link', { name: 'Jump' })).toHaveAttribute('href', '#setup');
    expect(tile.getAllByRole('heading', { name: 'Setup' }).map((heading) => heading.id)).toEqual([
      'setup',
      'setup-1',
    ]);
  });

  it('renders a mermaid fence as a diagram via the shared Markdown renderer', async () => {
    const rendered = new Promise<void>((resolve) => {
      mermaidMock.render.mockImplementationOnce(async () => {
        resolve();
        return { svg: '<svg data-testid="mermaid-svg"></svg>' };
      });
    });
    const { daemon } = await openMarkdown('```mermaid\ngraph TD;\nA-->B;\n```');
    await rendered;
    await daemon.idle();

    expect(inTile('tile-markdown').getByTestId('mermaid-svg')).toBeInTheDocument();
  });
});

describe('deriveTileTitle', () => {
  const markdownTile: TileLeaf = {
    type: 'tile',
    tileId: 'tile-markdown',
    tileKind: 'markdown',
    tileParams: '/tmp/project/notes.md',
  };

  it('uses the H1 heading when the document leads with one', () => {
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '# Project notes\n\nbody' }))
      .toBe('Project notes');
  });

  it('strips a heading marker of any level and inline markdown', () => {
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '## **Setup** `steps`' }))
      .toBe('Setup steps');
  });

  it('falls back to the first non-empty line when there is no heading', () => {
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '\n\nJust some plain notes here' }))
      .toBe('Just some plain notes here');
  });

  it('skips a closed YAML frontmatter block', () => {
    const content = '---\ntitle: ignored\n---\n# Real title\n';
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content })).toBe('Real title');
  });

  it('keeps a leading horizontal rule as content when there is no closing fence', () => {
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '---\nstill text' }))
      .toBe('still text');
  });

  it('truncates a very long title with an ellipsis', () => {
    const long = `# ${'word '.repeat(40).trim()}`;
    const title = deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: long });
    expect(title.endsWith('…')).toBe(true);
    expect(title.length).toBeLessThanOrEqual(80);
  });

  it('falls back to the basename for empty or error content', () => {
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '   \n  ' })).toBe('notes.md');
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '', error: 'boom' }))
      .toBe('notes.md');
  });

  it('uses the basename before content loads, and tile kind without a path', () => {
    expect(deriveTileTitle(markdownTile, undefined)).toBe('notes.md');
    expect(deriveTileTitle({ type: 'tile', tileId: 'tile-x', tileKind: 'markdown' }, undefined)).toBe('markdown');
  });

  it('falls back to "Editor" for a notebook tile with no open file yet', () => {
    expect(deriveTileTitle({ type: 'tile', tileId: 'tile-notebook', tileKind: 'notebook' }, undefined)).toBe('Editor');
  });

  it('uses the host as the title for a browser tile', () => {
    expect(deriveTileTitle({
      type: 'tile',
      tileId: 'tile-browser',
      tileKind: 'browser',
      tileParams: 'http://localhost:3000/dashboard',
    })).toBe('localhost:3000');
  });
});

const BROWSER_TILE: DaemonTile = { tile_id: 'tile-browser', tile_kind: 'browser', tile_params: 'https://backstage.spotify.net' };
const BROWSER_LABEL = 'browser-workspace-1-tile-browser';

describe('WorkspaceDockTile browser integration', () => {
  beforeEach(() => {
    invokeMock.mockResolvedValue(undefined);
  });

  it('closes the exact browser tile targeted by the native close command', async () => {
    const { daemon } = await openWorkspace([BROWSER_TILE]);

    expect(inTile('tile-browser').getByText('Error: In-app browser hosting requires the Tauri app')).toBeInTheDocument();
    await gesture(daemon, () => {
      window.dispatchEvent(new CustomEvent('attn:native-browser-close', { detail: BROWSER_LABEL }));
    });

    expect(daemon.sentOf('workspace_layout_undock_tile')).toEqual([
      expect.objectContaining({ workspace_id: WORKSPACE_ID, tile_id: 'tile-browser' }),
    ]);
  });

  it('reloads the browser from its tile header', async () => {
    await openWorkspace([BROWSER_TILE]);

    expect(inTile('tile-browser').getByText('Error: In-app browser hosting requires the Tauri app')).toBeInTheDocument();
    fireEvent.click(inTile('tile-browser').getByRole('button', { name: 'Reload browser' }));

    expect(invokeMock).toHaveBeenCalledWith('browser_host_control', {
      label: BROWSER_LABEL,
      action: 'reload',
      params: undefined,
      selector: undefined,
      text: undefined,
    });
  });

  it('claims browser close ownership from header controls', async () => {
    const view = await openWorkspace([]);
    vi.mocked(isTauri).mockReturnValue(true);
    await view.layout([BROWSER_TILE]);

    fireEvent.pointerDown(inTile('tile-browser').getByRole('textbox', { name: 'Browser address' }));

    expect(invokeMock).toHaveBeenCalledWith('browser_host_claim_focus', { label: BROWSER_LABEL });
  });

  it('navigates from the address bar and tracks native location changes', async () => {
    const view = await openWorkspace([BROWSER_TILE]);
    const address = inTile('tile-browser').getByRole('textbox', { name: 'Browser address' });

    fireEvent.change(address, { target: { value: 'example.com/docs' } });
    await gesture(view.daemon, () => fireEvent.submit(address.closest('form')!));

    expect(invokeMock).toHaveBeenCalledWith('browser_host_control', {
      label: BROWSER_LABEL,
      action: 'navigate',
      params: JSON.stringify({ url: 'https://example.com/docs' }),
      selector: undefined,
      text: undefined,
    });

    const reportLocation = () => gesture(view.daemon, () => {
      window.dispatchEvent(new CustomEvent('attn:browser-location', {
        detail: { label: BROWSER_LABEL, url: 'https://example.com/redirected' },
      }));
    });
    await reportLocation();

    expect(inTile('tile-browser').getByRole('textbox', { name: 'Browser address' })).toHaveValue('https://example.com/redirected');
    expect(view.tileUpdates()).toEqual([{ tile_id: 'tile-browser', tile_params: 'https://example.com/redirected' }]);

    await reportLocation();
    expect(view.tileUpdates()).toHaveLength(1);
  });

  it('normalizes host-and-port browser addresses', () => {
    expect(normalizeBrowserAddress('localhost:3000')).toBe('http://localhost:3000');
    expect(normalizeBrowserAddress('127.0.0.1:8080/path')).toBe('http://127.0.0.1:8080/path');
    expect(normalizeBrowserAddress('example.com:8080')).toBe('https://example.com:8080');
    expect(normalizeBrowserAddress('http://example.com:8080')).toBe('http://example.com:8080');
    expect(normalizeBrowserAddress('ftp://example.com')).toBe('ftp://example.com');
  });
});

type WriteOutcome = 'saved' | 'conflict' | 'error';

function serveNotebook(daemon: ScriptedDaemon, write: WriteOutcome) {
  daemon.on('fs_list', () => ({
    event: 'fs_list_result',
    success: true,
    entries: [{ path: 'notes.md', name: 'notes.md', is_dir: false, size: 5 }],
  }));
  daemon.on('fs_read', ({ path }) => ({ event: 'fs_read_result', success: true, result: { path, content: 'hello', hash: 'h1' } }));
  daemon.on('fs_write', ({ path }) => {
    if (write === 'error') return { event: 'fs_write_result', success: false, error: 'disk is read-only' };
    return { event: 'fs_write_result', success: true, result: { path, hash: 'h2', conflict: write === 'conflict', current_hash: 'h9' } };
  });
}

function typeInEditor(text: string) {
  const view = EditorView.findFromDOM(tileElement('tile-notebook').querySelector<HTMLElement>('.cm-content')!)!;
  act(() => view.dispatch({ changes: { from: view.state.doc.length, insert: text } }));
}

describe('WorkspaceDockTile notebook root switcher', () => {
  beforeEach(() => {
    vi.mocked(open).mockReset();
  });

  async function openNotebookTile({ tileParams, directory = WORKSPACE_DIRECTORY, write = 'saved' }: {
    tileParams?: string;
    directory?: string;
    write?: WriteOutcome;
  } = {}) {
    const view = await openWorkspace(
      [{ tile_id: 'tile-notebook', tile_kind: 'notebook', ...(tileParams !== undefined && { tile_params: tileParams }) }],
      {
        workspace: { directory },
        settings: { 'notebook.root.effective': '/notebook-root' },
        script: (daemon) => serveNotebook(daemon, write),
      },
    );
    const opened = view.tileUpdates().length;
    return {
      ...view,
      rootChanges: () => view.tileUpdates().slice(opened),
    };
  }

  function picker() {
    return inTile('tile-notebook').getByRole('combobox', { name: 'Editor root' });
  }

  function pick(daemon: ScriptedDaemon, value: string) {
    return gesture(daemon, () => fireEvent.change(picker(), { target: { value } }));
  }

  const openNote = serializeNotebookTileParams({ root: undefined, path: 'notes.md' });
  const workspaceRoot = serializeNotebookTileParams({ root: WORKSPACE_DIRECTORY });
  const pickedRoot = (tile_params: string) => [{ tile_id: 'tile-notebook', tile_params }];

  it('offers Notebook and Workspace options for a rootless tile with a distinct workspace directory', async () => {
    await openNotebookTile();

    const options = Array.from(picker().querySelectorAll('option')).map((option) => option.textContent);
    expect(options).toEqual(['Notebook', 'Workspace — attn', 'Browse…']);
    expect(picker()).toHaveValue('');
  });

  it('adds the current root as its own option when it matches neither the notebook root nor the workspace directory', async () => {
    await openNotebookTile({ tileParams: serializeNotebookTileParams({ root: '/tmp/some-other-root' }) });

    const options = Array.from(picker().querySelectorAll('option')).map((option) => option.textContent);
    expect(options).toEqual(['Notebook', 'Workspace — attn', 'some-other-root', 'Browse…']);
    expect(picker()).toHaveValue('/tmp/some-other-root');
  });

  it('omits the Workspace option when no workspace directory is set', async () => {
    await openNotebookTile({ directory: '' });

    const options = Array.from(picker().querySelectorAll('option')).map((option) => option.textContent);
    expect(options).toEqual(['Notebook', 'Browse…']);
  });

  it('selecting Notebook writes rootless params without the open path', async () => {
    const view = await openNotebookTile({
      tileParams: serializeNotebookTileParams({ root: '/tmp/some-other-root', path: 'notes.md' }),
    });

    await pick(view.daemon, '');

    expect(view.rootChanges()).toEqual(pickedRoot(serializeNotebookTileParams({ root: undefined })));
  });

  it('selecting the workspace directory writes it as the root without the open path', async () => {
    const view = await openNotebookTile({ tileParams: openNote });

    await pick(view.daemon, WORKSPACE_DIRECTORY);

    expect(view.rootChanges()).toEqual(pickedRoot(workspaceRoot));
  });

  it('Browse… persists the chosen directory as the root', async () => {
    vi.mocked(open).mockResolvedValue('/tmp/chosen-root');
    const view = await openNotebookTile();

    await pick(view.daemon, '__browse__');

    expect(view.rootChanges()).toEqual(pickedRoot(serializeNotebookTileParams({ root: '/tmp/chosen-root' })));
    expect(open).toHaveBeenCalledWith({ directory: true, multiple: false, title: 'Choose editor root' });
  });

  it('Browse… cancelled leaves the tile params untouched', async () => {
    vi.mocked(open).mockResolvedValue(null);
    const view = await openNotebookTile();

    await pick(view.daemon, '__browse__');

    expect(open).toHaveBeenCalled();
    expect(view.rootChanges()).toEqual([]);
  });

  it('saves the unsaved edit before switching the root', async () => {
    const view = await openNotebookTile({ tileParams: openNote });

    typeInEditor(' world');
    await pick(view.daemon, WORKSPACE_DIRECTORY);

    const [write] = view.daemon.sentOf('fs_write');
    const [rootChange] = view.daemon.sentOf('workspace_layout_update_tile').filter(({ tile_params }) => tile_params === workspaceRoot);
    expect(write).toMatchObject({ path: 'notes.md', content: 'hello world' });
    expect(view.daemon.sent.indexOf(write)).toBeLessThan(view.daemon.sent.indexOf(rootChange));
  });

  it('keeps the root when saving the unsaved edit conflicts', async () => {
    const view = await openNotebookTile({ tileParams: openNote, write: 'conflict' });

    typeInEditor(' world');
    await pick(view.daemon, WORKSPACE_DIRECTORY);

    expect(view.daemon.sentOf('fs_write')).toEqual([expect.objectContaining({ path: 'notes.md', content: 'hello world' })]);
    expect(view.rootChanges()).toEqual([]);
  });

  it('keeps the root when Browse… picks a directory but saving the unsaved edit fails', async () => {
    vi.mocked(open).mockResolvedValue('/tmp/chosen-root');
    const view = await openNotebookTile({ tileParams: openNote, write: 'error' });

    typeInEditor(' world');
    await pick(view.daemon, '__browse__');

    expect(view.daemon.sentOf('fs_write')).toHaveLength(1);
    expect(view.rootChanges()).toEqual([]);
  });
});

const SEND_DOC = 'First paragraph with target words inside it.\n';
const SEND_URI = 'attn://file/workspace-1/%2Ftmp%2Fproject%2FREADME.md';

function globalNote(id = 'g1'): WireAnnotation {
  return { id, type: 'global', text: 'whole-doc note', created_at: 1 };
}

function anchoredNote(content: string, needle: string): WireAnnotation {
  const blocks = extractBlockTexts(content);
  const block = blocks.find((candidate) => candidate.text.includes(needle))!;
  const start = block.text.indexOf(needle);
  return annotationToWire({
    id: 'stored-1',
    type: 'comment',
    text: 'stay attached',
    anchor: createAnchor(content, block.blockId, start, start + needle.length, blocks)!,
    createdAt: 1,
  });
}

type Submit = CommandMessage<'markdown_annotations_submit'>;
type SubmitAnswer = (command: Submit) => Reply | undefined;

const answered = (fields: { success: boolean; status: string; error?: string; generation?: number }): SubmitAnswer =>
  ({ document_uri, source_kind }) => ({ event: 'markdown_annotations_submit_result', document_uri, source_kind, ...fields });

const delivered = answered({ success: true, status: 'delivered', generation: 6 });

interface AnnotationScript {
  seeded?: WireAnnotation[];
  submit?: SubmitAnswer;
  hydrate?: boolean;
}

function serveAnnotations(daemon: ScriptedDaemon, { seeded = [globalNote()], submit = delivered, hydrate = true }: AnnotationScript) {
  daemon.on('markdown_annotations_get', ({ document_uri, source_kind }) => (hydrate
    ? { event: 'markdown_annotations_get_result', document_uri, source_kind, success: true, annotations: seeded, generation: 5 }
    : undefined));
  daemon.on('markdown_annotations_save', ({ document_uri, source_kind, generation }) => (
    { event: 'markdown_annotations_save_result', document_uri, source_kind, success: true, generation: generation + 1 }
  ));
  daemon.on('markdown_annotations_clear', ({ document_uri, source_kind, generation }) => (
    { event: 'markdown_annotations_clear_result', document_uri, source_kind, success: true, generation: generation + 1 }
  ));
  daemon.on('markdown_annotations_submit', (command) => submit(command));
}

const submissions = (daemon: ScriptedDaemon) =>
  daemon.sentOf('markdown_annotations_submit').map(({ document_uri, target_session_id, target_seed_id, orphaned_ids }) => ({
    document_uri,
    ...(target_session_id !== undefined && { target_session_id }),
    ...(target_seed_id !== undefined && { target_seed_id }),
    orphaned_ids: orphaned_ids ?? [],
  }));

function sendTile(tileSessionId: string): DaemonTile {
  return { tile_id: 'tile-md', tile_kind: 'markdown', tile_params: MARKDOWN_PATH, tile_session_id: tileSessionId };
}

function openSendTile({
  tileSessionId = 'sess-a',
  retarget = true,
  annotations = {},
}: { tileSessionId?: string; retarget?: boolean; annotations?: AnnotationScript } = {}) {
  return openWorkspace([sendTile(tileSessionId)], {
    script: (daemon) => {
      serveTileContent(SEND_DOC)(daemon);
      serveAnnotations(daemon, annotations);
      if (!retarget) {
        daemon.on('workspace_layout_update_tile', ({ workspace_id, tile_id }) => ({
          event: 'workspace_layout_action_result',
          action: 'workspace_layout_update_tile',
          workspace_id,
          tile_id,
          success: false,
          error: 'retarget rejected',
        }));
      }
    },
  });
}

function sendButton() {
  return screen.getByRole('button', {
    name: /^(Send \d+(?: to .+)?|Sending…|Sent ✓|Noted ✓|Approval needed to .+|Needs attention to .+|Send failed to .+)$/,
  });
}

function openSessionDestinations() {
  fireEvent.click(screen.getByRole('button', { name: 'Change annotation destination' }));
}

function chooseSession(label: string) {
  fireEvent.click(screen.getByRole('menuitemradio', { name: label }));
}

function focusTerminal(daemon: ScriptedDaemon) {
  return gesture(daemon, () => fireEvent.mouseDown(document.querySelector('[data-pane-id="pane-sess-a"]')!));
}

function pressCmdEnter(target: EventTarget = window): KeyboardEvent {
  const event = new KeyboardEvent('keydown', {
    key: 'Enter',
    metaKey: true,
    bubbles: true,
    cancelable: true,
  });
  act(() => {
    target.dispatchEvent(event);
  });
  return event;
}

describe('WorkspaceDockTile markdown send flow', () => {
  it('shows the bound session in Send and flags approval-blocked destinations (E13)', async () => {
    await openSendTile();

    expect(sendButton()).toHaveAccessibleName('Send 1 to alpha');
    expect(sendButton()).toBeEnabled();
    openSessionDestinations();
    expect(screen.getByRole('menuitemradio', { name: 'alpha' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('menuitemradio', { name: 'beta approval' })).toHaveAttribute('aria-checked', 'false');
  });

  it('navigates destination choices with arrows and returns focus on Escape', async () => {
    const { daemon } = await openSendTile();
    expect(sendButton()).toBeEnabled();

    const caret = screen.getByRole('button', { name: 'Change annotation destination' });
    await gesture(daemon, () => fireEvent.click(caret));
    const alpha = screen.getByRole('menuitemradio', { name: 'alpha' });
    const beta = screen.getByRole('menuitemradio', { name: 'beta approval' });
    expect(alpha).toHaveFocus();
    fireEvent.keyDown(alpha, { key: 'ArrowDown' });
    expect(beta).toHaveFocus();

    fireEvent.keyDown(window, { key: 'Escape' });
    act(() => { vi.advanceTimersToNextFrame(); });
    expect(screen.queryByRole('menu', { name: 'Send annotations to session' })).toBeNull();
    expect(caret).toHaveFocus();
  });

  it('retargets through the destination menu and follows the layout broadcast echo (E13)', async () => {
    const view = await openSendTile();
    expect(sendButton()).toHaveAccessibleName('Send 1 to alpha');

    openSessionDestinations();
    chooseSession('beta approval');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
    await view.daemon.idle();

    expect(view.tileUpdates()).toEqual([{ tile_id: 'tile-md', tile_params: MARKDOWN_PATH, tile_session_id: 'sess-b' }]);
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
  });

  it('a retarget takes effect immediately for Send without waiting for a broadcast (E13)', async () => {
    const view = await openSendTile();
    view.daemon.on('workspace_layout_update_tile', () => {});
    expect(sendButton()).toBeEnabled();

    openSessionDestinations();
    chooseSession('beta approval');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
    await gesture(view.daemon, () => fireEvent.click(sendButton()));
    expect(submissions(view.daemon)).toEqual([{ document_uri: SEND_URI, target_session_id: 'sess-b', orphaned_ids: [] }]);

    await view.layout([sendTile('sess-b')]);
    openSessionDestinations();
    expect(screen.getByRole('menuitemradio', { name: 'beta approval' })).toHaveAttribute('aria-checked', 'true');
  });

  it('rolls Send back to the persisted binding when retargeting fails (E13)', async () => {
    const { daemon } = await openSendTile({ retarget: false });
    expect(sendButton()).toBeEnabled();

    openSessionDestinations();
    chooseSession('beta approval');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
    await daemon.idle();
    expect(sendButton()).toHaveAccessibleName('Send 1 to alpha');
  });

  it('shows a disabled Send with No session when the bound session left the workspace (E13)', async () => {
    await openSendTile({ tileSessionId: 'sess-gone' });

    expect(sendButton()).toHaveTextContent('Send 1');
    expect(sendButton()).toHaveTextContent('No session');
    expect(sendButton()).toBeDisabled();
  });

  it('replaces Send 0 with the current destination (E14)', async () => {
    await openSendTile({ annotations: { seeded: [] } });

    expect(screen.getByRole('button', { name: 'Annotation destination: alpha' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: /Send 0/ })).toBeNull();
  });

  it('opens overall notes and the floating review inspector from the tile header', async () => {
    const { daemon } = await openSendTile({ annotations: { seeded: [] } });
    expect(screen.getByRole('button', { name: 'Annotation destination: alpha' })).toBeInTheDocument();

    for (const note of ['First overall note', 'Second overall note']) {
      fireEvent.click(inTile('tile-md').getByRole('button', { name: 'Overall note' }));
      fireEvent.change(screen.getByPlaceholderText('Add an overall note...'), { target: { value: note } });
      fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    }

    expect(screen.getByRole('button', { name: 'Notes 2' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Notes 2' }));
    expect(screen.getByRole('dialog', { name: 'Review notes' })).toBeInTheDocument();
    expect(screen.getByText('First overall note')).toBeInTheDocument();
    expect(screen.getByText('Second overall note')).toBeInTheDocument();

    await gesture(daemon, () => fireEvent.click(sendButton()));
    const saves = daemon.sentOf('markdown_annotations_save');
    expect(saves.length).toBeGreaterThan(0);
    expect(saves[0]).toMatchObject({ document_uri: SEND_URI });
    expect(saves[saves.length - 1].annotations.filter((annotation) => annotation.type === 'global')).toHaveLength(2);
  });

  it('delivers: Sending… → Sent ✓, list empties locally without re-fetch or second clear (E14)', async () => {
    const held: Submit[] = [];
    const { daemon } = await openSendTile({ annotations: { submit: (command) => void held.push(command) } });
    expect(sendButton()).toBeEnabled();

    fireEvent.click(sendButton());
    expect(sendButton()).toHaveTextContent('Sending…');
    await daemon.idle();
    expect(submissions(daemon)).toEqual([{ document_uri: SEND_URI, target_session_id: 'sess-a', orphaned_ids: [] }]);

    const answer = delivered(held[0])!;
    await gesture(daemon, () => daemon.replyTo(held[0], { ...answer, request_id: held[0].request_id, generation: 9 } as Reply));
    expect(screen.getByRole('status')).toHaveTextContent('Sent ✓');
    expect(sendButton()).toHaveTextContent('Sent ✓');
    expect(sendButton()).toBeDisabled();
    expect(daemon.sentOf('markdown_annotations_get')).toHaveLength(1);
    expect(daemon.sentOf('markdown_annotations_clear')).toEqual([]);
  });

  it('delivered-but-clear-failed keeps annotations and surfaces the warning, not Sent ✓ (E14)', async () => {
    const { daemon } = await openSendTile({
      annotations: { submit: answered({ success: true, status: 'delivered', error: 'delivered; failed to clear drafts: disk full' }) },
    });
    expect(sendButton()).toBeEnabled();

    await gesture(daemon, () => fireEvent.click(sendButton()));

    expect(screen.getByRole('status')).toHaveTextContent('failed to clear drafts');
    expect(sendButton()).toHaveTextContent('Needs attention');
    expect(sendButton()).toBeEnabled();
    expect(screen.queryByText('Sent ✓')).toBeNull();
  });

  it('refuses to Send while the draft is not hydrated (stale-draft guard, E14)', async () => {
    const { daemon } = await openSendTile({ annotations: { hydrate: false } });

    fireEvent.click(inTile('tile-md').getByRole('button', { name: 'Overall note' }));
    fireEvent.change(screen.getByPlaceholderText('Add an overall note...'), {
      target: { value: 'unsaved local note' },
    });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Add' })));
    expect(sendButton()).toHaveTextContent('Send 1');

    await gesture(daemon, () => fireEvent.click(sendButton()));
    expect(screen.getByRole('status')).toHaveTextContent('still syncing');
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);
    expect(sendButton()).toHaveTextContent('Send failed');
  });

  it('keeps annotations and explains when the target is waiting on approval (E15)', async () => {
    const { daemon } = await openSendTile({ annotations: { submit: answered({ success: false, status: 'skipped_pending_approval' }) } });
    expect(sendButton()).toBeEnabled();

    await gesture(daemon, () => fireEvent.click(sendButton()));

    expect(screen.getByRole('status')).toHaveTextContent('Target is waiting for approval — not sent');
    expect(sendButton()).toHaveTextContent('Approval needed');
    expect(sendButton()).toBeEnabled();
  });

  it('keeps annotations and surfaces the message on a rejected submit (E15)', async () => {
    const { daemon } = await openSendTile({ annotations: { submit: answered({ success: false, status: '', error: 'session not found' }) } });
    expect(sendButton()).toBeEnabled();

    await gesture(daemon, () => fireEvent.click(sendButton()));

    expect(screen.getByRole('status')).toHaveTextContent('session not found');
    expect(sendButton()).toHaveTextContent('Send failed');
  });

  it('⌘Enter sends when focus is inside the tile and annotations exist (E18)', async () => {
    const { daemon } = await openSendTile();
    expect(sendButton()).toBeEnabled();

    fireEvent.focusIn(tileBody('tile-md'));
    const event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(true);
    await daemon.idle();
    expect(submissions(daemon)).toEqual([{ document_uri: SEND_URI, target_session_id: 'sess-a', orphaned_ids: [] }]);
  });

  it('⌘Enter never fires from a textarea or without tile focus (E18)', async () => {
    const { daemon } = await openSendTile();
    expect(sendButton()).toBeEnabled();
    await focusTerminal(daemon);

    expect(pressCmdEnter().defaultPrevented).toBe(false);
    await daemon.idle();
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);

    const textarea = document.createElement('textarea');
    tileBody('tile-md').appendChild(textarea);
    fireEvent.focusIn(textarea);
    expect(pressCmdEnter(textarea).defaultPrevented).toBe(false);
    await daemon.idle();
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);
  });

  it('⌘Enter does nothing when there is nothing to send (E18)', async () => {
    const { daemon } = await openSendTile({ annotations: { seeded: [] } });
    expect(screen.getByRole('button', { name: 'Annotation destination: alpha' })).toBeInTheDocument();

    fireEvent.focusIn(tileBody('tile-md'));
    const event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(false);
    await daemon.idle();
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);
  });
});

const PLAN_BODY = '# Seed body\n\nAnnotate this plan.';
const SEED_AT = '2026-08-15T08:00:00Z';
const ONE_DONE = { total: 1, done: 1, withered: 0, growing: 0, dormant: 0, ready: 0, blocked: 0 };
const ONE_GROWING = { total: 1, done: 0, withered: 0, growing: 1, dormant: 0, ready: 0, blocked: 0 };

function planSeed(overrides: Partial<DaemonSeed> = {}): DaemonSeed {
  return daemonSeed('s-plan11', {
    title: 'Seed reader plan',
    body: PLAN_BODY,
    step_slug: 'seed-reader-plan',
    tender_session: 'sess-a',
    tender_member: 'trellis',
    state_changed_at: SEED_AT,
    created_at: SEED_AT,
    updated_at: SEED_AT,
    ...overrides,
  });
}

function planDocument(seed: DaemonSeed, overrides: Partial<DaemonSeedDocument> = {}): DaemonSeedDocument {
  return seedDocument(seed, {
    tender_holds: true,
    children: [planSeed({ id: 's-child1', title: 'Reader child', body: '', status: 'harvested' })],
    notes: [{
      id: 'n-live11',
      seed_id: 's-plan11',
      kind: 'note',
      body: 'Live ledger note',
      author_session: 'sess-a',
      author_member: 'trellis',
      created_at: '2026-08-15T09:00:00Z',
    }],
    notes_total: 1,
    ...overrides,
  });
}

type SeedRead = CommandMessage<'seed_document_get'>;
const SEED_URI = 'attn://seed/s-plan11';

function seedTile(seedId: string, tileSessionId?: string): DaemonTile {
  return { tile_id: `tile-seed-${seedId}`, tile_kind: 'seed', tile_params: seedId, ...(tileSessionId && { tile_session_id: tileSessionId }) };
}

async function openSeedTile(
  tile: DaemonTile,
  seeds: DaemonSeed[],
  answer: (seedId: string) => DaemonSeedDocument | Error | undefined,
  annotations: AnnotationScript = {},
) {
  const view = await openWorkspace([tile], {
    seeds,
    script: (daemon) => {
      serveAnnotations(daemon, annotations);
      daemon.on('seed_document_get', ({ seed_id }) => {
        const reply = answer(seed_id);
        if (reply === undefined) return;
        return reply instanceof Error
          ? { event: 'seed_document_get_result', success: false, error: reply.message }
          : { event: 'seed_document_get_result', success: true, document: reply };
      });
    },
  });
  return {
    ...view,
    reads: () => view.daemon.sentOf('seed_document_get'),
    annotationReads: () => view.daemon.sentOf('markdown_annotations_get').map(({ document_uri }) => document_uri),
    deliver: (read: SeedRead, document: DaemonSeedDocument) => gesture(view.daemon, () => {
      view.daemon.replyTo(read, { event: 'seed_document_get_result', request_id: read.request_id, success: true, document });
    }),
    push: (next: DaemonSeed[]) => gesture(view.daemon, () => {
      view.daemon.emit({ event: 'garden_seeds_updated', seeds: next, total: next.length });
    }),
  };
}

const noted = answered({ success: true, status: 'noted', generation: 8 });

function pressEscape(): KeyboardEvent {
  const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
  fireEvent(window, event);
  return event;
}

describe('WorkspaceDockTile seed reader', () => {
  it('loads the seed document, renders its plot and collapsed log, annotates by seed URI, and refetches on a garden push', async () => {
    const first = planDocument(planSeed({ plot_progress: ONE_DONE }));
    const pushed = planSeed({ body: '# Updated seed body', rev: 2 });
    const documents = [first, planDocument(pushed)];
    const view = await openSeedTile(seedTile('s-plan11', 'sess-a'), [first.seed], () => documents.shift());
    const tile = inTile('tile-seed-s-plan11');

    expect(tile.getByRole('heading', { name: 'Seed body' })).toBeInTheDocument();
    expect(tile.getByText('Reader child')).toBeInTheDocument();
    expect(tile.getByText('Log').closest('details')).not.toHaveAttribute('open');
    fireEvent.click(tile.getByText('Log').closest('summary')!);
    expect(tile.getByText('Live ledger note')).toBeInTheDocument();
    expect(tile.getByText('Seed reader plan', { selector: '.workspace-dock-tile-title' })).toBeInTheDocument();
    expect(tileElement('tile-seed-s-plan11').querySelector('.md-reader--annotating')).toBeInTheDocument();
    expect(view.daemon.sentOf('workspace_tile_content_get')).toEqual([]);
    expect(view.annotationReads()).toEqual([SEED_URI]);

    expect(sendButton()).toHaveTextContent('Send 1');
    await gesture(view.daemon, () => fireEvent.click(sendButton()));
    expect(submissions(view.daemon)).toEqual([{ document_uri: SEED_URI, target_session_id: 'sess-a', orphaned_ids: [] }]);

    await view.push([pushed]);
    expect(tile.getByRole('heading', { name: 'Updated seed body' })).toBeInTheDocument();
    expect(view.reads()).toHaveLength(2);
  });

  it('navigates the plot in place, climbs canonical ancestry, and reveals the current seed in the Garden', async () => {
    const plot = planSeed({ id: 's-plot11', title: 'Reader polish', body: '# Plot plan', plot_progress: ONE_GROWING });
    const child = planSeed({ id: 's-child1', title: 'Polish the tile', body: '# Child body', edges: [{ kind: 'part-of', to: plot.id }] });
    const details = new Map([
      [plot.id, planDocument(plot, { children: [child] })],
      [child.id, planDocument(child, { children: [] })],
    ]);
    const view = await openSeedTile(seedTile(plot.id), [plot, child], (seedId) => (seedId === child.id ? undefined : details.get(seedId)));
    const tile = inTile(`tile-seed-${plot.id}`);

    await gesture(view.daemon, () => fireEvent.click(tile.getByRole('button', { name: /Polish the tile/ })));
    expect(view.tileUpdates()).toEqual([{ tile_id: `tile-seed-${plot.id}`, tile_params: child.id }]);
    expect(tile.queryByRole('heading', { name: 'Plot plan' })).toBeNull();
    expect(tile.getByText('Loading seed…')).toBeInTheDocument();
    expect(tile.getByText(child.title, { selector: '.workspace-dock-tile-title' })).toBeInTheDocument();
    await view.deliver(view.reads().find((read) => read.seed_id === child.id)!, details.get(child.id)!);
    expect(tile.getByRole('heading', { name: 'Child body' })).toBeInTheDocument();
    expect(tile.getByRole('button', { name: 'Back to Reader polish' })).toBeInTheDocument();
    expect(view.annotationReads()).toContain(`attn://seed/${child.id}`);

    await gesture(view.daemon, () => fireEvent.click(tile.getByRole('button', { name: 'Reveal in Garden' })));
    expect(Array.from(document.querySelectorAll('.garden-trail__step'), (step) => step.textContent?.trim()))
      .toEqual(['Garden', 'Reader polish']);
    expect(document.querySelector('.garden-panel .garden-head__title')).toHaveTextContent(child.title);

    await gesture(view.daemon, () => fireEvent.click(tile.getByRole('button', { name: 'Back to Reader polish' })));
    expect(view.tileUpdates().pop()).toEqual({ tile_id: `tile-seed-${plot.id}`, tile_params: plot.id });
    expect(tile.getByRole('heading', { name: 'Plot plan' })).toBeInTheDocument();
  });

  it('unwinds a recursive plot trail with Escape only while the seed tile owns focus', async () => {
    const root = planSeed({ id: 's-root11', title: 'Reader polish', body: '# Root plan', plot_progress: ONE_GROWING });
    const nested = planSeed({
      id: 's-nest11',
      title: 'Nested polish',
      body: '# Nested plan',
      edges: [{ kind: 'part-of', to: root.id }],
      plot_progress: ONE_GROWING,
    });
    const leaf = planSeed({ id: 's-leaf11', title: 'Leaf polish', body: '# Leaf plan', edges: [{ kind: 'part-of', to: nested.id }] });
    const details = new Map([
      [root.id, planDocument(root, { children: [nested] })],
      [nested.id, planDocument(nested, { children: [leaf] })],
      [leaf.id, planDocument(leaf, { children: [] })],
    ]);
    const view = await openSeedTile(seedTile(leaf.id), [root, nested, leaf], (seedId) => details.get(seedId));
    const tile = inTile(`tile-seed-${leaf.id}`);
    const climbedTo = () => view.tileUpdates().map(({ tile_params }) => tile_params);

    expect(tile.getByRole('heading', { name: 'Leaf plan' })).toBeInTheDocument();
    await focusTerminal(view.daemon);
    pressEscape();
    await view.daemon.idle();
    expect(climbedTo()).toEqual([]);

    fireEvent.focusIn(tileBody(`tile-seed-${leaf.id}`));
    expect(pressEscape().defaultPrevented).toBe(true);
    await view.daemon.idle();
    expect(climbedTo()).toEqual([nested.id]);
    expect(tile.getByRole('heading', { name: 'Nested plan' })).toBeInTheDocument();

    expect(pressEscape().defaultPrevented).toBe(true);
    await view.daemon.idle();
    expect(climbedTo()).toEqual([nested.id, root.id]);
    expect(tile.getByRole('heading', { name: 'Root plan' })).toBeInTheDocument();

    pressEscape();
    await view.daemon.idle();
    expect(climbedTo()).toEqual([nested.id, root.id]);
  });

  it('hides the previous document while navigating outside a capped Garden snapshot', async () => {
    const plot = planSeed({ id: 's-plot12', title: 'Sparse plot', body: '# Previous body', plot_progress: { ...ONE_GROWING, growing: 0, ready: 1 } });
    const child = planSeed({ id: 's-child2', title: 'Outside the snapshot', body: '# Next body' });
    const view = await openSeedTile(
      seedTile(plot.id),
      [],
      (seedId) => (seedId === child.id ? undefined : planDocument(plot, { children: [child] })),
    );
    const tile = inTile(`tile-seed-${plot.id}`);

    await gesture(view.daemon, () => fireEvent.click(tile.getByRole('button', { name: /Outside the snapshot/ })));
    expect(tile.queryByRole('heading', { name: 'Previous body' })).toBeNull();
    expect(tile.getByText('Loading seed…')).toBeInTheDocument();
  });

  it('keeps the tended seed primary bound to its live tender and offers Note on seed in the caret menu', async () => {
    const detail = planDocument(planSeed());
    const view = await openSeedTile(seedTile('s-plan11', 'sess-b'), [detail.seed], () => detail, { submit: noted });

    expect(screen.getByRole('button', { name: 'Send 1' })).toBeEnabled();
    expect(screen.queryByRole('combobox', { name: 'Send annotations to session' })).toBeNull();
    const caret = screen.getByRole('button', { name: 'More annotation destinations' });
    expect(caret).toHaveAttribute('aria-haspopup', 'menu');
    fireEvent.click(caret);
    await gesture(view.daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Note on seed' })));

    expect(submissions(view.daemon)).toEqual([{ document_uri: SEED_URI, target_seed_id: 's-plan11', orphaned_ids: [] }]);
    expect(screen.getByRole('status')).toHaveTextContent('Noted ✓');
  });

  it('makes Note on seed the unsplit primary when nobody tends the seed', async () => {
    const detail = planDocument(planSeed({ tender_session: '', tender_member: '' }), { tender_holds: false });
    const view = await openSeedTile(seedTile('s-plan11'), [detail.seed], () => detail, { submit: noted });

    const primary = screen.getByRole('button', { name: 'Note on seed 1' });
    expect(primary).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'More annotation destinations' })).toBeNull();
    await gesture(view.daemon, () => fireEvent.click(primary));
    expect(submissions(view.daemon)).toEqual([{ document_uri: SEED_URI, target_seed_id: 's-plan11', orphaned_ids: [] }]);
  });

  it('flips the primary live across park and claim pushes without waiting for detail reads', async () => {
    const first = planDocument(planSeed());
    const documents = [first];
    const view = await openSeedTile(seedTile('s-plan11'), [first.seed], () => documents.shift());
    expect(screen.getByRole('button', { name: 'Send 1' })).toBeInTheDocument();

    await view.push([planSeed({ tender_session: '', tender_member: '', rev: 2 })]);
    expect(screen.getByRole('button', { name: 'Note on seed 1' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'More annotation destinations' })).toBeNull();

    await view.push([planSeed({ tender_session: 'sess-b', tender_member: 'trellis', rev: 3 })]);
    const primary = screen.getByRole('button', { name: 'Send 1' });
    expect(screen.getByRole('button', { name: 'More annotation destinations' })).toBeInTheDocument();
    await gesture(view.daemon, () => fireEvent.click(primary));
    expect(submissions(view.daemon)).toEqual([{ document_uri: SEED_URI, target_session_id: 'sess-b', orphaned_ids: [] }]);
  });

  it('re-anchors a persisted highlight from the pushed body without remounting or accepting a stale detail body', async () => {
    const oldBody = 'First paragraph with target words inside it.\n';
    const newBody = '# New introduction\n\n' + oldBody;
    const first = planDocument(planSeed({ body: oldBody }));
    const documents = [first];
    const view = await openSeedTile(seedTile('s-plan11'), [first.seed], () => documents.shift(), {
      seeded: [anchoredNote(oldBody, 'target words')],
    });
    const tile = tileElement('tile-seed-s-plan11');
    expect(tile.querySelector('[data-md-mark="stored-1"]')).toHaveTextContent('target words');
    const scrollNode = tile.querySelector<HTMLElement>('.md-reader-doc')!;
    scrollNode.scrollTop = 137;

    await view.push([planSeed({ body: newBody, rev: 2 })]);
    expect(within(tile).getByRole('heading', { name: 'New introduction' })).toBeInTheDocument();
    expect(tile.querySelector('.md-reader-doc')).toBe(scrollNode);
    expect(scrollNode.scrollTop).toBe(137);
    expect(tile.querySelector('[data-md-mark="stored-1"]')).toHaveTextContent('target words');
    expect(tile.querySelector('.md-card-orphan-badge')).toBeNull();
    expect(within(tile).queryByText('⚠ moved')).toBeNull();
    expect(view.reads()).toHaveLength(2);

    await view.deliver(view.reads()[1], { ...first, notes_total: 2 });
    expect(within(tile).getByRole('heading', { name: 'New introduction' })).toBeInTheDocument();
  });

  it('names an unknown seed read failure in the tile', async () => {
    await openSeedTile(seedTile('s-missing'), [], () => new Error('no seed s-missing is planted here'));

    expect(inTile('tile-seed-s-missing').getByText('no seed s-missing is planted here')).toBeInTheDocument();
  });
});
