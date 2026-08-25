import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { open } from '@tauri-apps/plugin-dialog';
import { resolveMarkdownTarget } from '../MarkdownReader/markdownLinks';
import { normalizeBrowserAddress } from './browserAddress';
import { WorkspaceDockTile } from './WorkspaceDockTile';
import type { WorkspaceTileSessionOption } from './WorkspaceDockTile';
import { deriveTileTitle } from '../../utils/tilePresentation';
import { serializeNotebookTileParams, type TileLeaf } from '../../types/workspace';
import { NotebookSurfaceProvider, type NotebookSurfaceContextValue } from '../../contexts/NotebookSurfaceContext';
import { setMarkdownAnnotationsTransport } from '../MarkdownReader/annotations/transport';
import { fileMarkdownSource, seedMarkdownSource } from '../MarkdownReader/documentSource';
import type {
  MarkdownAnnotationsSubmitResult,
  MarkdownAnnotationsTransport,
} from '../MarkdownReader/annotations/transport';
import type { WireAnnotation } from '../MarkdownReader/annotations/types';
import { annotationToWire } from '../MarkdownReader/annotations/types';
import { createAnchor, extractBlockTexts } from '../MarkdownReader/anchoring';
import { DaemonApiProvider, type DaemonApi } from '../../contexts/DaemonApiContext';
import type { Seed } from '../../types/generated';
import type { SeedDocument } from '../SeedDocumentView';

vi.mock('@tauri-apps/plugin-dialog', () => ({ open: vi.fn() }));

const notebookSurfaceStub = vi.hoisted(() => ({
  flushPendingSave: vi.fn(async (): Promise<'saved' | 'conflict' | 'error' | 'noop'> => 'noop'),
}));
vi.mock('../NotebookSurface', async () => {
  const { forwardRef, useImperativeHandle } = await import('react');
  return {
    NotebookSurface: forwardRef(function MockNotebookSurface(_props: unknown, ref: React.Ref<{ flushPendingSave: () => Promise<string> }>) {
      useImperativeHandle(ref, () => ({
        flushPendingSave: notebookSurfaceStub.flushPendingSave,
      }), []);
      return <div data-testid="notebook-surface" />;
    }),
  };
});

// WorkspaceDockTile reads effectiveNotebookRoot unconditionally, so every render
// here needs the provider even for the markdown/browser tiles.
const testSurfaceValue: NotebookSurfaceContextValue = {
  makeDaemon: () => ({
    listDir: vi.fn(),
    readFile: vi.fn(),
    writeFile: vi.fn(),
    existsFile: vi.fn(),
    readAsset: vi.fn(),
    backlinksNotebook: vi.fn(),
    sendToChief: vi.fn(),
    listFiles: vi.fn(),
    changeSignal: 0,
  }),
  effectiveNotebookRoot: '/notebook-root',
  sendFsWatch: vi.fn().mockResolvedValue({ root: '' }),
  sendFsUnwatch: vi.fn().mockResolvedValue({ root: '' }),
  connectionGeneration: 0,
};

const defaultDaemonApi = {} as DaemonApi;

function NotebookSurfaceTestWrapper({
  api = defaultDaemonApi,
  children,
}: {
  api?: DaemonApi;
  children: ReactNode;
}) {
  return (
    <DaemonApiProvider api={api}>
      <NotebookSurfaceProvider value={testSurfaceValue}>{children}</NotebookSurfaceProvider>
    </DaemonApiProvider>
  );
}

function SeedTileTestWrapper({ api, children }: { api: DaemonApi; children: ReactNode }) {
  return <NotebookSurfaceTestWrapper api={api}>{children}</NotebookSurfaceTestWrapper>;
}

const opener = vi.hoisted(() => ({
  openUrl: vi.fn(async () => {}),
}));

vi.mock('@tauri-apps/plugin-opener', () => opener);
const invokeMock = vi.mocked(invoke);

// jsdom cannot run real mermaid (it needs a canvas/layout engine).
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

function renderMarkdown(content: string, allowLocalTargets = true) {
  return render(
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
    { wrapper: NotebookSurfaceTestWrapper },
  );
}

