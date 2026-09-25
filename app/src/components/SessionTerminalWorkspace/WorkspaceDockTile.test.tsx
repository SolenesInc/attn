import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen } from '@testing-library/react';
import { EditorView } from '@codemirror/view';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { open } from '@tauri-apps/plugin-dialog';
import { openUrl } from '@tauri-apps/plugin-opener';
import { resolveMarkdownTarget } from '../MarkdownReader/markdownLinks';
import { normalizeBrowserAddress } from './browserAddress';
import { WorkspaceDockTile } from './WorkspaceDockTile';
import type { WorkspaceTileSessionOption } from './WorkspaceDockTile';
import { deriveTileTitle } from '../../utils/tilePresentation';
import { serializeNotebookTileParams, type TileLeaf } from '../../types/workspace';
import { NotebookSurfaceProvider } from '../../contexts/NotebookSurfaceContext';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { useAppNotebookSurface } from '../../application/useAppNotebookSurface';
import type { WireAnnotation } from '../MarkdownReader/annotations/types';
import { annotationToWire } from '../MarkdownReader/annotations/types';
import { createAnchor, extractBlockTexts } from '../MarkdownReader/anchoring';
import { renderWithDaemon } from '../../test/renderApp';
import type { CommandMessage } from '../../test/protocol';
import type { Reply, ScriptedDaemon } from '../../test/scriptedDaemon';
import type { Seed } from '../../types/generated';
import type { SeedDocument } from '../SeedDocumentView';

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

function DaemonNotebookSurface({ children }: { children: ReactNode }) {
  const api = useDaemonApi();
  const { notebookSurfaceContextValue } = useAppNotebookSurface({
    ...api,
    fsChangeSignals: {},
    effectiveNotebookRoot: '/notebook-root',
  });
  return <NotebookSurfaceProvider value={notebookSurfaceContextValue}>{children}</NotebookSurfaceProvider>;
}

async function renderTile(tile: ReactElement, script: (daemon: ScriptedDaemon) => void = () => {}) {
  const view = await renderWithDaemon();
  script(view.daemon);
  const surfaced = (ui: ReactElement) => <DaemonNotebookSurface>{ui}</DaemonNotebookSurface>;
  view.rerender(surfaced(tile));
  await view.daemon.idle();
  return {
    ...view,
    rerender: async (ui: ReactElement) => {
      view.rerender(surfaced(ui));
      await view.daemon.idle();
    },
    settle: () => view.daemon.idle(),
  };
}

const invokeMock = vi.mocked(invoke);

