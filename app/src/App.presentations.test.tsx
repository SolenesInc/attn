import { fireEvent, screen, within } from '@testing-library/react';
import { invoke } from '@tauri-apps/api/core';
import { describe, expect, it, vi } from 'vitest';
import { agentPane, daemonSession, daemonWorkspace } from './test/daemonFixtures';
import type { EventMessage } from './test/protocol';
import { renderApp } from './test/renderApp';

type Presentation = EventMessage<'get_presentations_result'>['presentations'][number];

const REVIEW: Presentation = {
  id: 'pres-7',
  created_at: '2026-07-01T00:00:00Z',
  kind: 'pr',
  latest_round_seq: 1,
  latest_round_submitted: false,
  repo_path: '/tmp/s2',
  session_id: 's2',
  status: 'open',
  title: 'Parser fix, round 1',
};

async function openSplit() {
  const workspace = daemonWorkspace('ws', {
    root: {
      type: 'split',
      split_id: 'split-a',
      direction: 'vertical',
      ratio: 0.5,
      children: [{ type: 'pane', pane_id: 'pane-s1' }, { type: 'pane', pane_id: 'pane-s2' }],
    },
    panes: [agentPane('s1', 'ws'), agentPane('s2', 'ws')],
  }, { title: 'ws' });
  const view = await renderApp({
    initialState: {
      sessions: [daemonSession('s1', { workspace_id: 'ws', state: 'idle' }), daemonSession('s2', { workspace_id: 'ws', state: 'idle' })],
      workspaces: [workspace],
    },
  });
  view.daemon.on('get_presentations', () => ({ event: 'get_presentations_result', success: true, presentations: [] }));
  fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
  await view.daemon.idle();
  return view;
}

const pane = (sessionId: string) => within(document.querySelector<HTMLElement>(`[data-pane-id="pane-${sessionId}"]`)!);

describe('App presentations', () => {
  it('offers a review chip in the header of the session whose presentation awaits review, and opens it there', async () => {
    const { daemon } = await openSplit();
    vi.mocked(invoke).mockResolvedValue(undefined);

    daemon.emit({ event: 'presentation_added', presentation: REVIEW });
    await daemon.idle();

    const chip = screen.getByRole('button', { name: '▶ review' });
    expect(chip).toHaveAttribute('title', 'Parser fix, round 1');
    expect(chip).toHaveAttribute('data-presentation-id', 'pres-7');
    expect(chip).toHaveAttribute('data-session-id', 's2');
    expect(pane('s2').getByRole('button', { name: '▶ review' })).toBe(chip);
    expect(pane('s1').queryByRole('button', { name: '▶ review' })).toBeNull();

    const sent = daemon.sent.length;
    fireEvent.pointerDown(chip, { button: 0, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { clientX: 40, clientY: 40 });
    fireEvent.pointerUp(window, { clientX: 40, clientY: 40 });
    fireEvent.click(chip);
    await daemon.idle();

    expect(invoke).toHaveBeenCalledWith('open_presentation_window', { presentationId: 'pres-7' });
    expect(daemon.sent.slice(sent)).toEqual([]);
  });
});