describe('WorkspaceDockTile Markdown rendering', () => {
  beforeEach(() => {
    invokeMock.mockReset();
    invokeMock.mockResolvedValue(undefined);
    vi.mocked(isTauri).mockReturnValue(false);
    opener.openUrl.mockClear();
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

  it('blocks automatic remote image loads', () => {
    const { container } = renderMarkdown('![tracking](https://example.test/pixel?id=123)');

    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('[blocked image: tracking]')).toBeInTheDocument();
    expect(opener.openUrl).not.toHaveBeenCalled();
  });

  it('renders relative local images inline via the asset protocol', () => {
    const { container } = renderMarkdown('![diagram](docs/diagram.png)');

    const img = container.querySelector('img.md-reader-image');
    expect(img).toHaveAttribute('src', 'asset://localhost//tmp/project/docs/diagram.png');
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it('opens relative and external links through the Tauri opener', () => {
    renderMarkdown('[guide](docs/setup.md) [site](https://example.test/docs)');

    fireEvent.click(screen.getByRole('link', { name: 'guide' }));
    expect(invokeMock).toHaveBeenCalledWith('open_safe_markdown_target', {
      path: '/tmp/project/docs/setup.md',
    });

    fireEvent.click(screen.getByRole('link', { name: 'site' }));
    expect(opener.openUrl).toHaveBeenCalledWith('https://example.test/docs');
  });

  it('disables local targets for remote workspace content', () => {
    renderMarkdown('[guide](docs/setup.md) ![diagram](docs/diagram.png) [site](https://example.test/docs)', false);

    expect(screen.queryByRole('link', { name: 'guide' })).toBeNull();
    expect(screen.getByText('[blocked image: diagram]')).toBeInTheDocument();
    expect(document.querySelector('img.md-reader-image')).toBeNull();

    fireEvent.click(screen.getByRole('link', { name: 'site' }));
    expect(invokeMock).not.toHaveBeenCalled();
    expect(opener.openUrl).toHaveBeenCalledWith('https://example.test/docs');
  });

  it('blocks executable-associated local targets from repository Markdown', () => {
    renderMarkdown('[guide](scripts/setup.command) ![diagram](scripts/setup.command)');

    expect(screen.queryByRole('link', { name: 'guide' })).toBeNull();
    expect(screen.getByText('guide')).toHaveAttribute(
      'title',
      'Blocked local target: /tmp/project/scripts/setup.command',
    );
    expect(screen.getByText('[blocked image: diagram]')).toBeInTheDocument();
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it('adds duplicate-safe heading ids for fragment links', () => {
    renderMarkdown('[Jump](#setup)\n\n## Setup\n\n## Setup');

    expect(screen.getByRole('link', { name: 'Jump' })).toHaveAttribute('href', '#setup');
    expect(screen.getAllByRole('heading', { name: 'Setup' }).map((heading) => heading.id)).toEqual([
      'setup',
      'setup-1',
    ]);
  });

  it('renders a mermaid fence as a diagram via the shared Markdown renderer', async () => {
    renderMarkdown('```mermaid\ngraph TD;\nA-->B;\n```');

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    expect(mermaidMock.render).toHaveBeenCalled();
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

describe('WorkspaceDockTile browser integration', () => {
  beforeEach(() => {
    invokeMock.mockReset();
    invokeMock.mockResolvedValue(undefined);
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  });

  it('closes the exact browser tile targeted by the native close command', async () => {
    const onClose = vi.fn();
    render(
      <WorkspaceDockTile
        tile={{
          type: 'tile',
          tileId: 'tile-browser',
          tileKind: 'browser',
          tileParams: 'https://backstage.spotify.net',
        }}
        workspaceId="workspace-1"
        dragging={false}
        onClose={onClose}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
      { wrapper: NotebookSurfaceTestWrapper },
    );

    await screen.findByText('Error: In-app browser hosting requires the Tauri app');
    act(() => {
      window.dispatchEvent(new CustomEvent('attn:native-browser-close', {
        detail: 'browser-workspace-1-tile-browser',
      }));
    });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('reloads the browser from its tile header', async () => {
    render(
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
      />,
      { wrapper: NotebookSurfaceTestWrapper },
    );

    await screen.findByText('Error: In-app browser hosting requires the Tauri app');
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
    vi.mocked(isTauri).mockReturnValue(true);
    render(
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
      />,
      { wrapper: NotebookSurfaceTestWrapper },
    );

    fireEvent.pointerDown(screen.getByRole('textbox', { name: 'Browser address' }));

    expect(invokeMock).toHaveBeenCalledWith('browser_host_claim_focus', {
      label: 'browser-workspace-1-tile-browser',
    });
  });

  it('navigates from the address bar and tracks native location changes', async () => {
    const onUpdateParams = vi.fn(async () => {});
    render(
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
        onUpdateParams={onUpdateParams}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
      { wrapper: NotebookSurfaceTestWrapper },
    );
    const address = screen.getByRole('textbox', { name: 'Browser address' });

    fireEvent.change(address, { target: { value: 'example.com/docs' } });
    fireEvent.submit(address.closest('form')!);

    await waitFor(() => {
      expect(invokeMock).toHaveBeenCalledWith('browser_host_control', {
        label: 'browser-workspace-1-tile-browser',
        action: 'navigate',
        params: JSON.stringify({ url: 'https://example.com/docs' }),
        selector: undefined,
        text: undefined,
      });
    });

    act(() => {
      window.dispatchEvent(new CustomEvent('attn:browser-location', {
        detail: {
          label: 'browser-workspace-1-tile-browser',
          url: 'https://example.com/redirected',
        },
      }));
    });

    await waitFor(() => {
      expect(address).toHaveValue('https://example.com/redirected');
      expect(onUpdateParams).toHaveBeenCalledWith('https://example.com/redirected');
    });

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

describe('WorkspaceDockTile notebook root switcher', () => {
  beforeEach(() => {
    vi.mocked(open).mockReset();
    notebookSurfaceStub.flushPendingSave.mockReset();
    notebookSurfaceStub.flushPendingSave.mockResolvedValue('noop');
  });

  function renderNotebookTile(opts: {
    tileParams?: string;
    workspaceDirectory?: string;
    onUpdateParams?: (tileParams: string) => Promise<unknown> | void;
  } = {}) {
    const onUpdateParams = opts.onUpdateParams ?? vi.fn(async () => {});
    const utils = render(
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
      { wrapper: NotebookSurfaceTestWrapper },
    );
    return { ...utils, onUpdateParams };
  }

  function picker() {
    return screen.getByRole('combobox', { name: 'Editor root' });
  }

  it('offers Notebook and Workspace options for a rootless tile with a distinct workspace directory', () => {
    renderNotebookTile({ workspaceDirectory: '/Users/victor/code/attn' });

    const options = Array.from(picker().querySelectorAll('option')).map((option) => option.textContent);
    expect(options).toEqual(['Notebook', 'Workspace — attn', 'Browse…']);
    expect(picker()).toHaveValue('');
  });

  it('adds the current root as its own option when it matches neither the notebook root nor the workspace directory', async () => {
    renderNotebookTile({
      tileParams: serializeNotebookTileParams({ root: '/tmp/some-other-root' }),
      workspaceDirectory: '/Users/victor/code/attn',
    });
    await act(async () => { await Promise.resolve(); });

    const options = Array.from(picker().querySelectorAll('option')).map((option) => option.textContent);
    expect(options).toEqual(['Notebook', 'Workspace — attn', 'some-other-root', 'Browse…']);
    expect(picker()).toHaveValue('/tmp/some-other-root');
  });

  it('omits the Workspace option when no workspace directory is set', () => {
    renderNotebookTile({});

    const options = Array.from(picker().querySelectorAll('option')).map((option) => option.textContent);
    expect(options).toEqual(['Notebook', 'Browse…']);
  });

  it('selecting Notebook writes rootless params without the open path', async () => {
    const { onUpdateParams } = renderNotebookTile({
      tileParams: serializeNotebookTileParams({ root: '/tmp/some-other-root', path: 'notes.md' }),
      workspaceDirectory: '/Users/victor/code/attn',
    });
    await act(async () => { await Promise.resolve(); });

    fireEvent.change(picker(), { target: { value: '' } });

    await waitFor(() => expect(onUpdateParams).toHaveBeenCalledWith(serializeNotebookTileParams({ root: undefined })));
  });

  it('selecting the workspace directory writes it as the root without the open path', async () => {
    const { onUpdateParams } = renderNotebookTile({
      tileParams: serializeNotebookTileParams({ root: undefined, path: 'notes.md' }),
      workspaceDirectory: '/Users/victor/code/attn',
    });

    fireEvent.change(picker(), { target: { value: '/Users/victor/code/attn' } });

    await waitFor(() => expect(onUpdateParams).toHaveBeenCalledWith(serializeNotebookTileParams({ root: '/Users/victor/code/attn' })));
  });

  it('Browse… persists the chosen directory as the root', async () => {
    vi.mocked(open).mockResolvedValue('/tmp/chosen-root');
    const { onUpdateParams } = renderNotebookTile({ workspaceDirectory: '/Users/victor/code/attn' });

    fireEvent.change(picker(), { target: { value: '__browse__' } });

    await waitFor(() => {
      expect(onUpdateParams).toHaveBeenCalledWith(serializeNotebookTileParams({ root: '/tmp/chosen-root' }));
    });
    expect(open).toHaveBeenCalledWith({ directory: true, multiple: false, title: 'Choose editor root' });
  });

  it('Browse… cancelled leaves the tile params untouched', async () => {
    vi.mocked(open).mockResolvedValue(null);
    const { onUpdateParams } = renderNotebookTile({ workspaceDirectory: '/Users/victor/code/attn' });

    fireEvent.change(picker(), { target: { value: '__browse__' } });

    await waitFor(() => expect(open).toHaveBeenCalled());
    expect(onUpdateParams).not.toHaveBeenCalled();
  });

  it('flushes the dirty buffer, then updates params, in that order, when flushPendingSave resolves "saved"', async () => {
    const callOrder: string[] = [];
    notebookSurfaceStub.flushPendingSave.mockImplementation(async () => {
      callOrder.push('flush');
      return 'saved';
    });
    const onUpdateParams = vi.fn(async (params: string) => {
      callOrder.push(`params:${params}`);
    });
    renderNotebookTile({ workspaceDirectory: '/Users/victor/code/attn', onUpdateParams });

    fireEvent.change(picker(), { target: { value: '/Users/victor/code/attn' } });

    await waitFor(() => expect(onUpdateParams).toHaveBeenCalled());
    expect(callOrder).toEqual([
      'flush',
      `params:${serializeNotebookTileParams({ root: '/Users/victor/code/attn' })}`,
    ]);
  });

  it('never calls onUpdateParams when flushPendingSave resolves "conflict"', async () => {
    notebookSurfaceStub.flushPendingSave.mockResolvedValue('conflict');
    const { onUpdateParams } = renderNotebookTile({ workspaceDirectory: '/Users/victor/code/attn' });

    fireEvent.change(picker(), { target: { value: '/Users/victor/code/attn' } });

    await waitFor(() => expect(notebookSurfaceStub.flushPendingSave).toHaveBeenCalled());
    await act(async () => { await Promise.resolve(); });
    expect(onUpdateParams).not.toHaveBeenCalled();
  });

  it('never calls onUpdateParams via Browse… when flushPendingSave resolves "error"', async () => {
    notebookSurfaceStub.flushPendingSave.mockResolvedValue('error');
    vi.mocked(open).mockResolvedValue('/tmp/chosen-root');
    const { onUpdateParams } = renderNotebookTile({ workspaceDirectory: '/Users/victor/code/attn' });

    fireEvent.change(picker(), { target: { value: '__browse__' } });

    await waitFor(() => expect(notebookSurfaceStub.flushPendingSave).toHaveBeenCalled());
    await act(async () => { await Promise.resolve(); });
    expect(onUpdateParams).not.toHaveBeenCalled();
  });
});

const SEND_PATH = '/tmp/project/README.md';
const SEND_DOC = 'First paragraph with target words inside it.\n';

/** A global (anchor-less) annotation — hydrates a count of 1 without any DOM
    anchor resolution, keeping these tests about the send flow, not anchoring. */
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

function makeSendTransport(seed: WireAnnotation[] = [globalNote()]) {
  const getSpy = vi.fn(async () => ({ annotations: seed, generation: 5 }));
  const saveSpy = vi.fn(async (_source: unknown, _annotations: WireAnnotation[], _generation: number) => ({ stale: false }));
  const clearSpy = vi.fn(async (_source: unknown, generation: number) => ({ generation }));
  const submitSpy = vi.fn(
    async (): Promise<MarkdownAnnotationsSubmitResult> => ({ status: 'delivered', generation: 6 }),
  );
  const transport: MarkdownAnnotationsTransport = {
    getMarkdownAnnotations: getSpy,
    saveMarkdownAnnotations: saveSpy,
    clearMarkdownAnnotations: clearSpy,
    submitMarkdownAnnotations: submitSpy,
  };
  return { transport, getSpy, saveSpy, clearSpy, submitSpy };
}

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

function renderSendTile({
  tileSessionId = 'sess-a',
  sessions = SEND_SESSIONS,
  onRetargetTile = vi.fn(),
}: {
  tileSessionId?: string;
  sessions?: WorkspaceTileSessionOption[];
  onRetargetTile?: (sessionId: string) => Promise<unknown> | void;
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
  const view = render(
    <WorkspaceDockTile tile={sendTile(tileSessionId)} {...props} />,
    { wrapper: NotebookSurfaceTestWrapper },
  );
  return {
    ...view,
    onRetargetTile,
    rebind: (nextSessionId: string) =>
      view.rerender(<WorkspaceDockTile tile={sendTile(nextSessionId)} {...props} />),
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

/** Dispatch ⌘Enter the way the real key arrives: a window-capture keydown. */
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
  beforeEach(() => {
    invokeMock.mockReset();
    invokeMock.mockResolvedValue(undefined);
    vi.mocked(isTauri).mockReturnValue(false);
  });

  afterEach(() => {
    setMarkdownAnnotationsTransport(null);
  });

  it('shows the bound session in Send and flags approval-blocked destinations (E13)', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport().transport);
    renderSendTile();

    await waitFor(() => {
      expect(sendButton()).toHaveAccessibleName('Send 1 to alpha');
    });
    expect(sendButton()).toBeEnabled();
    openSessionDestinations();
    expect(screen.getByRole('menuitemradio', { name: 'alpha' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('menuitemradio', { name: 'beta approval' })).toHaveAttribute('aria-checked', 'false');
  });

  it('navigates destination choices with arrows and returns focus on Escape', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport().transport);
    renderSendTile();
    await waitFor(() => expect(sendButton()).toBeEnabled());

    const caret = screen.getByRole('button', { name: 'Change annotation destination' });
    fireEvent.click(caret);
    const alpha = screen.getByRole('menuitemradio', { name: 'alpha' });
    const beta = screen.getByRole('menuitemradio', { name: 'beta approval' });
    await waitFor(() => expect(alpha).toHaveFocus());
    fireEvent.keyDown(alpha, { key: 'ArrowDown' });
    expect(beta).toHaveFocus();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('menu', { name: 'Send annotations to session' })).toBeNull();
    await waitFor(() => expect(caret).toHaveFocus());
  });

  it('retargets through the destination menu and follows the layout broadcast echo (E13)', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport().transport);
    const onRetargetTile = vi.fn(async () => {});
    const { rebind } = renderSendTile({ onRetargetTile });
    await waitFor(() => {
      expect(sendButton()).toHaveAccessibleName('Send 1 to alpha');
    });

    openSessionDestinations();
    chooseSession('beta approval');
    expect(onRetargetTile).toHaveBeenCalledWith('sess-b');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
    rebind('sess-b');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
  });

  it('a retarget takes effect immediately for Send without waiting for a broadcast (E13)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    setMarkdownAnnotationsTransport(transport);
    const onRetargetTile = vi.fn(async () => {});
    const { rebind } = renderSendTile({ onRetargetTile });
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    openSessionDestinations();
    chooseSession('beta approval');
    expect(onRetargetTile).toHaveBeenCalledWith('sess-b');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(submitSpy).toHaveBeenCalledWith(
        fileMarkdownSource('workspace-1', SEND_PATH),
        { kind: 'session', sessionId: 'sess-b' },
        [],
      );
    });

    rebind('sess-b');
    openSessionDestinations();
    expect(screen.getByRole('menuitemradio', { name: 'beta approval' })).toHaveAttribute('aria-checked', 'true');
  });

  it('rolls Send back to the persisted binding when retargeting fails (E13)', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport().transport);
    const onRetargetTile = vi.fn(async () => {
      throw new Error('retarget rejected');
    });
    renderSendTile({ onRetargetTile });
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    openSessionDestinations();
    chooseSession('beta approval');
    expect(sendButton()).toHaveAccessibleName('Send 1 to beta');
    await waitFor(() => {
      expect(sendButton()).toHaveAccessibleName('Send 1 to alpha');
    });
  });

  it('shows a disabled Send with No session when the bound session left the workspace (E13)', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport().transport);
    renderSendTile({ tileSessionId: 'sess-gone' });

    await waitFor(() => {
      expect(sendButton()).toHaveTextContent('Send 1');
    });
    expect(sendButton()).toHaveTextContent('No session');
    expect(sendButton()).toBeDisabled();
  });

  it('replaces Send 0 with the current destination (E14)', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport([]).transport);
    renderSendTile();

    expect(await screen.findByRole('button', { name: 'Annotation destination: alpha' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: /Send 0/ })).toBeNull();
  });

  it('opens overall notes and the floating review inspector from the tile header', async () => {
    const { transport, saveSpy } = makeSendTransport([]);
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();
    await screen.findByRole('button', { name: 'Annotation destination: alpha' });

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
    await waitFor(() => expect(saveSpy).toHaveBeenCalled());
    const latest = saveSpy.mock.calls[saveSpy.mock.calls.length - 1]?.[1] as WireAnnotation[];
    expect(latest.filter((annotation) => annotation.type === 'global')).toHaveLength(2);
  });

  it('delivers: Sending… → Sent ✓, list empties locally without re-fetch or second clear (E14)', async () => {
    const { transport, getSpy, clearSpy, submitSpy } = makeSendTransport();
    let resolveSubmit: (result: MarkdownAnnotationsSubmitResult) => void = () => {};
    submitSpy.mockImplementation(
      () => new Promise<MarkdownAnnotationsSubmitResult>((resolve) => {
        resolveSubmit = resolve;
      }),
    );
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    fireEvent.click(sendButton());
    expect(sendButton()).toHaveTextContent('Sending…');
    await waitFor(() => {
      expect(submitSpy).toHaveBeenCalledWith(
        fileMarkdownSource('workspace-1', SEND_PATH),
        { kind: 'session', sessionId: 'sess-a' },
        [],
      );
    });

    await act(async () => {
      resolveSubmit({ status: 'delivered', generation: 9 });
    });
    expect(screen.getByRole('status')).toHaveTextContent('Sent ✓');
    expect(sendButton()).toHaveTextContent('Sent ✓');
    expect(sendButton()).toBeDisabled();
    expect(getSpy).toHaveBeenCalledTimes(1);
    expect(clearSpy).not.toHaveBeenCalled();
  });

  it('delivered-but-clear-failed keeps annotations and surfaces the warning, not Sent ✓ (E14)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    submitSpy.mockResolvedValue({
      status: 'delivered',
      error: 'delivered; failed to clear drafts: disk full',
    });
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(screen.getByRole('status')).toHaveTextContent('failed to clear drafts');
    });
    expect(sendButton()).toHaveTextContent('Needs attention');
    expect(sendButton()).toBeEnabled();
    expect(screen.queryByText('Sent ✓')).toBeNull();
  });

  it('refuses to Send while the draft is not hydrated (stale-draft guard, E14)', async () => {
    const { transport, getSpy, submitSpy } = makeSendTransport();
    getSpy.mockImplementation(() => new Promise(() => {}));
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();

    fireEvent.click(screen.getByRole('button', { name: 'Overall note' }));
    fireEvent.change(screen.getByPlaceholderText('Add an overall note...'), {
      target: { value: 'unsaved local note' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    await waitFor(() => {
      expect(sendButton()).toHaveTextContent('Send 1');
    });

    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(screen.getByRole('status')).toHaveTextContent('still syncing');
    });
    expect(submitSpy).not.toHaveBeenCalled();
    expect(sendButton()).toHaveTextContent('Send failed');
  });

  it('keeps annotations and explains when the target is waiting on approval (E15)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    submitSpy.mockResolvedValue({ status: 'skipped_pending_approval' });
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(screen.getByRole('status')).toHaveTextContent(
        'Target is waiting for approval — not sent',
      );
    });
    expect(sendButton()).toHaveTextContent('Approval needed');
    expect(sendButton()).toBeEnabled();
  });

  it('keeps annotations and surfaces the message on a rejected submit (E15)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    submitSpy.mockRejectedValue(new Error('session not found'));
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(screen.getByRole('status')).toHaveTextContent('session not found');
    });
    expect(sendButton()).toHaveTextContent('Send failed');
  });

  it('⌘Enter sends when focus is inside the tile and annotations exist (E18)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    setMarkdownAnnotationsTransport(transport);
    const { container } = renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    const body = container.querySelector<HTMLElement>('.workspace-dock-tile-body')!;
    fireEvent.focusIn(body);
    const event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(true);
    await waitFor(() => {
      expect(submitSpy).toHaveBeenCalledWith(
        fileMarkdownSource('workspace-1', SEND_PATH),
        { kind: 'session', sessionId: 'sess-a' },
        [],
      );
    });
  });

  it('⌘Enter never fires from a textarea, without tile focus, or at zero annotations (E18)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    setMarkdownAnnotationsTransport(transport);
    const { container, unmount } = renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    let event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(false);
    expect(submitSpy).not.toHaveBeenCalled();

    const body = container.querySelector<HTMLElement>('.workspace-dock-tile-body')!;
    const textarea = document.createElement('textarea');
    body.appendChild(textarea);
    fireEvent.focusIn(textarea);
    event = pressCmdEnter(textarea);
    expect(event.defaultPrevented).toBe(false);
    expect(submitSpy).not.toHaveBeenCalled();
    unmount();

    setMarkdownAnnotationsTransport(makeSendTransport([]).transport);
    const zero = renderSendTile();
    await screen.findByRole('button', { name: 'Annotation destination: alpha' });
    const zeroBody = zero.container.querySelector<HTMLElement>('.workspace-dock-tile-body')!;
    fireEvent.focusIn(zeroBody);
    event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(false);
    expect(submitSpy).not.toHaveBeenCalled();
  });
});

