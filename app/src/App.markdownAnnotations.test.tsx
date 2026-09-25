import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentPane, daemonSeed, daemonSession, daemonWorkspace, dockTiles, seedDocument, type DaemonTile } from './test/daemonFixtures';
import type { CommandMessage } from './test/protocol';
import { renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const SEED_URI = 'attn://seed/s-7k3f9m';

const PLAN = daemonSeed('s-7k3f9m', { title: 'parser plan', body: '# Plan\n\nShip the parser fix.', status: 'planted' });

function workspaceWith(tiles: DaemonTile[]) {
  return daemonWorkspace('ws', { root: dockTiles({ type: 'pane', pane_id: 'pane-s1' }, tiles), panes: [agentPane('s1', 'ws')] }, { title: 'ws' });
}

async function openTiles(tiles: DaemonTile[], script: (daemon: ScriptedDaemon) => void) {
  const view = await renderApp({
    initialState: { sessions: [daemonSession('s1', { workspace_id: 'ws' })], workspaces: [workspaceWith([])], seeds: [PLAN] },
  });
  script(view.daemon);
  fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
  await view.daemon.idle();
  view.daemon.emit({ event: 'workspace_layout_updated', workspace_layout: workspaceWith(tiles).layout! });
  await view.daemon.idle();
  return view;
}

function serveSeedPlan(daemon: ScriptedDaemon) {
  daemon.on('seed_document_get', () => ({
    event: 'seed_document_get_result',
    success: true,
    document: seedDocument(PLAN),
  }));
  daemon.on('markdown_annotations_get', ({ document_uri, source_kind }) => ({
    event: 'markdown_annotations_get_result',
    document_uri,
    source_kind,
    success: true,
    annotations: [],
    generation: 5,
  }));
}

async function addOverallNote(daemon: ScriptedDaemon, text: string) {
  const tile = within(document.querySelector<HTMLElement>('[data-pane-id="tile-seed"]')!);
  fireEvent.click(tile.getByRole('button', { name: 'Overall note' }));
  fireEvent.change(screen.getByPlaceholderText('Add an overall note...'), { target: { value: text } });
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  await act(() => vi.advanceTimersByTimeAsync(500));
  await daemon.idle();
}

function answerSave(daemon: ScriptedDaemon, save: CommandMessage<'markdown_annotations_save'>, outcome: { success: boolean; stale?: boolean }) {
  daemon.emit({
    event: 'markdown_annotations_save_result',
    request_id: save.request_id,
    document_uri: save.document_uri,
    source_kind: save.source_kind,
    generation: save.generation,
    ...outcome,
  });
}

describe('App markdown annotations', () => {
  it('saves a seed document’s marks under the seed’s identity, and reads them again when the save was stale', async () => {
    const { daemon } = await openTiles([{ tile_id: 'tile-seed', tile_kind: 'seed', tile_params: PLAN.id }], serveSeedPlan);
    expect(daemon.sentOf('markdown_annotations_get')).toEqual([
      expect.objectContaining({ document_uri: SEED_URI, source_kind: 'seed', seed_id: PLAN.id }),
    ]);

    await addOverallNote(daemon, 'Split the fix from the refactor.');
    const [save] = daemon.sentOf('markdown_annotations_save');
    expect(save).toMatchObject({
      document_uri: SEED_URI,
      source_kind: 'seed',
      seed_id: PLAN.id,
      generation: 6,
      annotations: [expect.objectContaining({ type: 'global', text: 'Split the fix from the refactor.' })],
    });

    answerSave(daemon, save, { success: false, stale: true });
    await daemon.idle();

    expect(daemon.sentOf('markdown_annotations_get')).toHaveLength(2);
  });

  it('lets a newer save stand when the answer to the one it overtook arrives late', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { daemon } = await openTiles([{ tile_id: 'tile-seed', tile_kind: 'seed', tile_params: PLAN.id }], serveSeedPlan);

    await addOverallNote(daemon, 'First overall note');
    await addOverallNote(daemon, 'Second overall note');
    const [first, second] = daemon.sentOf('markdown_annotations_save');
    expect(first.request_id).not.toBe(second.request_id);

    answerSave(daemon, first, { success: false, stale: true });
    answerSave(daemon, second, { success: true });
    await daemon.idle();

    expect(daemon.sentOf('markdown_annotations_get')).toHaveLength(1);
  });

  it('reads a document’s marks once while two tiles show it', async () => {
    const { daemon } = await openTiles([
      { tile_id: 'tile-a', tile_kind: 'markdown', tile_params: '/tmp/doc.md' },
      { tile_id: 'tile-b', tile_kind: 'markdown', tile_params: '/tmp/doc.md' },
    ], () => {});
    for (const tileId of ['tile-a', 'tile-b']) {
      daemon.emit({ event: 'workspace_tile_content', workspace_id: 'ws', tile_id: tileId, tile_kind: 'markdown', path: '/tmp/doc.md', content: '# Doc\n\nBody.' });
    }
    await daemon.idle();

    expect(daemon.sentOf('markdown_annotations_get')).toEqual([
      expect.objectContaining({ document_uri: 'attn://file/ws/%2Ftmp%2Fdoc.md', source_kind: 'file' }),
    ]);
  });
});