function renderMarkdown(content: string, allowLocalTargets = true) {
  return renderTile(
    <WorkspaceDockTile
      tile={{ type: 'tile', tileId: 'tile-markdown', tileKind: 'markdown', tileParams: '/tmp/project/README.md' }}
      workspaceId="workspace-1"
      content={{ path: '/tmp/project/README.md', content }}
      allowLocalTargets={allowLocalTargets}
      dragging={false}
      onClose={vi.fn()}
      onHeaderPointerDown={vi.fn()}
      onRequestContent={vi.fn()}
    />,
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
    const { container } = await renderMarkdown('![tracking](https://example.test/pixel?id=123)');

    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('[blocked image: tracking]')).toBeInTheDocument();
    expect(openUrl).not.toHaveBeenCalled();
  });

  it('renders relative local images inline via the asset protocol', async () => {
    const { container } = await renderMarkdown('![diagram](docs/diagram.png)');

    const img = container.querySelector('img.md-reader-image');
    expect(img).toHaveAttribute('src', 'asset://localhost//tmp/project/docs/diagram.png');
    expect(invokeMock).not.toHaveBeenCalledWith('open_safe_markdown_target', expect.anything());
  });

  it('opens relative and external links through the Tauri opener', async () => {
    await renderMarkdown('[guide](docs/setup.md) [site](https://example.test/docs)');

    fireEvent.click(screen.getByRole('link', { name: 'guide' }));
    expect(invokeMock).toHaveBeenCalledWith('open_safe_markdown_target', {
      path: '/tmp/project/docs/setup.md',
    });

    fireEvent.click(screen.getByRole('link', { name: 'site' }));
    expect(openUrl).toHaveBeenCalledWith('https://example.test/docs');
  });

  it('disables local targets for remote workspace content', async () => {
    await renderMarkdown('[guide](docs/setup.md) ![diagram](docs/diagram.png) [site](https://example.test/docs)', false);

    expect(screen.queryByRole('link', { name: 'guide' })).toBeNull();
    expect(screen.getByText('[blocked image: diagram]')).toBeInTheDocument();
    expect(document.querySelector('img.md-reader-image')).toBeNull();

    fireEvent.click(screen.getByRole('link', { name: 'site' }));
    expect(invokeMock).not.toHaveBeenCalledWith('open_safe_markdown_target', expect.anything());
    expect(openUrl).toHaveBeenCalledWith('https://example.test/docs');
  });

  it('blocks executable-associated local targets from repository Markdown', async () => {
    await renderMarkdown('[guide](scripts/setup.command) ![diagram](scripts/setup.command)');

    expect(screen.queryByRole('link', { name: 'guide' })).toBeNull();
    expect(screen.getByText('guide')).toHaveAttribute(
      'title',
      'Blocked local target: /tmp/project/scripts/setup.command',
    );
    expect(screen.getByText('[blocked image: diagram]')).toBeInTheDocument();
    expect(invokeMock).not.toHaveBeenCalledWith('open_safe_markdown_target', expect.anything());
  });

  it('adds duplicate-safe heading ids for fragment links', async () => {
    await renderMarkdown('[Jump](#setup)\n\n## Setup\n\n## Setup');

    expect(screen.getByRole('link', { name: 'Jump' })).toHaveAttribute('href', '#setup');
    expect(screen.getAllByRole('heading', { name: 'Setup' }).map((heading) => heading.id)).toEqual([
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
    const { settle } = await renderMarkdown('```mermaid\ngraph TD;\nA-->B;\n```');
    await rendered;
    await settle();

    expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
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

function browserTile(overrides: Partial<Parameters<typeof WorkspaceDockTile>[0]> = {}) {
  return (
    <WorkspaceDockTile
      tile={{
        type: 'tile',
        tileId: 'tile-browser',
        tileKind: 'browser',
        tileParams: 'https://backstage.spotify.net',
      }}
      workspaceId="workspace-1"
      dragging={false}
      onClose={vi.fn()}
      onHeaderPointerDown={vi.fn()}
      onRequestContent={vi.fn()}
      {...overrides}
    />
  );
}

describe('WorkspaceDockTile browser integration', () => {
  beforeEach(() => {
    invokeMock.mockResolvedValue(undefined);
  });

  it('closes the exact browser tile targeted by the native close command', async () => {
    const onClose = vi.fn();
    await renderTile(browserTile({ onClose }));

    expect(screen.getByText('Error: In-app browser hosting requires the Tauri app')).toBeInTheDocument();
    act(() => {
      window.dispatchEvent(new CustomEvent('attn:native-browser-close', {
        detail: 'browser-workspace-1-tile-browser',
      }));
    });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('reloads the browser from its tile header', async () => {
    await renderTile(browserTile());

    expect(screen.getByText('Error: In-app browser hosting requires the Tauri app')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Reload browser' }));

    expect(invokeMock).toHaveBeenCalledWith('browser_host_control', {
      label: 'browser-workspace-1-tile-browser',
      action: 'reload',
      params: undefined,
      selector: undefined,
      text: undefined,
    });
  });

  it('claims browser close ownership from header controls', async () => {
    const view = await renderTile(<></>);
    vi.mocked(isTauri).mockReturnValue(true);
    await view.rerender(browserTile());

    fireEvent.pointerDown(screen.getByRole('textbox', { name: 'Browser address' }));

    expect(invokeMock).toHaveBeenCalledWith('browser_host_claim_focus', {
      label: 'browser-workspace-1-tile-browser',
    });
  });

  it('navigates from the address bar and tracks native location changes', async () => {
    const onUpdateParams = vi.fn(async () => {});
    const view = await renderTile(browserTile({ onUpdateParams }));
    const address = screen.getByRole('textbox', { name: 'Browser address' });

    fireEvent.change(address, { target: { value: 'example.com/docs' } });
    fireEvent.submit(address.closest('form')!);
    await view.settle();

    expect(invokeMock).toHaveBeenCalledWith('browser_host_control', {
      label: 'browser-workspace-1-tile-browser',
      action: 'navigate',
      params: JSON.stringify({ url: 'https://example.com/docs' }),
      selector: undefined,
      text: undefined,
    });

    act(() => {
      window.dispatchEvent(new CustomEvent('attn:browser-location', {
        detail: {
          label: 'browser-workspace-1-tile-browser',
          url: 'https://example.com/redirected',
        },
      }));
    });
    await view.settle();

    expect(address).toHaveValue('https://example.com/redirected');
    expect(onUpdateParams).toHaveBeenCalledWith('https://example.com/redirected');

    act(() => {
      window.dispatchEvent(new CustomEvent('attn:browser-location', {
        detail: {
          label: 'browser-workspace-1-tile-browser',
          url: 'https://example.com/redirected',
        },
      }));
    });
    expect(onUpdateParams).toHaveBeenCalledTimes(1);
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

function serveNotebook(daemon: ScriptedDaemon, write: WriteOutcome = 'saved') {
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
  const view = EditorView.findFromDOM(document.querySelector<HTMLElement>('.cm-content')!)!;
  act(() => view.dispatch({ changes: { from: view.state.doc.length, insert: text } }));
}

describe('WorkspaceDockTile notebook root switcher', () => {
  beforeEach(() => {
    vi.mocked(open).mockReset();
  });

  async function renderNotebookTile(opts: {
    tileParams?: string;
    workspaceDirectory?: string;
    write?: WriteOutcome;
  } = {}) {
    const onUpdateParams = vi.fn(async (_tileParams: string) => {});
    const view = await renderTile(
      <WorkspaceDockTile
        tile={{ type: 'tile', tileId: 'tile-notebook', tileKind: 'notebook', tileParams: opts.tileParams }}
        workspaceId="workspace-1"
        workspaceDirectory={opts.workspaceDirectory}
        dragging={false}
        onClose={vi.fn()}
        onUpdateParams={onUpdateParams}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
      (daemon) => serveNotebook(daemon, opts.write),
    );
    onUpdateParams.mockClear();
    return { ...view, onUpdateParams };
  }

  function picker() {
    return screen.getByRole('combobox', { name: 'Editor root' });
  }

  const openNote = serializeNotebookTileParams({ root: undefined, path: 'notes.md' });
  const workspaceRoot = serializeNotebookTileParams({ root: '/Users/victor/code/attn' });

  it('offers Notebook and Workspace options for a rootless tile with a distinct workspace directory', async () => {
    await renderNotebookTile({ workspaceDirectory: '/Users/victor/code/attn' });

    const options = Array.from(picker().querySelectorAll('option')).map((option) => option.textContent);
    expect(options).toEqual(['Notebook', 'Workspace — attn', 'Browse…']);
    expect(picker()).toHaveValue('');
  });

  it('adds the current root as its own option when it matches neither the notebook root nor the workspace directory', async () => {
    await renderNotebookTile({
      tileParams: serializeNotebookTileParams({ root: '/tmp/some-other-root' }),
      workspaceDirectory: '/Users/victor/code/attn',
    });

    const options = Array.from(picker().querySelectorAll('option')).map((option) => option.textContent);
    expect(options).toEqual(['Notebook', 'Workspace — attn', 'some-other-root', 'Browse…']);
    expect(picker()).toHaveValue('/tmp/some-other-root');
  });

  it('omits the Workspace option when no workspace directory is set', async () => {
    await renderNotebookTile({});

    const options = Array.from(picker().querySelectorAll('option')).map((option) => option.textContent);
    expect(options).toEqual(['Notebook', 'Browse…']);
  });

  it('selecting Notebook writes rootless params without the open path', async () => {
    const { onUpdateParams, settle } = await renderNotebookTile({
      tileParams: serializeNotebookTileParams({ root: '/tmp/some-other-root', path: 'notes.md' }),
      workspaceDirectory: '/Users/victor/code/attn',
    });

    fireEvent.change(picker(), { target: { value: '' } });
    await settle();

    expect(onUpdateParams).toHaveBeenCalledWith(serializeNotebookTileParams({ root: undefined }));
  });

  it('selecting the workspace directory writes it as the root without the open path', async () => {
    const { onUpdateParams, settle } = await renderNotebookTile({ tileParams: openNote, workspaceDirectory: '/Users/victor/code/attn' });

    fireEvent.change(picker(), { target: { value: '/Users/victor/code/attn' } });
    await settle();

    expect(onUpdateParams).toHaveBeenCalledWith(workspaceRoot);
  });

  it('Browse… persists the chosen directory as the root', async () => {
    vi.mocked(open).mockResolvedValue('/tmp/chosen-root');
    const { onUpdateParams, settle } = await renderNotebookTile({ workspaceDirectory: '/Users/victor/code/attn' });

    fireEvent.change(picker(), { target: { value: '__browse__' } });
    await settle();

    expect(onUpdateParams).toHaveBeenCalledWith(serializeNotebookTileParams({ root: '/tmp/chosen-root' }));
    expect(open).toHaveBeenCalledWith({ directory: true, multiple: false, title: 'Choose editor root' });
  });

  it('Browse… cancelled leaves the tile params untouched', async () => {
    vi.mocked(open).mockResolvedValue(null);
    const { onUpdateParams, settle } = await renderNotebookTile({ workspaceDirectory: '/Users/victor/code/attn' });

    fireEvent.change(picker(), { target: { value: '__browse__' } });
    await settle();

    expect(open).toHaveBeenCalled();
    expect(onUpdateParams).not.toHaveBeenCalled();
  });

  it('saves the unsaved edit before switching the root', async () => {
    const { daemon, onUpdateParams, settle } = await renderNotebookTile({ tileParams: openNote, workspaceDirectory: '/Users/victor/code/attn' });
    const order: string[] = [];
    onUpdateParams.mockImplementation(async (params) => { order.push(`params:${params}`); });
    daemon.on('fs_write', ({ path, content }) => {
      order.push(`write:${content}`);
      return { event: 'fs_write_result', success: true, result: { path, hash: 'h2', conflict: false } };
    });

    typeInEditor(' world');
    fireEvent.change(picker(), { target: { value: '/Users/victor/code/attn' } });
    await settle();

    expect(order).toEqual(['write:hello world', `params:${workspaceRoot}`]);
  });

  it('keeps the root when saving the unsaved edit conflicts', async () => {
    const { daemon, onUpdateParams, settle } = await renderNotebookTile({ tileParams: openNote, workspaceDirectory: '/Users/victor/code/attn', write: 'conflict' });

    typeInEditor(' world');
    fireEvent.change(picker(), { target: { value: '/Users/victor/code/attn' } });
    await settle();

    expect(daemon.sentOf('fs_write')).toEqual([expect.objectContaining({ path: 'notes.md', content: 'hello world' })]);
    expect(onUpdateParams).not.toHaveBeenCalled();
  });

  it('keeps the root when Browse… picks a directory but saving the unsaved edit fails', async () => {
    vi.mocked(open).mockResolvedValue('/tmp/chosen-root');
    const { daemon, onUpdateParams, settle } = await renderNotebookTile({ tileParams: openNote, workspaceDirectory: '/Users/victor/code/attn', write: 'error' });

    typeInEditor(' world');
    fireEvent.change(picker(), { target: { value: '__browse__' } });
    await settle();

    expect(daemon.sentOf('fs_write')).toHaveLength(1);
    expect(onUpdateParams).not.toHaveBeenCalled();
  });
});

const SEND_PATH = '/tmp/project/README.md';
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

function serveAnnotations(
  daemon: ScriptedDaemon,
  { seeded = [globalNote()], submit = delivered, hydrate = true }: { seeded?: WireAnnotation[]; submit?: SubmitAnswer; hydrate?: boolean } = {},
) {
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

const SEND_SESSIONS: WorkspaceTileSessionOption[] = [
  { sessionId: 'sess-a', label: 'alpha', state: 'working' },
  { sessionId: 'sess-b', label: 'beta', state: 'pending_approval' },
];

function sendTile(tileSessionId: string | undefined): TileLeaf {
  return {
    type: 'tile',
    tileId: 'tile-md',
    tileKind: 'markdown',
    tileParams: SEND_PATH,
    ...(tileSessionId !== undefined ? { tileSessionId } : {}),
  };
}

async function renderSendTile({
  tileSessionId = 'sess-a',
  sessions = SEND_SESSIONS,
  onRetargetTile = vi.fn(),
  annotations = {},
}: {
  tileSessionId?: string;
  sessions?: WorkspaceTileSessionOption[];
  onRetargetTile?: (sessionId: string) => Promise<unknown> | void;
  annotations?: Parameters<typeof serveAnnotations>[1];
} = {}) {
  const props = {
    workspaceId: 'workspace-1',
    content: { path: SEND_PATH, content: SEND_DOC },
    dragging: false,
    workspaceSessions: sessions,
    onClose: vi.fn(),
    onRetargetTile,
    onHeaderPointerDown: vi.fn(),
    onRequestContent: vi.fn(),
  };
  const view = await renderTile(
    <WorkspaceDockTile tile={sendTile(tileSessionId)} {...props} />,
    (daemon) => serveAnnotations(daemon, annotations),
  );
  return {
    ...view,
    onRetargetTile,
    rebind: (nextSessionId: string) => view.rerender(<WorkspaceDockTile tile={sendTile(nextSessionId)} {...props} />),
  };
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
    await renderSendTile();

    expect(sendButton()).toHaveAccessibleName('Send 1 to alpha');
    expect(sendButton()).toBeEnabled();
    openSessionDestinations();
    expect(screen.getByRole('menuitemradio', { name: 'alpha' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('menuitemradio', { name: 'beta approval' })).toHaveAttribute('aria-checked', 'false');
  });

  it('navigates destination choices with arrows and returns focus on Escape', async () => {
    const { settle } = await renderSendTile();
    expect(sendButton()).toBeEnabled();

    const caret = screen.getByRole('button', { name: 'Change annotation destination' });
    fireEvent.click(caret);
    await settle();
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
    const onRetargetTile = vi.fn(async () => {});
    const { rebind } = await renderSendTile({ onRetargetTile });
    expect(sendButton()).toHaveAccessibleName('Send 1 to alpha');

    openSessionDestinations();
    chooseSession('beta approval');
    expect(onRetargetTile).toHaveBeenCalledWith('sess-b');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
    await rebind('sess-b');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
  });

  it('a retarget takes effect immediately for Send without waiting for a broadcast (E13)', async () => {
    const onRetargetTile = vi.fn(async () => {});
    const { daemon, rebind, settle } = await renderSendTile({ onRetargetTile });
    expect(sendButton()).toBeEnabled();

    openSessionDestinations();
    chooseSession('beta approval');
    expect(onRetargetTile).toHaveBeenCalledWith('sess-b');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
    fireEvent.click(sendButton());
    await settle();
    expect(submissions(daemon)).toEqual([{ document_uri: SEND_URI, target_session_id: 'sess-b', orphaned_ids: [] }]);

    await rebind('sess-b');
    openSessionDestinations();
    expect(screen.getByRole('menuitemradio', { name: 'beta approval' })).toHaveAttribute('aria-checked', 'true');
  });

  it('rolls Send back to the persisted binding when retargeting fails (E13)', async () => {
    const onRetargetTile = vi.fn(async () => {
      throw new Error('retarget rejected');
    });
    const { settle } = await renderSendTile({ onRetargetTile });
    expect(sendButton()).toBeEnabled();

    openSessionDestinations();
    chooseSession('beta approval');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
    await settle();
    expect(sendButton()).toHaveAccessibleName('Send 1 to alpha');
  });

  it('shows a disabled Send with No session when the bound session left the workspace (E13)', async () => {
    await renderSendTile({ tileSessionId: 'sess-gone' });

    expect(sendButton()).toHaveTextContent('Send 1');
    expect(sendButton()).toHaveTextContent('No session');
    expect(sendButton()).toBeDisabled();
  });

  it('replaces Send 0 with the current destination (E14)', async () => {
    await renderSendTile({ annotations: { seeded: [] } });

    expect(screen.getByRole('button', { name: 'Annotation destination: alpha' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: /Send 0/ })).toBeNull();
  });

  it('opens overall notes and the floating review inspector from the tile header', async () => {
    const { daemon, settle } = await renderSendTile({ annotations: { seeded: [] } });
    expect(screen.getByRole('button', { name: 'Annotation destination: alpha' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Overall note' }));
    fireEvent.change(screen.getByPlaceholderText('Add an overall note...'), {
      target: { value: 'First overall note' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    fireEvent.click(screen.getByRole('button', { name: 'Overall note' }));
    fireEvent.change(screen.getByPlaceholderText('Add an overall note...'), {
      target: { value: 'Second overall note' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    expect(screen.getByRole('button', { name: 'Notes 2' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Notes 2' }));
    expect(screen.getByRole('dialog', { name: 'Review notes' })).toBeInTheDocument();
    expect(screen.getByText('First overall note')).toBeInTheDocument();
    expect(screen.getByText('Second overall note')).toBeInTheDocument();

    fireEvent.click(sendButton());
    await settle();
    const saves = daemon.sentOf('markdown_annotations_save');
    expect(saves.length).toBeGreaterThan(0);
    expect(saves[0]).toMatchObject({ document_uri: SEND_URI });
    expect(saves[saves.length - 1].annotations.filter((annotation) => annotation.type === 'global')).toHaveLength(2);
  });

  it('delivers: Sending… → Sent ✓, list empties locally without re-fetch or second clear (E14)', async () => {
    const held: Submit[] = [];
    const { daemon, settle } = await renderSendTile({ annotations: { submit: (command) => void held.push(command) } });
    expect(sendButton()).toBeEnabled();

    fireEvent.click(sendButton());
    expect(sendButton()).toHaveTextContent('Sending…');
    await settle();
    expect(submissions(daemon)).toEqual([{ document_uri: SEND_URI, target_session_id: 'sess-a', orphaned_ids: [] }]);

    daemon.replyTo(held[0], { ...delivered(held[0])!, request_id: held[0].request_id, generation: 9 } as Reply);
    await settle();
    expect(screen.getByRole('status')).toHaveTextContent('Sent ✓');
    expect(sendButton()).toHaveTextContent('Sent ✓');
    expect(sendButton()).toBeDisabled();
    expect(daemon.sentOf('markdown_annotations_get')).toHaveLength(1);
    expect(daemon.sentOf('markdown_annotations_clear')).toEqual([]);
  });

  it('delivered-but-clear-failed keeps annotations and surfaces the warning, not Sent ✓ (E14)', async () => {
    const { settle } = await renderSendTile({
      annotations: { submit: answered({ success: true, status: 'delivered', error: 'delivered; failed to clear drafts: disk full' }) },
    });
    expect(sendButton()).toBeEnabled();

    fireEvent.click(sendButton());
    await settle();

    expect(screen.getByRole('status')).toHaveTextContent('failed to clear drafts');
    expect(sendButton()).toHaveTextContent('Needs attention');
    expect(sendButton()).toBeEnabled();
    expect(screen.queryByText('Sent ✓')).toBeNull();
  });

  it('refuses to Send while the draft is not hydrated (stale-draft guard, E14)', async () => {
    const { daemon, settle } = await renderSendTile({ annotations: { hydrate: false } });

    fireEvent.click(screen.getByRole('button', { name: 'Overall note' }));
    fireEvent.change(screen.getByPlaceholderText('Add an overall note...'), {
      target: { value: 'unsaved local note' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    await settle();
    expect(sendButton()).toHaveTextContent('Send 1');

    fireEvent.click(sendButton());
    await settle();
    expect(screen.getByRole('status')).toHaveTextContent('still syncing');
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);
    expect(sendButton()).toHaveTextContent('Send failed');
  });

  it('keeps annotations and explains when the target is waiting on approval (E15)', async () => {
    const { settle } = await renderSendTile({ annotations: { submit: answered({ success: false, status: 'skipped_pending_approval' }) } });
    expect(sendButton()).toBeEnabled();

    fireEvent.click(sendButton());
    await settle();

    expect(screen.getByRole('status')).toHaveTextContent('Target is waiting for approval — not sent');
    expect(sendButton()).toHaveTextContent('Approval needed');
    expect(sendButton()).toBeEnabled();
  });

  it('keeps annotations and surfaces the message on a rejected submit (E15)', async () => {
    const { settle } = await renderSendTile({ annotations: { submit: answered({ success: false, status: '', error: 'session not found' }) } });
    expect(sendButton()).toBeEnabled();

    fireEvent.click(sendButton());
    await settle();

    expect(screen.getByRole('status')).toHaveTextContent('session not found');
    expect(sendButton()).toHaveTextContent('Send failed');
  });

  it('⌘Enter sends when focus is inside the tile and annotations exist (E18)', async () => {
    const { daemon, container, settle } = await renderSendTile();
    expect(sendButton()).toBeEnabled();

    const body = container.querySelector<HTMLElement>('.workspace-dock-tile-body')!;
    fireEvent.focusIn(body);
    const event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(true);
    await settle();
    expect(submissions(daemon)).toEqual([{ document_uri: SEND_URI, target_session_id: 'sess-a', orphaned_ids: [] }]);
  });

  it('⌘Enter never fires from a textarea or without tile focus (E18)', async () => {
    const { daemon, container, settle } = await renderSendTile();
    expect(sendButton()).toBeEnabled();

    let event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(false);

    const body = container.querySelector<HTMLElement>('.workspace-dock-tile-body')!;
    const textarea = document.createElement('textarea');
    body.appendChild(textarea);
    fireEvent.focusIn(textarea);
    event = pressCmdEnter(textarea);
    expect(event.defaultPrevented).toBe(false);
    await settle();
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);
  });

  it('⌘Enter does nothing when there is nothing to send (E18)', async () => {
    const { daemon, container, settle } = await renderSendTile({ annotations: { seeded: [] } });
    expect(screen.getByRole('button', { name: 'Annotation destination: alpha' })).toBeInTheDocument();

    fireEvent.focusIn(container.querySelector<HTMLElement>('.workspace-dock-tile-body')!);
    const event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(false);
    await settle();
    expect(daemon.sentOf('markdown_annotations_submit')).toEqual([]);
  });
});

function seedFixture(overrides: Partial<Seed> = {}): Seed {
  return {
    id: 's-plan11',
    title: 'Seed reader plan',
    body: '# Seed body\n\nAnnotate this plan.',
    status: 'growing',
    state_changed_at: '2026-08-15T08:00:00Z',
    state_changed_at_exact: true,
    step_slug: 'seed-reader-plan',
    planter_session: '',
    planter_member: '',
    tender_session: 'sess-a',
    tender_member: 'trellis',
    edges: [],
    template: false,
    gate: false,
    vars: [],
    ready: false,
    rev: 1,
    created_at: '2026-08-15T08:00:00Z',
    updated_at: '2026-08-15T08:00:00Z',
    ...overrides,
  };
}

function seedDocumentFixture(body = '# Seed body\n\nAnnotate this plan.'): SeedDocument {
  return {
    seed: seedFixture({
      body,
      plot_progress: { total: 1, done: 1, withered: 0, growing: 0, dormant: 0, ready: 0, blocked: 0 },
    }),
    tender_holds: true,
    children: [seedFixture({ id: 's-child1', title: 'Reader child', body: '', status: 'harvested' })],
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
    artifacts: [],
    references: [],
  };
}

type SeedRead = CommandMessage<'seed_document_get'>;
const SEED_URI = 'attn://seed/s-plan11';

async function renderSeedTile(
  tile: ReactElement,
  answer: (seedId: string) => SeedDocument | Error | undefined,
  annotations: Parameters<typeof serveAnnotations>[1] = {},
) {
  const view = await renderTile(tile, (daemon) => {
    serveAnnotations(daemon, annotations);
    daemon.on('seed_document_get', ({ seed_id }) => {
      const reply = answer(seed_id);
      if (reply === undefined) return;
      return reply instanceof Error
        ? { event: 'seed_document_get_result', success: false, error: reply.message }
        : { event: 'seed_document_get_result', success: true, document: reply };
    });
  });
  return {
    ...view,
    reads: () => view.daemon.sentOf('seed_document_get'),
    annotationReads: () => view.daemon.sentOf('markdown_annotations_get').map(({ document_uri }) => document_uri),
    deliver: async (read: SeedRead, document: SeedDocument) => {
      view.daemon.replyTo(read, { event: 'seed_document_get_result', request_id: read.request_id, success: true, document });
      await view.daemon.idle();
    },
  };
}

const noted = answered({ success: true, status: 'noted', generation: 8 });

describe('WorkspaceDockTile seed reader', () => {
  it('loads the seed document, renders its plot and collapsed log, annotates by seed URI, and refetches on a garden push', async () => {
    const first = seedDocumentFixture();
    const second = {
      ...seedDocumentFixture('# Updated seed body'),
      seed: seedFixture({ body: '# Updated seed body', rev: 2 }),
    };
    const documents = [first, second];
    const onRequestContent = vi.fn();
    const props = {
      tile: {
        type: 'tile' as const,
        tileId: 'tile-seed-s-plan11',
        tileKind: 'seed' as const,
        tileParams: 's-plan11',
        tileSessionId: 'sess-a',
      },
      workspaceId: 'workspace-1',
      dragging: false,
      workspaceSessions: SEND_SESSIONS,
      gardenSeeds: [first.seed],
      onClose: vi.fn(),
      onHeaderPointerDown: vi.fn(),
      onRequestContent,
    };
    const view = await renderSeedTile(<WorkspaceDockTile {...props} />, () => documents.shift());

    expect(screen.getByRole('heading', { name: 'Seed body' })).toBeInTheDocument();
    expect(screen.getByText('Reader child')).toBeInTheDocument();
    expect(screen.getByText('Log').closest('details')).not.toHaveAttribute('open');
    fireEvent.click(screen.getByText('Log').closest('summary') as HTMLElement);
    expect(screen.getByText('Live ledger note')).toBeInTheDocument();
    expect(screen.getByText('Seed reader plan', { selector: '.workspace-dock-tile-title' })).toBeInTheDocument();
    expect(view.container.querySelector('.md-reader--annotating')).toBeInTheDocument();
    expect(onRequestContent).not.toHaveBeenCalled();
    expect(view.annotationReads()).toEqual([SEED_URI]);

    expect(sendButton()).toHaveTextContent('Send 1');
    fireEvent.click(sendButton());
    await view.settle();
    expect(submissions(view.daemon)).toEqual([{ document_uri: SEED_URI, target_session_id: 'sess-a', orphaned_ids: [] }]);

    await view.rerender(<WorkspaceDockTile {...props} gardenSeeds={[second.seed]} />);
    expect(screen.getByRole('heading', { name: 'Updated seed body' })).toBeInTheDocument();
    expect(view.reads()).toHaveLength(2);
  });

  it('navigates the plot in place, climbs canonical ancestry, and reveals the current seed in the Garden', async () => {
    const plot = seedFixture({
      id: 's-plot11',
      title: 'Reader polish',
      body: '# Plot plan',
      plot_progress: { total: 1, done: 0, withered: 0, growing: 1, dormant: 0, ready: 0, blocked: 0 },
    });
    const child = seedFixture({
      id: 's-child1',
      title: 'Polish the tile',
      body: '# Child body',
      edges: [{ kind: 'part-of', to: plot.id }],
    });
    const details = new Map<string, SeedDocument>([
      [plot.id, { ...seedDocumentFixture(plot.body), seed: plot, children: [child] }],
      [child.id, { ...seedDocumentFixture(child.body), seed: child, children: [] }],
    ]);
    const onUpdateParams = vi.fn().mockResolvedValue({});
    const onRevealSeedInGarden = vi.fn();

    const view = await renderSeedTile(
      <WorkspaceDockTile
        tile={{ type: 'tile', tileId: 'tile-seed-s-plot11', tileKind: 'seed', tileParams: plot.id }}
        workspaceId="workspace-1"
        dragging={false}
        gardenSeeds={[plot, child]}
        onClose={vi.fn()}
        onUpdateParams={onUpdateParams}
        onRevealSeedInGarden={onRevealSeedInGarden}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
      (seedId) => (seedId === child.id ? undefined : details.get(seedId)),
    );

    fireEvent.click(screen.getByRole('button', { name: /Polish the tile/ }));
    expect(onUpdateParams).toHaveBeenCalledWith(child.id);
    expect(screen.queryByRole('heading', { name: 'Plot plan' })).toBeNull();
    expect(screen.getByText('Loading seed…')).toBeInTheDocument();
    expect(screen.getByText(child.title, { selector: '.workspace-dock-tile-title' })).toBeInTheDocument();
    await view.settle();
    await view.deliver(view.reads().find((read) => read.seed_id === child.id)!, details.get(child.id)!);
    expect(screen.getByRole('heading', { name: 'Child body' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Back to Reader polish' })).toBeInTheDocument();
    expect(view.annotationReads()).toContain(`attn://seed/${child.id}`);

    fireEvent.click(screen.getByRole('button', { name: 'Reveal in Garden' }));
    expect(onRevealSeedInGarden).toHaveBeenCalledWith(child.id);

    fireEvent.click(screen.getByRole('button', { name: 'Back to Reader polish' }));
    expect(onUpdateParams).toHaveBeenLastCalledWith(plot.id);
    await view.settle();
    expect(screen.getByRole('heading', { name: 'Plot plan' })).toBeInTheDocument();
  });
  it('unwinds a recursive plot trail with Escape only while the seed tile owns focus', async () => {
    const root = seedFixture({
      id: 's-root11',
      title: 'Reader polish',
      body: '# Root plan',
      plot_progress: { total: 1, done: 0, withered: 0, growing: 1, dormant: 0, ready: 0, blocked: 0 },
    });
    const nested = seedFixture({
      id: 's-nest11',
      title: 'Nested polish',
      body: '# Nested plan',
      edges: [{ kind: 'part-of', to: root.id }],
      plot_progress: { total: 1, done: 0, withered: 0, growing: 1, dormant: 0, ready: 0, blocked: 0 },
    });
    const leaf = seedFixture({
      id: 's-leaf11',
      title: 'Leaf polish',
      body: '# Leaf plan',
      edges: [{ kind: 'part-of', to: nested.id }],
    });
    const details = new Map<string, SeedDocument>([
      [root.id, { ...seedDocumentFixture(root.body), seed: root, children: [nested] }],
      [nested.id, { ...seedDocumentFixture(nested.body), seed: nested, children: [leaf] }],
      [leaf.id, { ...seedDocumentFixture(leaf.body), seed: leaf, children: [] }],
    ]);
    const onUpdateParams = vi.fn().mockResolvedValue({});
    const view = await renderSeedTile(
      <WorkspaceDockTile
        tile={{ type: 'tile', tileId: 'tile-seed-s-leaf11', tileKind: 'seed', tileParams: leaf.id }}
        workspaceId="workspace-1"
        dragging={false}
        gardenSeeds={[root, nested, leaf]}
        onClose={vi.fn()}
        onUpdateParams={onUpdateParams}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
      (seedId) => details.get(seedId),
    );

    expect(screen.getByRole('heading', { name: 'Leaf plan' })).toBeInTheDocument();
    const unfocusedEscape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    fireEvent(window, unfocusedEscape);
    expect(unfocusedEscape.defaultPrevented).toBe(false);
    expect(onUpdateParams).not.toHaveBeenCalled();

    const body = view.container.querySelector<HTMLElement>('.workspace-dock-tile-body')!;
    fireEvent.focusIn(body);
    const firstEscape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    fireEvent(window, firstEscape);
    expect(firstEscape.defaultPrevented).toBe(true);
    expect(onUpdateParams).toHaveBeenLastCalledWith(nested.id);
    await view.settle();
    expect(screen.getByRole('heading', { name: 'Nested plan' })).toBeInTheDocument();

    const secondEscape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    fireEvent(window, secondEscape);
    expect(secondEscape.defaultPrevented).toBe(true);
    expect(onUpdateParams).toHaveBeenLastCalledWith(root.id);
    await view.settle();
    expect(screen.getByRole('heading', { name: 'Root plan' })).toBeInTheDocument();

    const rootEscape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    fireEvent(window, rootEscape);
    expect(rootEscape.defaultPrevented).toBe(false);
    expect(onUpdateParams).toHaveBeenCalledTimes(2);
  });

  it('hides the previous document while navigating outside a capped Garden snapshot', async () => {
    const plot = seedFixture({
      id: 's-plot12',
      title: 'Sparse plot',
      body: '# Previous body',
      plot_progress: { total: 1, done: 0, withered: 0, growing: 0, dormant: 0, ready: 1, blocked: 0 },
    });
    const child = seedFixture({ id: 's-child2', title: 'Outside the snapshot', body: '# Next body' });

    const view = await renderSeedTile(
      <WorkspaceDockTile
        tile={{ type: 'tile', tileId: 'tile-seed-s-plot12', tileKind: 'seed', tileParams: plot.id }}
        workspaceId="workspace-1"
        dragging={false}
        gardenSeeds={[]}
        onClose={vi.fn()}
        onUpdateParams={vi.fn().mockResolvedValue({})}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
      (seedId) => (seedId === child.id ? undefined : { ...seedDocumentFixture(plot.body), seed: plot, children: [child] }),
    );

    fireEvent.click(screen.getByRole('button', { name: /Outside the snapshot/ }));
    await view.settle();
    expect(screen.queryByRole('heading', { name: 'Previous body' })).toBeNull();
    expect(screen.getByText('Loading seed…')).toBeInTheDocument();
  });

  it('keeps the tended seed primary bound to its live tender and offers Note on seed in the caret menu', async () => {
    const detail = seedDocumentFixture();
    const view = await renderSeedTile(
      <WorkspaceDockTile
        tile={{
          type: 'tile', tileId: 'tile-seed-s-plan11', tileKind: 'seed',
          tileParams: 's-plan11', tileSessionId: 'sess-b',
        }}
        workspaceId="workspace-1"
        dragging={false}
        workspaceSessions={SEND_SESSIONS}
        gardenSeeds={[detail.seed]}
        onClose={vi.fn()}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
      () => detail,
      { submit: noted },
    );

    expect(screen.getByRole('button', { name: 'Send 1' })).toBeEnabled();
    expect(screen.queryByRole('combobox', { name: 'Send annotations to session' })).toBeNull();
    const caret = screen.getByRole('button', { name: 'More annotation destinations' });
    expect(caret).toHaveAttribute('aria-haspopup', 'menu');
    fireEvent.click(caret);
    fireEvent.click(screen.getByRole('menuitem', { name: 'Note on seed' }));
    await view.settle();

    expect(submissions(view.daemon)).toEqual([{ document_uri: SEED_URI, target_seed_id: 's-plan11', orphaned_ids: [] }]);
    expect(screen.getByRole('status')).toHaveTextContent('Noted ✓');
  });

  it('makes Note on seed the unsplit primary when nobody tends the seed', async () => {
    const detail = {
      ...seedDocumentFixture(),
      seed: seedFixture({ tender_session: '', tender_member: '' }),
      tender_holds: false,
    };
    const view = await renderSeedTile(
      <WorkspaceDockTile
        tile={{ type: 'tile', tileId: 'tile-seed-s-plan11', tileKind: 'seed', tileParams: 's-plan11' }}
        workspaceId="workspace-1"
        dragging={false}
        workspaceSessions={SEND_SESSIONS}
        gardenSeeds={[detail.seed]}
        onClose={vi.fn()}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
      () => detail,
      { submit: noted },
    );

    const primary = screen.getByRole('button', { name: 'Note on seed 1' });
    expect(primary).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'More annotation destinations' })).toBeNull();
    fireEvent.click(primary);
    await view.settle();
    expect(submissions(view.daemon)).toEqual([{ document_uri: SEED_URI, target_seed_id: 's-plan11', orphaned_ids: [] }]);
  });

  it('flips the primary live across park and claim pushes without waiting for detail reads', async () => {
    const first = seedDocumentFixture();
    const documents = [first];
    const props = {
      tile: { type: 'tile' as const, tileId: 'tile-seed-s-plan11', tileKind: 'seed' as const, tileParams: 's-plan11' },
      workspaceId: 'workspace-1',
      dragging: false,
      workspaceSessions: SEND_SESSIONS,
      onClose: vi.fn(),
      onHeaderPointerDown: vi.fn(),
      onRequestContent: vi.fn(),
    };
    const view = await renderSeedTile(<WorkspaceDockTile {...props} gardenSeeds={[first.seed]} />, () => documents.shift());
    expect(screen.getByRole('button', { name: 'Send 1' })).toBeInTheDocument();

    const parked = seedFixture({ tender_session: '', tender_member: '', rev: 2 });
    await view.rerender(<WorkspaceDockTile {...props} gardenSeeds={[parked]} />);
    expect(screen.getByRole('button', { name: 'Note on seed 1' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'More annotation destinations' })).toBeNull();

    const claimed = seedFixture({ tender_session: 'sess-b', tender_member: 'trellis', rev: 3 });
    await view.rerender(<WorkspaceDockTile {...props} gardenSeeds={[claimed]} />);
    const primary = screen.getByRole('button', { name: 'Send 1' });
    expect(screen.getByRole('button', { name: 'More annotation destinations' })).toBeInTheDocument();
    fireEvent.click(primary);
    await view.settle();
    expect(submissions(view.daemon)).toEqual([{ document_uri: SEED_URI, target_session_id: 'sess-b', orphaned_ids: [] }]);
  });

  it('re-anchors a persisted highlight from the pushed body without remounting or accepting a stale detail body', async () => {
    const oldBody = 'First paragraph with target words inside it.\n';
    const newBody = '# New introduction\n\n' + oldBody;
    const first = {
      ...seedDocumentFixture(oldBody),
      seed: seedFixture({ body: oldBody }),
    };
    const documents = [first];
    const props = {
      tile: { type: 'tile' as const, tileId: 'tile-seed-s-plan11', tileKind: 'seed' as const, tileParams: 's-plan11' },
      workspaceId: 'workspace-1',
      dragging: false,
      gardenSeeds: [first.seed],
      onClose: vi.fn(),
      onHeaderPointerDown: vi.fn(),
      onRequestContent: vi.fn(),
    };
    const view = await renderSeedTile(<WorkspaceDockTile {...props} />, () => documents.shift(), {
      seeded: [anchoredNote(oldBody, 'target words')],
    });
    const oldMark = view.container.querySelector('[data-md-mark="stored-1"]');
    expect(oldMark).toHaveTextContent('target words');
    const scrollNode = view.container.querySelector<HTMLElement>('.md-reader-doc')!;
    scrollNode.scrollTop = 137;

    const pushed = seedFixture({ body: newBody, rev: 2 });
    await view.rerender(<WorkspaceDockTile {...props} gardenSeeds={[pushed]} />);
    expect(screen.getByRole('heading', { name: 'New introduction' })).toBeInTheDocument();
    expect(view.container.querySelector('.md-reader-doc')).toBe(scrollNode);
    expect(scrollNode.scrollTop).toBe(137);
    expect(view.container.querySelector('[data-md-mark="stored-1"]')).toHaveTextContent('target words');
    expect(view.container.querySelector('.md-card-orphan-badge')).toBeNull();
    expect(screen.queryByText('⚠ moved')).toBeNull();
    expect(view.reads()).toHaveLength(2);

    await view.deliver(view.reads()[1], { ...first, notes_total: 2 });
    expect(screen.getByRole('heading', { name: 'New introduction' })).toBeInTheDocument();
  });

  it('names an unknown seed read failure in the tile', async () => {
    await renderSeedTile(
      <WorkspaceDockTile
        tile={{ type: 'tile', tileId: 'tile-seed-s-missing', tileKind: 'seed', tileParams: 's-missing' }}
        workspaceId="workspace-1"
        dragging={false}
        onClose={vi.fn()}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
      () => new Error('no seed s-missing is planted here'),
    );

    expect(screen.getByText('no seed s-missing is planted here')).toBeInTheDocument();
  });
});