function seedFixture(overrides: Partial<Seed> = {}): Seed {
  return {
    id: 's-plan11',
    title: 'Seed reader plan',
    body: '# Seed body\n\nAnnotate this plan.',
    status: 'growing',
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
  };
}

describe('WorkspaceDockTile seed reader', () => {
  afterEach(() => {
    setMarkdownAnnotationsTransport(null);
  });

  it('loads the seed document, renders its plot and collapsed log, annotates by seed URI, and refetches on a garden push', async () => {
    const first = seedDocumentFixture();
    const second = {
      ...seedDocumentFixture('# Updated seed body'),
      seed: seedFixture({ body: '# Updated seed body', rev: 2 }),
    };
    const sendSeedDocumentGet = vi.fn()
      .mockResolvedValueOnce(first)
      .mockResolvedValueOnce(second);
    const sendOpenMarkdown = vi.fn().mockResolvedValue({});
    const daemonApi = { sendSeedDocumentGet, sendOpenMarkdown } as unknown as DaemonApi;
    const { transport, getSpy, submitSpy } = makeSendTransport();
    setMarkdownAnnotationsTransport(transport);
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
    const view = render(
      <WorkspaceDockTile {...props} />,
      { wrapper: ({ children }) => <SeedTileTestWrapper api={daemonApi}>{children}</SeedTileTestWrapper> },
    );

    expect(await screen.findByRole('heading', { name: 'Seed body' })).toBeInTheDocument();
    expect(screen.getByText('Reader child')).toBeInTheDocument();
    expect(screen.getByText('Log').closest('details')).not.toHaveAttribute('open');
    fireEvent.click(screen.getByText('Log').closest('summary') as HTMLElement);
    expect(screen.getByText('Live ledger note')).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText('Seed reader plan', { selector: '.workspace-dock-tile-title' })).toBeInTheDocument();
    });
    expect(view.container.querySelector('.md-reader--annotating')).toBeInTheDocument();
    expect(onRequestContent).not.toHaveBeenCalled();
    expect(getSpy).toHaveBeenCalledWith(seedMarkdownSource('s-plan11'));

    await waitFor(() => expect(sendButton()).toHaveTextContent('Send 1'));
    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(submitSpy).toHaveBeenCalledWith(
        seedMarkdownSource('s-plan11'),
        { kind: 'session', sessionId: 'sess-a' },
        [],
      );
    });

    view.rerender(<WorkspaceDockTile {...props} gardenSeeds={[second.seed]} />);
    expect(await screen.findByRole('heading', { name: 'Updated seed body' })).toBeInTheDocument();
    expect(sendSeedDocumentGet).toHaveBeenCalledTimes(2);
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
    let resolveChild: (detail: SeedDocument) => void = () => {};
    const childPending = new Promise<SeedDocument>((resolve) => { resolveChild = resolve; });
    const sendSeedDocumentGet = vi.fn((seedID: string) => (
      seedID === child.id ? childPending : Promise.resolve(details.get(seedID) as SeedDocument)
    ));
    const daemonApi = { sendSeedDocumentGet, sendOpenMarkdown: vi.fn() } as unknown as DaemonApi;
    const { transport, getSpy } = makeSendTransport();
    setMarkdownAnnotationsTransport(transport);
    const onUpdateParams = vi.fn().mockResolvedValue({});
    const onRevealSeedInGarden = vi.fn();

    render(
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
      { wrapper: ({ children }) => <SeedTileTestWrapper api={daemonApi}>{children}</SeedTileTestWrapper> },
    );

    fireEvent.click(await screen.findByRole('button', { name: /Polish the tile/ }));
    expect(onUpdateParams).toHaveBeenCalledWith(child.id);
    expect(screen.queryByRole('heading', { name: 'Plot plan' })).toBeNull();
    expect(screen.getByText('Loading seed…')).toBeInTheDocument();
    expect(screen.getByText(child.title, { selector: '.workspace-dock-tile-title' })).toBeInTheDocument();
    await act(async () => resolveChild(details.get(child.id) as SeedDocument));
    expect(await screen.findByRole('heading', { name: 'Child body' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Back to Reader polish' })).toBeInTheDocument();
    await waitFor(() => expect(getSpy).toHaveBeenCalledWith(seedMarkdownSource(child.id)));

    fireEvent.click(screen.getByRole('button', { name: 'Reveal in Garden' }));
    expect(onRevealSeedInGarden).toHaveBeenCalledWith(child.id);

    fireEvent.click(screen.getByRole('button', { name: 'Back to Reader polish' }));
    expect(onUpdateParams).toHaveBeenLastCalledWith(plot.id);
    expect(await screen.findByRole('heading', { name: 'Plot plan' })).toBeInTheDocument();
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
    const daemonApi = {
      sendSeedDocumentGet: vi.fn((seedID: string) => Promise.resolve(details.get(seedID) as SeedDocument)),
      sendOpenMarkdown: vi.fn(),
    } as unknown as DaemonApi;
    const onUpdateParams = vi.fn().mockResolvedValue({});
    const view = render(
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
      { wrapper: ({ children }) => <SeedTileTestWrapper api={daemonApi}>{children}</SeedTileTestWrapper> },
    );

    expect(await screen.findByRole('heading', { name: 'Leaf plan' })).toBeInTheDocument();
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
    expect(await screen.findByRole('heading', { name: 'Nested plan' })).toBeInTheDocument();

    const secondEscape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    fireEvent(window, secondEscape);
    expect(secondEscape.defaultPrevented).toBe(true);
    expect(onUpdateParams).toHaveBeenLastCalledWith(root.id);
    expect(await screen.findByRole('heading', { name: 'Root plan' })).toBeInTheDocument();

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
    const childPending = new Promise<SeedDocument>(() => {});
    const sendSeedDocumentGet = vi.fn((seedID: string) => (
      seedID === child.id
        ? childPending
        : Promise.resolve({ ...seedDocumentFixture(plot.body), seed: plot, children: [child] })
    ));
    const daemonApi = { sendSeedDocumentGet, sendOpenMarkdown: vi.fn() } as unknown as DaemonApi;

    render(
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
      { wrapper: ({ children }) => <SeedTileTestWrapper api={daemonApi}>{children}</SeedTileTestWrapper> },
    );

    fireEvent.click(await screen.findByRole('button', { name: /Outside the snapshot/ }));
    expect(screen.queryByRole('heading', { name: 'Previous body' })).toBeNull();
    expect(screen.getByText('Loading seed…')).toBeInTheDocument();
  });

  it('keeps the tended seed primary bound to its live tender and offers Note on seed in the caret menu', async () => {
    const detail = seedDocumentFixture();
    const daemonApi = {
      sendSeedDocumentGet: vi.fn().mockResolvedValue(detail),
      sendOpenMarkdown: vi.fn(),
    } as unknown as DaemonApi;
    const { transport, submitSpy } = makeSendTransport();
    submitSpy.mockResolvedValue({ status: 'noted', generation: 8 });
    setMarkdownAnnotationsTransport(transport);
    render(
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
      { wrapper: ({ children }) => <SeedTileTestWrapper api={daemonApi}>{children}</SeedTileTestWrapper> },
    );

    await waitFor(() => expect(screen.getByRole('button', { name: 'Send 1' })).toBeEnabled());
    expect(screen.queryByRole('combobox', { name: 'Send annotations to session' })).toBeNull();
    const caret = screen.getByRole('button', { name: 'More annotation destinations' });
    expect(caret).toHaveAttribute('aria-haspopup', 'menu');
    fireEvent.click(caret);
    fireEvent.click(screen.getByRole('menuitem', { name: 'Note on seed' }));

    await waitFor(() => {
      expect(submitSpy).toHaveBeenCalledWith(
        seedMarkdownSource('s-plan11'),
        { kind: 'seed', seedId: 's-plan11' },
        [],
      );
    });
    expect(await screen.findByRole('status')).toHaveTextContent('Noted ✓');
  });

  it('makes Note on seed the unsplit primary when nobody tends the seed', async () => {
    const detail = {
      ...seedDocumentFixture(),
      seed: seedFixture({ tender_session: '', tender_member: '' }),
      tender_holds: false,
    };
    const daemonApi = {
      sendSeedDocumentGet: vi.fn().mockResolvedValue(detail),
      sendOpenMarkdown: vi.fn(),
    } as unknown as DaemonApi;
    const { transport, submitSpy } = makeSendTransport();
    submitSpy.mockResolvedValue({ status: 'noted', generation: 8 });
    setMarkdownAnnotationsTransport(transport);
    render(
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
      { wrapper: ({ children }) => <SeedTileTestWrapper api={daemonApi}>{children}</SeedTileTestWrapper> },
    );

    const primary = await screen.findByRole('button', { name: 'Note on seed 1' });
    expect(primary).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'More annotation destinations' })).toBeNull();
    fireEvent.click(primary);
    await waitFor(() => {
      expect(submitSpy).toHaveBeenCalledWith(
        seedMarkdownSource('s-plan11'),
        { kind: 'seed', seedId: 's-plan11' },
        [],
      );
    });
  });

  it('flips the primary live across park and claim pushes without waiting for detail reads', async () => {
    const first = seedDocumentFixture();
    const never = new Promise<SeedDocument>(() => {});
    const sendSeedDocumentGet = vi.fn()
      .mockResolvedValueOnce(first)
      .mockImplementation(() => never);
    const daemonApi = { sendSeedDocumentGet, sendOpenMarkdown: vi.fn() } as unknown as DaemonApi;
    const { transport, submitSpy } = makeSendTransport();
    setMarkdownAnnotationsTransport(transport);
    const props = {
      tile: { type: 'tile' as const, tileId: 'tile-seed-s-plan11', tileKind: 'seed' as const, tileParams: 's-plan11' },
      workspaceId: 'workspace-1',
      dragging: false,
      workspaceSessions: SEND_SESSIONS,
      onClose: vi.fn(),
      onHeaderPointerDown: vi.fn(),
      onRequestContent: vi.fn(),
    };
    const view = render(
      <WorkspaceDockTile {...props} gardenSeeds={[first.seed]} />,
      { wrapper: ({ children }) => <SeedTileTestWrapper api={daemonApi}>{children}</SeedTileTestWrapper> },
    );
    await screen.findByRole('button', { name: 'Send 1' });

    const parked = seedFixture({ tender_session: '', tender_member: '', rev: 2 });
    view.rerender(<WorkspaceDockTile {...props} gardenSeeds={[parked]} />);
    expect(screen.getByRole('button', { name: 'Note on seed 1' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'More annotation destinations' })).toBeNull();

    const claimed = seedFixture({ tender_session: 'sess-b', tender_member: 'trellis', rev: 3 });
    view.rerender(<WorkspaceDockTile {...props} gardenSeeds={[claimed]} />);
    const primary = screen.getByRole('button', { name: 'Send 1' });
    expect(screen.getByRole('button', { name: 'More annotation destinations' })).toBeInTheDocument();
    fireEvent.click(primary);
    await waitFor(() => {
      expect(submitSpy).toHaveBeenCalledWith(
        seedMarkdownSource('s-plan11'),
        { kind: 'session', sessionId: 'sess-b' },
        [],
      );
    });
  });

  it('re-anchors a persisted highlight from the pushed body without remounting or accepting a stale detail body', async () => {
    const oldBody = 'First paragraph with target words inside it.\n';
    const newBody = '# New introduction\n\n' + oldBody;
    const first = {
      ...seedDocumentFixture(oldBody),
      seed: seedFixture({ body: oldBody }),
    };
    let resolveDetail: (document: SeedDocument) => void = () => {};
    const detailPending = new Promise<SeedDocument>((resolve) => { resolveDetail = resolve; });
    const sendSeedDocumentGet = vi.fn()
      .mockResolvedValueOnce(first)
      .mockReturnValueOnce(detailPending);
    const daemonApi = { sendSeedDocumentGet, sendOpenMarkdown: vi.fn() } as unknown as DaemonApi;
    const { transport } = makeSendTransport([anchoredNote(oldBody, 'target words')]);
    setMarkdownAnnotationsTransport(transport);
    const props = {
      tile: { type: 'tile' as const, tileId: 'tile-seed-s-plan11', tileKind: 'seed' as const, tileParams: 's-plan11' },
      workspaceId: 'workspace-1',
      dragging: false,
      gardenSeeds: [first.seed],
      onClose: vi.fn(),
      onHeaderPointerDown: vi.fn(),
      onRequestContent: vi.fn(),
    };
    const view = render(
      <WorkspaceDockTile {...props} />,
      { wrapper: ({ children }) => <SeedTileTestWrapper api={daemonApi}>{children}</SeedTileTestWrapper> },
    );
    await waitFor(() => {
      expect(view.container.querySelector('[data-md-mark="stored-1"]')).not.toBeNull();
    });
    const oldMark = view.container.querySelector('[data-md-mark="stored-1"]');
    expect(oldMark).toHaveTextContent('target words');
    const scrollNode = view.container.querySelector<HTMLElement>('.md-reader-doc')!;
    scrollNode.scrollTop = 137;

    const pushed = seedFixture({ body: newBody, rev: 2 });
    view.rerender(<WorkspaceDockTile {...props} gardenSeeds={[pushed]} />);
    expect(screen.getByRole('heading', { name: 'New introduction' })).toBeInTheDocument();
    expect(view.container.querySelector('.md-reader-doc')).toBe(scrollNode);
    expect(scrollNode.scrollTop).toBe(137);
    await waitFor(() => {
      expect(view.container.querySelector('[data-md-mark="stored-1"]')).toHaveTextContent('target words');
    });
    expect(view.container.querySelector('.md-card-orphan-badge')).toBeNull();
    expect(screen.queryByText('⚠ moved')).toBeNull();
    expect(sendSeedDocumentGet).toHaveBeenCalledTimes(2);

    await act(async () => {
      resolveDetail({ ...first, notes_total: 2 });
    });
    expect(screen.getByRole('heading', { name: 'New introduction' })).toBeInTheDocument();
  });

  it('names an unknown seed read failure in the tile', async () => {
    const daemonApi = {
      sendSeedDocumentGet: vi.fn().mockRejectedValue(new Error('no seed s-missing is planted here')),
      sendOpenMarkdown: vi.fn(),
    } as unknown as DaemonApi;
    render(
      <WorkspaceDockTile
        tile={{ type: 'tile', tileId: 'tile-seed-s-missing', tileKind: 'seed', tileParams: 's-missing' }}
        workspaceId="workspace-1"
        dragging={false}
        onClose={vi.fn()}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
      { wrapper: ({ children }) => <SeedTileTestWrapper api={daemonApi}>{children}</SeedTileTestWrapper> },
    );

    expect(await screen.findByText('no seed s-missing is planted here')).toBeInTheDocument();
  });
});
