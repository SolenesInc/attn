import { fireEvent, screen, within } from '@testing-library/react';
import { invoke } from '@tauri-apps/api/core';
import { describe, expect, it, vi } from 'vitest';
import { agentPane, daemonSession, daemonDesktop } from './test/daemonFixtures';
import type { EventMessage } from './test/protocol';
import { renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

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
  const workspace = daemonDesktop('ws', {
    root: {
      type: 'split',
      split_id: 'split-a',
      direction: 'vertical',
      ratio: 0.5,
      children: [{ type: 'pane', pane_id: 'pane-s1' }, { type: 'pane', pane_id: 'pane-s2' }],
    },
    panes: [agentPane('s1', 'ws'), agentPane('s2', 'ws')],
  }, { name: 'ws' });
  const view = await renderApp({
    initialState: {
      sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'idle' })],
      desktops: [workspace],
    },
  });
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

  describe('which review chip a session shows', () => {
    const chips = () => screen.queryAllByRole('button', { name: '▶ review' });

    async function present(daemon: ScriptedDaemon, event: 'presentation_added' | 'presentation_updated', presentation: Presentation) {
      daemon.emit({ event, presentation });
      await daemon.idle();
    }

    it('keeps only the presentations still awaiting review from the list the daemon gives at connect', async () => {
      const { daemon } = await openSplit();
      expect(daemon.sentOf('get_presentations')).toHaveLength(1);

      daemon.emit({
        event: 'get_presentations_result',
        success: true,
        presentations: [
          REVIEW,
          { ...REVIEW, id: 'submitted', session_id: 's1', latest_round_submitted: true },
          { ...REVIEW, id: 'closed', session_id: 's1', status: 'closed' },
        ],
      });
      await daemon.idle();

      expect(chips().map((chip) => chip.getAttribute('data-presentation-id'))).toEqual(['pres-7']);
    });

    it.each([
      ['its latest round is submitted', { latest_round_submitted: true }],
      ['it closes', { status: 'closed' }],
    ])('drops the chip once %s', async (_, change) => {
      const { daemon } = await openSplit();
      await present(daemon, 'presentation_added', REVIEW);

      await present(daemon, 'presentation_updated', { ...REVIEW, ...change });

      expect(chips()).toEqual([]);
    });

    it('updates a chip in place rather than adding another', async () => {
      const { daemon } = await openSplit();
      await present(daemon, 'presentation_added', REVIEW);

      await present(daemon, 'presentation_updated', { ...REVIEW, title: 'Parser fix, round 2' });

      expect(chips().map((chip) => chip.getAttribute('title'))).toEqual(['Parser fix, round 2']);
    });

    it.each([
      ['newest last', ['pres-old', 'pres-new']],
      ['newest first', ['pres-new', 'pres-old']],
    ])('shows the newest of a session’s presentations, whichever arrives %s', async (_, order) => {
      const { daemon } = await openSplit();
      const created = { 'pres-old': '2026-07-01T00:00:00Z', 'pres-new': '2026-07-02T00:00:00Z' } as Record<string, string>;

      for (const id of order) await present(daemon, 'presentation_added', { ...REVIEW, id, created_at: created[id] });

      expect(chips().map((chip) => chip.getAttribute('data-presentation-id'))).toEqual(['pres-new']);
    });
  });
});
