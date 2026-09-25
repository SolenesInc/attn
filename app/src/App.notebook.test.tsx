import { act, fireEvent, screen } from '@testing-library/react';
import { EditorView } from '@codemirror/view';
import { describe, expect, it, vi } from 'vitest';
import { renderApp } from './test/renderApp';

async function openNotebook() {
  const view = await renderApp();
  view.daemon.on('fs_list', ({ path }) => ({
    event: 'fs_list_result',
    success: true,
    entries: path
      ? [{ path: `${path}/areas.md`, name: 'areas.md', is_dir: false, size: 12 }]
      : [
        { path: 'knowledge', name: 'knowledge', is_dir: true, size: 0 },
        { path: 'todo.md', name: 'todo.md', is_dir: false, size: 12, modified: '2026-06-20T00:00:00Z' },
        { path: 'gone.md', name: 'gone.md', is_dir: false, size: 12 },
      ],
  }));
  view.daemon.on('fs_read', ({ path }) => (
    path === 'todo.md'
      ? { event: 'fs_read_result', success: true, result: { path, content: '# Todo\n\nbuy milk', hash: 'h1' } }
      : { event: 'fs_read_result', success: false, error: `fsdoc: ${path} not found` }
  ));
  view.daemon.on('notebook_backlinks', () => ({
    event: 'notebook_backlinks_result',
    success: true,
    entries: [{ path: 'journal/2026-08-01.md', size: 2 }],
  }));
  fireEvent.click(screen.getByRole('button', { name: /Open Notebook/ }));
  await view.daemon.idle();
  return view;
}

async function openEntry(daemon: Awaited<ReturnType<typeof openNotebook>>['daemon'], name: string) {
  fireEvent.click(screen.getByRole('treeitem', { name }));
  await daemon.idle();
}

function documentPane() {
  return document.querySelector<HTMLElement>('.notebook-browser-document')!;
}

describe('App notebook', () => {
  it('lists the notebook root, then a folder when the user opens it', async () => {
    const { daemon } = await openNotebook();
    expect(daemon.sentOf('fs_list')[0]).not.toHaveProperty('path');
    expect(screen.getByRole('treeitem', { name: 'knowledge' })).toHaveAttribute('aria-expanded', 'false');

    await openEntry(daemon, 'knowledge');

    expect(daemon.sentOf('fs_list')).toContainEqual(expect.objectContaining({ path: 'knowledge' }));
    expect(screen.getByRole('treeitem', { name: 'areas.md' })).toBeInTheDocument();
  });

  it('shows an opened note with the notes that link to it', async () => {
    const { daemon } = await openNotebook();

    await openEntry(daemon, 'todo.md');

    expect(documentPane()).toHaveTextContent('buy milk');
    expect(daemon.sentOf('notebook_backlinks')).toContainEqual(expect.objectContaining({ path: 'todo.md' }));
    expect(screen.getByRole('button', { name: '2026-08-01' })).toHaveAttribute('title', 'journal/2026-08-01.md');
  });

  it('says why a note the daemon cannot read is unavailable', async () => {
    const { daemon } = await openNotebook();

    await openEntry(daemon, 'gone.md');

    expect(documentPane()).toHaveTextContent('File unavailable');
    expect(documentPane()).toHaveTextContent('fsdoc: gone.md not found');
  });

  it('saves an edit against the version it read, and overwrites the changed file only when the user says so', async () => {
    const { daemon } = await openNotebook();
    daemon.on('fs_write', ({ path, base_hash }) => (
      base_hash === 'h1'
        ? { event: 'fs_write_result', success: true, result: { path, conflict: true, current_hash: 'h2' } }
        : { event: 'fs_write_result', success: true, result: { path, conflict: false, hash: 'h3' } }
    ));
    await openEntry(daemon, 'todo.md');

    const editor = EditorView.findFromDOM(documentPane().querySelector<HTMLElement>('.cm-content')!)!;
    act(() => editor.dispatch({ changes: { from: editor.state.doc.length, insert: ' and eggs' } }));
    await act(() => vi.advanceTimersByTimeAsync(700));
    await daemon.idle();
    expect(documentPane()).toHaveTextContent('This file changed on disk since you opened it.');

    fireEvent.click(screen.getByRole('button', { name: 'Overwrite anyway' }));
    await daemon.idle();

    expect(daemon.sentOf('fs_write').map(({ path, content, base_hash }) => ({ path, content, base_hash }))).toEqual([
      { path: 'todo.md', content: '# Todo\n\nbuy milk and eggs', base_hash: 'h1' },
      { path: 'todo.md', content: '# Todo\n\nbuy milk and eggs', base_hash: 'h2' },
    ]);
    expect(documentPane()).toHaveTextContent('Saved');
  });
});
