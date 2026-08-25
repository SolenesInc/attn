import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NotebookBrowser } from './NotebookBrowser';
import type { FsEntry, FsExistsResult, FsReadAssetResult, FsReadResult, FsWriteResult, NotebookEntry, NotebookSendToChiefResult } from '../hooks/useDaemonSocket';

// The live editor is CodeMirror-backed and cannot mount under happy-dom (its
// async measure pass throws), so it is mocked to a controlled textarea here.
const editorMock = vi.hoisted(() => ({
  current: null as null | {
    onFollowLink?: (href: string) => void;
    onSelectionChange?: (sel: { text: string; top: number; left: number } | null) => void;
  },
  scrollCalls: [] as number[],
  externalApplies: [] as string[],
  focusCalls: 0,
}));

vi.mock('./notebook/LiveMarkdownEditor', async () => {
  const { forwardRef, useImperativeHandle } = await import('react');
  return {
    LiveMarkdownEditor: forwardRef(function MockLiveMarkdownEditor(
      {
        value,
        onChange,
        onFollowLink,
        onSelectionChange,
        ariaLabel,
      }: {
        value: string;
        onChange: (value: string) => void;
        onFollowLink?: (href: string) => void;
        onSelectionChange?: (sel: { text: string; top: number; left: number } | null) => void;
        ariaLabel?: string;
      },
      ref: React.Ref<{ scrollToPos: (pos: number) => void; applyExternalContent: (next: string) => void; closeSearchPanel: () => boolean; focus: () => void }>,
    ) {
      editorMock.current = { onFollowLink, onSelectionChange };
      useImperativeHandle(ref, () => ({
        scrollToPos: (pos: number) => editorMock.scrollCalls.push(pos),
        applyExternalContent: (next: string) => {
          editorMock.externalApplies.push(next);
          onChange(next);
        },
        closeSearchPanel: () => false,
        focus: () => { editorMock.focusCalls += 1; },
      }), [onChange]);
      return (
        <textarea
          aria-label={ariaLabel ?? 'Note'}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        />
      );
    }),
  };
});

const TREE: Record<string, FsEntry[]> = {
  '': [
    { path: 'knowledge', name: 'knowledge', isDir: true, size: 0 },
    { path: 'journal', name: 'journal', isDir: true, size: 0 },
    { path: 'notes.txt', name: 'notes.txt', isDir: false, size: 64 },
    { path: 'cover.png', name: 'cover.png', isDir: false, size: 4096 },
    { path: 'prototype.docx', name: 'prototype.docx', isDir: false, size: 4096 },
  ],
  knowledge: [
    { path: 'knowledge/index.md', name: 'index.md', isDir: false, size: 128 },
    { path: 'knowledge/areas', name: 'areas', isDir: true, size: 0 },
  ],
  'knowledge/areas': [{ path: 'knowledge/areas/foo.md', name: 'foo.md', isDir: false, size: 30 }],
  journal: [{ path: 'journal/2026-06-13.md', name: '2026-06-13.md', isDir: false, size: 20 }],
};

function makeProps(overrides: Partial<React.ComponentProps<typeof NotebookBrowser>> = {}) {
  const listDir = vi
    .fn<(path: string) => Promise<FsEntry[]>>()
    .mockImplementation((path) => Promise.resolve(TREE[path] ?? []));
  const readFile = vi.fn<(path: string) => Promise<FsReadResult>>().mockImplementation((path) =>
    Promise.resolve({ path, content: `# ${path}\n\nSee [the decision](/knowledge/areas/foo.md) for why.`, hash: 'h1' }),
  );
  const backlinksNotebook = vi.fn<(path: string) => Promise<NotebookEntry[]>>().mockResolvedValue([
    { path: 'journal/2026-06-13.md', type: 'journal', title: '2026-06-13', size: 20 },
  ]);
  const writeFile = vi
    .fn<(path: string, content: string, baseHash?: string) => Promise<FsWriteResult>>()
    .mockImplementation((path) => Promise.resolve({ path, hash: 'h2', conflict: false }));
  const existsFile = vi
    .fn<(path: string) => Promise<FsExistsResult>>()
    .mockImplementation((path) => Promise.resolve({ path, exists: true }));
  const readAsset = vi
    .fn<(path: string) => Promise<FsReadAssetResult>>()
    .mockImplementation((path) => Promise.resolve({ path, mimeType: 'image/png', dataBase64: '' }));
  const sendToChief = vi
    .fn<(selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>>()
    .mockResolvedValue({ path: 'inbox.md', nudged: false });
  const listFiles = vi.fn<() => Promise<NotebookEntry[]>>().mockResolvedValue([]);
  return {
    props: {
      isOpen: true,
      onClose: vi.fn(),
      listDir,
      readFile,
      backlinksNotebook,
      writeFile,
      existsFile,
      readAsset,
      sendToChief,
      listFiles,
      changeSignal: 0,
      ...overrides,
    },
    listDir,
    readFile,
    backlinksNotebook,
    writeFile,
    existsFile,
    readAsset,
    sendToChief,
  };
}

function editor() {
  return screen.getByRole('textbox', { name: 'Note' }) as HTMLTextAreaElement;
}

async function waitForNoteLoaded() {
  await waitFor(() => expect(editor().value).toContain('# knowledge/index.md'));
}

function followLink(href: string) {
  act(() => editorMock.current!.onFollowLink!(href));
}

const FOO = '/knowledge/areas/foo.md';

describe('NotebookBrowser', () => {
  afterEach(() => {
    editorMock.current = null;
    editorMock.scrollCalls.length = 0;
    editorMock.externalApplies.length = 0;
    editorMock.focusCalls = 0;
    vi.restoreAllMocks();
  });

  it('renders nothing when closed', () => {
    const { props } = makeProps({ isOpen: false });
    const { container } = render(<NotebookBrowser {...props} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('opens the preferred note and shows its content + backlinks', async () => {
    const { props, readFile, backlinksNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);

    await waitFor(() => expect(readFile).toHaveBeenCalledWith('knowledge/index.md'));
    expect(backlinksNotebook).toHaveBeenCalledWith('knowledge/index.md');
    expect(await screen.findByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();
    await waitForNoteLoaded();
    expect(await screen.findByRole('button', { name: '2026-06-13' })).toBeInTheDocument();
  });

  it('opens an explicitly requested ticket artifact before the preferred note', async () => {
    const path = 'tickets/tk/design.md';
    const { props, readFile } = makeProps({ initialPath: path });
    render(<NotebookBrowser {...props} />);

    await waitFor(() => expect(editor().value).toContain(`# ${path}`));
    expect(readFile).toHaveBeenCalledWith(path);
    expect(readFile).not.toHaveBeenCalledWith('knowledge/index.md');
  });

  it('renders the filesystem tree in the sidebar and opens a clicked text file in a plain editor', async () => {
    const { props, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);

    expect(await screen.findByRole('treeitem', { name: 'knowledge' })).toBeInTheDocument();
    expect(screen.getByRole('treeitem', { name: 'journal' })).toBeInTheDocument();
    const notes = screen.getByRole('treeitem', { name: 'notes.txt' });

    fireEvent.click(notes);
    await waitFor(() => expect(readFile).toHaveBeenCalledWith('notes.txt'));
    const plain = (await screen.findByRole('textbox', { name: 'File contents' })) as HTMLTextAreaElement;
    expect(plain.value).toContain('# notes.txt');
    expect(screen.getByRole('heading', { level: 2, name: 'notes.txt' })).toBeInTheDocument();
    expect(screen.queryByText('Outline')).not.toBeInTheDocument();
    expect(screen.queryByText('Backlinks')).not.toBeInTheDocument();
  });

  it('shows the note outline in the context rail and jumps to a heading on click', async () => {
    const { props, readFile } = makeProps();
    const body = '# Top\n\nintro\n\n## Middle\n\ntext\n\n### Deep\n';
    readFile.mockImplementation((path) => Promise.resolve({ path, content: body, hash: 'h1' }));
    render(<NotebookBrowser {...props} />);

    expect(await screen.findByRole('button', { name: 'Top' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Middle' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Deep' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Middle' }));
    expect(editorMock.scrollCalls).toContain(body.indexOf('## Middle'));
  });

  it('collapses the outline section when its header is toggled', async () => {
    const { props, readFile } = makeProps();
    readFile.mockImplementation((path) =>
      Promise.resolve({ path, content: '# Only heading\n\nbody', hash: 'h1' }),
    );
    render(<NotebookBrowser {...props} />);

    expect(await screen.findByRole('button', { name: 'Only heading' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /^Outline/ }));
    expect(screen.queryByRole('button', { name: 'Only heading' })).not.toBeInTheDocument();
  });

  it('opens a binary file as a read-only placeholder without reading it', async () => {
    const { props, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('treeitem', { name: 'cover.png' });

    fireEvent.click(screen.getByRole('treeitem', { name: 'cover.png' }));

    expect(await screen.findByText('Preview not available')).toBeInTheDocument();
    expect(screen.getByText("cover.png can't be opened here yet.")).toBeInTheDocument();
    expect(readFile).not.toHaveBeenCalledWith('cover.png');
  });

  it('fails closed for an unknown opaque attachment without reading it', async () => {
    const { props, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('treeitem', { name: 'prototype.docx' });

    fireEvent.click(screen.getByRole('treeitem', { name: 'prototype.docx' }));

    expect(await screen.findByText('Preview not available')).toBeInTheDocument();
    expect(screen.getByText("prototype.docx can't be opened here yet.")).toBeInTheDocument();
    expect(readFile).not.toHaveBeenCalledWith('prototype.docx');
  });

  it('does not read a binary file when the Notebook is reopened with one selected', async () => {
    const { props, readFile } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);
    await screen.findByRole('treeitem', { name: 'cover.png' });

    fireEvent.click(screen.getByRole('treeitem', { name: 'cover.png' }));
    expect(await screen.findByText('Preview not available')).toBeInTheDocument();
    rerender(<NotebookBrowser {...props} isOpen={false} />);
    rerender(<NotebookBrowser {...props} isOpen />);

    expect(await screen.findByText('Preview not available')).toBeInTheDocument();
    expect(readFile).not.toHaveBeenCalledWith('cover.png');
  });

  it('navigates when an in-notebook link is followed', async () => {
    const { props, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

    followLink(FOO);

    await waitFor(() => expect(readFile).toHaveBeenCalledWith('knowledge/areas/foo.md'));
    expect(await screen.findByRole('heading', { level: 2, name: 'foo' })).toBeInTheDocument();
  });

  it('navigates when a backlink is clicked', async () => {
    const { props, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

    fireEvent.click(await screen.findByRole('button', { name: '2026-06-13' }));

    await waitFor(() => expect(readFile).toHaveBeenCalledWith('journal/2026-06-13.md'));
  });

  it('renders the clicked note immediately without waiting on the slower backlinks fetch', async () => {
    const { props, backlinksNotebook } = makeProps();
    let resolveBacklinks: (e: NotebookEntry[]) => void = () => {};
    backlinksNotebook.mockImplementation(
      () => new Promise<NotebookEntry[]>((resolve) => { resolveBacklinks = resolve; }),
    );
    render(<NotebookBrowser {...props} />);

    await waitForNoteLoaded();
    expect(await screen.findByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();

    followLink(FOO);
    await waitFor(() => expect(editor().value).toContain('# knowledge/areas/foo.md'));
    expect(screen.getByRole('heading', { level: 2, name: 'foo' })).toBeInTheDocument();

    await act(async () => {
      resolveBacklinks([{ path: 'journal/2026-06-13.md', type: 'journal', title: '2026-06-13', size: 20 }]);
    });
    expect(await screen.findByRole('button', { name: '2026-06-13' })).toBeInTheDocument();
  });

  it("clears the previous note's backlinks the moment a new note is opened, not after the slow walk", async () => {
    const { props, backlinksNotebook } = makeProps();
    let resolveB: (e: NotebookEntry[]) => void = () => {};
    let calls = 0;
    backlinksNotebook.mockImplementation(() => {
      calls += 1;
      if (calls === 1) {
        return Promise.resolve([
          { path: 'journal/2026-06-13.md', type: 'journal', title: 'Backlink to A', size: 20 },
        ]);
      }
      return new Promise<NotebookEntry[]>((resolve) => {
        resolveB = resolve;
      });
    });
    render(<NotebookBrowser {...props} />);

    expect(await screen.findByRole('button', { name: 'Backlink to A' })).toBeInTheDocument();

    followLink(FOO);
    await waitFor(() => expect(editor().value).toContain('# knowledge/areas/foo.md'));

    expect(screen.queryByRole('button', { name: 'Backlink to A' })).not.toBeInTheDocument();
    expect(screen.getByText('Finding backlinks…')).toBeInTheDocument();
    expect(screen.queryByText('No other note links here.')).not.toBeInTheDocument();

    await act(async () => {
      resolveB([{ path: 'knowledge/index.md', type: 'note', title: 'Backlink to B', size: 10 }]);
    });
    expect(await screen.findByRole('button', { name: 'Backlink to B' })).toBeInTheDocument();
    expect(screen.queryByText('Finding backlinks…')).not.toBeInTheDocument();
  });

  it('re-lists the tree and reloads the open note when the change signal bumps', async () => {
    const { props, listDir, readFile } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);
    await waitFor(() => expect(readFile).toHaveBeenCalledWith('knowledge/index.md'));
    await waitFor(() => expect(listDir.mock.calls.some((c) => c[0] === '')).toBe(true));
    const rootListsBefore = listDir.mock.calls.filter((c) => c[0] === '').length;
    const openNoteReadsBefore = readFile.mock.calls.filter((c) => c[0] === 'knowledge/index.md').length;

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    await waitFor(() =>
      expect(listDir.mock.calls.filter((c) => c[0] === '').length).toBeGreaterThan(rootListsBefore),
    );
    await waitFor(() =>
      expect(readFile.mock.calls.filter((c) => c[0] === 'knowledge/index.md').length).toBeGreaterThan(
        openNoteReadsBefore,
      ),
    );
  });

  it('does not disturb the open note when an unrelated change re-reads it unchanged', async () => {
    const { props, readFile, backlinksNotebook } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();
    await waitFor(() => expect(backlinksNotebook).toHaveBeenCalledTimes(1));
    const valueBefore = editor().value;

    rerender(<NotebookBrowser {...props} changeSignal={1} />);
    await waitFor(() =>
      expect(readFile.mock.calls.filter((c) => c[0] === 'knowledge/index.md').length).toBeGreaterThan(1),
    );

    expect(editorMock.externalApplies).toHaveLength(0);
    expect(backlinksNotebook).toHaveBeenCalledTimes(1);
    expect(editor().value).toBe(valueBefore);
  });

  it('applies a genuine on-disk change to the open note via the scroll-preserving path', async () => {
    const { props, readFile, backlinksNotebook } = makeProps();
    let indexReads = 0;
    readFile.mockImplementation((path) => {
      if (path === 'knowledge/index.md') {
        indexReads += 1;
        if (indexReads >= 2) {
          return Promise.resolve({ path, content: '# knowledge/index.md\n\nNEW agent-written body.', hash: 'h-new' });
        }
      }
      return Promise.resolve({ path, content: `# ${path}\n\noriginal body`, hash: 'h1' });
    });
    const { rerender } = render(<NotebookBrowser {...props} />);
    await waitFor(() => expect(editor().value).toContain('original body'));
    await waitFor(() => expect(backlinksNotebook).toHaveBeenCalledTimes(1));

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    await waitFor(() => expect(editor().value).toContain('NEW agent-written body'));
    expect(editorMock.externalApplies).toContain('# knowledge/index.md\n\nNEW agent-written body.');
    await waitFor(() => expect(backlinksNotebook).toHaveBeenCalledTimes(2));
  });

  it('shows the file as unavailable when a change-signal reload finds it deleted', async () => {
    const { props, readFile } = makeProps();
    let indexReads = 0;
    readFile.mockImplementation((path) => {
      if (path === 'knowledge/index.md') {
        indexReads += 1;
        if (indexReads >= 2) return Promise.reject(new Error('fs: knowledge/index.md not found'));
      }
      return Promise.resolve({ path, content: `# ${path}\n\nbody`, hash: 'h1' });
    });
    const { rerender } = render(<NotebookBrowser {...props} />);
    expect(await screen.findByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    expect(await screen.findByText('File unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 2, name: 'index' })).not.toBeInTheDocument();
  });

  it('keeps the open note when the tree refresh fails transiently', async () => {
    const { props, listDir } = makeProps();
    let rootLists = 0;
    listDir.mockImplementation((path) => {
      if (path === '') {
        rootLists += 1;
        if (rootLists >= 2) return Promise.reject(new Error('socket closed'));
      }
      return Promise.resolve(TREE[path] ?? []);
    });
    const { rerender } = render(<NotebookBrowser {...props} />);
    expect(await screen.findByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    await waitFor(() => expect(rootLists).toBeGreaterThan(1));
    expect(screen.getByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();
    expect(screen.queryByText('File unavailable')).not.toBeInTheDocument();
  });

  it('shows the empty state and selects nothing when the root is empty', async () => {
    const { props, readFile } = makeProps({
      listDir: vi.fn<(path: string) => Promise<FsEntry[]>>().mockResolvedValue([]),
    });
    readFile.mockRejectedValue(new Error('fs: not found'));
    render(<NotebookBrowser {...props} />);

    expect(await screen.findByText('Nothing selected')).toBeInTheDocument();
    expect(await screen.findByText('This folder is empty.')).toBeInTheDocument();
  });

  it('moves focus into the dialog on open so the focus trap engages', async () => {
    const { props } = makeProps();
    render(<NotebookBrowser {...props} />);

    const dialog = await screen.findByRole('dialog');
    await waitFor(() => expect(dialog).toHaveFocus());
  });

  it('shows an error state when a navigated-to file cannot be read but keeps the tree', async () => {
    const { props, readFile } = makeProps();
    readFile.mockImplementation((path) =>
      path === 'knowledge/areas/foo.md'
        ? Promise.reject(new Error('fs: knowledge/areas/foo.md not found'))
        : Promise.resolve({ path, content: `# ${path}\n\nbody`, hash: 'h1' }),
    );
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

    followLink(FOO);

    expect(await screen.findByText('File unavailable')).toBeInTheDocument();
    expect(screen.getByRole('treeitem', { name: 'knowledge' })).toBeInTheDocument();
  });

  it('autosaves an edited note via hash-CAS and shows a Saved indicator', async () => {
    const { props, writeFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# edited\n' } });

    await waitFor(
      () => expect(writeFile).toHaveBeenCalledWith('knowledge/index.md', '# edited\n', 'h1'),
      { timeout: 2000 },
    );
    expect(await screen.findByText('Saved')).toBeInTheDocument();
  });

  it('there is no view/edit toggle — the surface is always editable', async () => {
    const { props } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    expect(editor()).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument();
  });

  it('surfaces an autosave conflict and lets the user overwrite the on-disk version', async () => {
    const { props, writeFile } = makeProps();
    writeFile
      .mockResolvedValueOnce({ path: 'knowledge/index.md', conflict: true, currentHash: 'hX' })
      .mockResolvedValueOnce({ path: 'knowledge/index.md', hash: 'h3', conflict: false });
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# mine\n' } });

    expect(await screen.findByText(/changed on disk/i)).toBeInTheDocument();
    expect(editor().value).toBe('# mine\n');

    fireEvent.click(screen.getByRole('button', { name: 'Overwrite anyway' }));
    await waitFor(() => expect(writeFile).toHaveBeenLastCalledWith('knowledge/index.md', '# mine\n', 'hX'));
    await waitFor(() => expect(screen.queryByText(/changed on disk/i)).not.toBeInTheDocument());
    expect(editorMock.focusCalls).toBeGreaterThan(0);
  });

  it('flushes an unsaved buffer when navigating away before autosave fires', async () => {
    const { props, writeFile, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# quick edit\n' } });
    followLink(FOO);

    await waitFor(() => expect(writeFile).toHaveBeenCalledWith('knowledge/index.md', '# quick edit\n', 'h1'));
    await waitFor(() => expect(readFile).toHaveBeenCalledWith('knowledge/areas/foo.md'));
    expect(await screen.findByRole('heading', { level: 2, name: 'foo' })).toBeInTheDocument();
  });

  it('aborts navigation and surfaces the conflict when the navigate flush conflicts', async () => {
    const { props, writeFile, readFile } = makeProps();
    writeFile.mockResolvedValueOnce({ path: 'knowledge/index.md', conflict: true, currentHash: 'hX' });
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# mine\n' } });
    followLink(FOO);

    await waitFor(() => expect(writeFile).toHaveBeenCalledWith('knowledge/index.md', '# mine\n', 'h1'));
    expect(await screen.findByText(/changed on disk/i)).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 2, name: 'foo' })).not.toBeInTheDocument();
    expect(editor().value).toBe('# mine\n');
    expect(readFile).not.toHaveBeenCalledWith('knowledge/areas/foo.md');
  });

  it('does not stamp a stale reload-from-disk onto a note navigated to mid-reload', async () => {
    const { props, writeFile, readFile } = makeProps();
    writeFile
      .mockResolvedValueOnce({ path: 'knowledge/index.md', conflict: true, currentHash: 'hX' })
      .mockResolvedValueOnce({ path: 'knowledge/index.md', hash: 'h2', conflict: false });
    let indexReads = 0;
    let resolveReload: (r: FsReadResult) => void = () => {};
    readFile.mockImplementation((path) => {
      if (path === 'knowledge/index.md') {
        indexReads += 1;
        if (indexReads === 2) return new Promise<FsReadResult>((resolve) => { resolveReload = resolve; });
      }
      return Promise.resolve({ path, content: `# ${path}\n\nbody`, hash: 'h1' });
    });
    render(<NotebookBrowser {...props} />);
    await waitFor(() => expect(editor().value).toContain('# knowledge/index.md'));

    fireEvent.change(editor(), { target: { value: '# mine\n' } });
    expect(await screen.findByText(/changed on disk/i, undefined, { timeout: 2500 })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Reload from disk' }));
    followLink(FOO);
    await screen.findByRole('heading', { level: 2, name: 'foo' });
    await waitFor(() => expect(editor().value).toContain('# knowledge/areas/foo.md'));

    await act(async () => {
      resolveReload({ path: 'knowledge/index.md', content: '# reloaded index\n', hash: 'hX' });
    });
    expect(editor().value).not.toContain('# reloaded index');
    expect(editor().value).toContain('# knowledge/areas/foo.md');
  });

  it('keeps the buffer when a change signal arrives with unsaved edits (no clobber)', async () => {
    const { props, readFile, listDir } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# my draft\n' } });

    const rootListsBefore = listDir.mock.calls.filter((c) => c[0] === '').length;
    const readsBefore = readFile.mock.calls.length;
    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    await waitFor(() =>
      expect(listDir.mock.calls.filter((c) => c[0] === '').length).toBeGreaterThan(rootListsBefore),
    );
    expect(editor().value).toBe('# my draft\n');
    expect(readFile.mock.calls.length).toBe(readsBefore);
  });

  it('does not stamp a mid-autosave result onto a note the user navigated to', async () => {
    const { props, writeFile } = makeProps();
    let resolveWrite: (r: FsWriteResult) => void = () => {};
    writeFile.mockImplementationOnce(
      () => new Promise<FsWriteResult>((resolve) => { resolveWrite = resolve; }),
    );
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# A edited\n' } });
    await waitFor(
      () => expect(writeFile).toHaveBeenCalledWith('knowledge/index.md', '# A edited\n', 'h1'),
      { timeout: 2000 },
    );

    followLink(FOO);
    await screen.findByRole('heading', { level: 2, name: 'foo' });
    await waitFor(() => expect(editor().value).toContain('# knowledge/areas/foo.md'));

    await act(async () => {
      resolveWrite({ path: 'knowledge/index.md', hash: 'h2', conflict: false });
    });

    expect(editor().value).not.toContain('# A edited');
    expect(screen.queryByText('Saved')).not.toBeInTheDocument();
  });

  it('auto-dismisses the Saved indicator after a successful autosave', async () => {
    const { props } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# edited\n' } });

    expect(await screen.findByText('Saved')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText('Saved')).not.toBeInTheDocument(), { timeout: 4000 });
  }, 10000);

  it('sends an editor selection to the chief and shows the outcome', async () => {
    const { props, sendToChief } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

    act(() => editorMock.current!.onSelectionChange!({ text: 'a key decision', top: 40, left: 60 }));
    fireEvent.click(await screen.findByRole('button', { name: 'Send to chief' }, { timeout: 4000 }));

    await waitFor(() => expect(sendToChief).toHaveBeenCalledWith('a key decision', 'knowledge/index.md'));
    expect(await screen.findByText("Added to chief's inbox")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Send to chief' })).not.toBeInTheDocument());
  });

  it('shows no send-to-chief action for a collapsed selection', async () => {
    const { props, sendToChief } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

    act(() => editorMock.current!.onSelectionChange!(null));

    expect(screen.queryByRole('button', { name: 'Send to chief' })).not.toBeInTheDocument();
    expect(sendToChief).not.toHaveBeenCalled();
  });

  it('surfaces an error when sending to the chief fails', async () => {
    const { props, sendToChief } = makeProps();
    sendToChief.mockRejectedValueOnce(new Error('no chief reachable'));
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

    act(() => editorMock.current!.onSelectionChange!({ text: 'something', top: 40, left: 60 }));
    fireEvent.click(await screen.findByRole('button', { name: 'Send to chief' }, { timeout: 4000 }));

    expect(await screen.findByText('no chief reachable')).toBeInTheDocument();
  });

  it('does not flash a send-to-chief outcome on a note navigated to mid-send', async () => {
    const { props, sendToChief } = makeProps();
    let resolveSend: (r: NotebookSendToChiefResult) => void = () => {};
    sendToChief.mockImplementationOnce(
      () => new Promise<NotebookSendToChiefResult>((resolve) => { resolveSend = resolve; }),
    );
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

    act(() => editorMock.current!.onSelectionChange!({ text: 'from A', top: 40, left: 60 }));
    fireEvent.click(await screen.findByRole('button', { name: 'Send to chief' }, { timeout: 4000 }));
    await waitFor(() => expect(sendToChief).toHaveBeenCalledWith('from A', 'knowledge/index.md'));

    followLink(FOO);
    await screen.findByRole('heading', { level: 2, name: 'foo' });

    await act(async () => {
      resolveSend({ path: 'inbox.md', nudged: false });
    });
    expect(screen.queryByText("Added to chief's inbox")).not.toBeInTheDocument();
  });

});

describe('NotebookBrowser stage 5 chrome', () => {
  afterEach(() => {
    editorMock.current = null;
    editorMock.scrollCalls.length = 0;
    editorMock.externalApplies.length = 0;
    editorMock.focusCalls = 0;
    vi.restoreAllMocks();
  });

  const body = () => document.querySelector('.notebook-browser-body') as HTMLElement;

  it('folds and unfolds the file tree via its edge handle, without unmounting the pane', async () => {
    const { props } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    expect(body().className).not.toContain('tree-folded');
    fireEvent.click(screen.getByRole('button', { name: 'Hide file tree' }));
    expect(body().className).toContain('tree-folded');
    expect(editor()).toBeInTheDocument();
    const list = document.querySelector('.notebook-browser-list');
    expect(list).not.toBeNull();
    expect(list?.getAttribute('aria-hidden')).toBe('true');
    expect(list?.hasAttribute('inert')).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Show file tree' }));
    expect(body().className).not.toContain('tree-folded');
    expect(list?.hasAttribute('inert')).toBe(false);
  });

  it('folds the context rail (present only for a markdown note)', async () => {
    const { props } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.click(screen.getByRole('button', { name: 'Hide context rail' }));
    expect(body().className).toContain('rail-folded');
    const rail = document.querySelector('.notebook-browser-rail');
    expect(rail?.hasAttribute('inert')).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Show context rail' }));
    expect(body().className).not.toContain('rail-folded');
    expect(rail?.hasAttribute('inert')).toBe(false);
  });

  it('shows the chief pulse as active / idle, and hides it when there is no chief', () => {
    const { unmount } = render(<NotebookBrowser {...makeProps({ chiefActive: true }).props} />);
    expect(screen.getByText('chief: active')).toBeInTheDocument();
    unmount();

    const idle = render(<NotebookBrowser {...makeProps({ chiefActive: false }).props} />);
    expect(screen.getByText('chief: idle')).toBeInTheDocument();
    idle.unmount();

    render(<NotebookBrowser {...makeProps().props} />);
    expect(screen.queryByText(/chief:/)).not.toBeInTheDocument();
  });

  it('renders a kind badge from the note frontmatter type', async () => {
    const { props } = makeProps({
      readFile: vi.fn<(path: string) => Promise<FsReadResult>>().mockResolvedValue({
        path: 'knowledge/index.md',
        content: '---\ntype: journal\n---\n# Friday\n\nbody',
        hash: 'h1',
      }),
    });
    render(<NotebookBrowser {...props} />);

    const badge = await screen.findByText('journal', { selector: '.notebook-browser-kind-badge' });
    expect(badge.className).toContain('is-journal');
  });
});
