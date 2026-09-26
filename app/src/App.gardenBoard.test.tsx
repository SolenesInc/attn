import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonSession, type DaemonSeed } from './test/daemonFixtures';
import { openGarden, planted, plot } from './test/garden';
import { gesture, pressShortcut } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

function armedOn(number: number) {
  return {
    pull_request: `github.com:victorarias/attn#${number}`,
    url: `https://github.com/victorarias/attn/pull/${number}`,
    set_at: '2026-08-27T09:00:00Z',
  };
}

const garden = [
  planted('s-ready1', 'pick this up', { ready: true }),
  planted('s-wait11', 'waiting on work'),
  plot('s-plot11', 'a plot in motion', '', { total: 3, done: 1, growing: 1, ready: 1 }, { ready: true }),
  planted('s-work11', 'owned work', { status: 'growing', tender_member: 'trellis' }),
  planted('s-park11', 'paused on purpose', { status: 'dormant' }),
  planted('s-armed1', 'waiting on the merge', { status: 'dormant', harvest_when: armedOn(42) }),
  planted('s-armrdy', 'armed but still pickable', { ready: true, harvest_when: armedOn(43) }),
  planted('s-done11', 'finished work', { status: 'harvested' }),
];

async function openBoard(seeds: DaemonSeed[], sessions = [daemonSession('s1')]) {
  const { daemon } = await openGarden(seeds, { sessions });
  await gesture(daemon, () => pressShortcut('board.open'));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'board' })));
  daemon.on('seed_transition', ({ seed_id }) => ({
    event: 'seed_transition_result',
    success: true,
    seed: seeds.find((seed) => seed.id === seed_id)!,
  }));
  return daemon;
}

const column = (label: string) => within(screen.getByRole('region', { name: new RegExp(`^${label}, `) }));
const theBoard = () => within(screen.getByRole('region', { name: 'The garden board' }));
const card = (title: string) => theBoard().getByRole('button', { name: new RegExp(`^${title}`) });

async function move(daemon: ScriptedDaemon, title: string, verb: RegExp, prompt: string, text: string) {
  fireEvent.focus(card(title));
  fireEvent.click(theBoard().getByRole('button', { name: `Move ${title}` }));
  fireEvent.click(screen.getByRole('menuitem', { name: verb }));
  const field = screen.getByRole('textbox', { name: prompt });
  fireEvent.change(field, { target: { value: text } });
  await gesture(daemon, () => fireEvent.keyDown(field, { key: 'Enter' }));
}

describe('App garden board', () => {
  it('names active work In progress and counts a plot in motion', async () => {
    await openBoard(garden);

    expect(screen.getByRole('heading', { name: 'In progress' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Growing' })).toBeNull();
    expect(screen.getByText('1 in progress · 1 ready · 1/3 done')).toBeInTheDocument();
  });

  it('puts dormant work under Parked', async () => {
    await openBoard(garden);

    expect(column('Parked').getByRole('button', { name: /^paused on purpose/ })).toHaveTextContent('parked');
  });

  it('says what an armed card waits on instead of repeating its column, and keeps it beside ready', async () => {
    await openBoard(garden);

    const armed = card('waiting on the merge');
    expect(armed).toHaveTextContent('harvests on #42');
    expect(armed).not.toHaveTextContent('parked');
    expect(within(armed).getByText('harvests on #42')).toHaveAttribute('title', 'harvests when victorarias/attn#42 merges');
    expect(card('paused on purpose')).not.toHaveTextContent('harvests on');

    expect(card('armed but still pickable')).toHaveTextContent('ready');
    expect(card('armed but still pickable')).toHaveTextContent('harvests on #43');
  });

  it('takes a seed over from its live tender when harvesting it', async () => {
    const held = planted('s-held11', 'held work', { status: 'growing', tender_session: 'live-tender' });
    const daemon = await openBoard([held], [daemonSession('s1'), daemonSession('live-tender')]);

    await move(daemon, 'held work', /Harvest/, 'Harvest s-held11: what got done', 'the work is complete');

    expect(daemon.sentOf('seed_transition')).toEqual([expect.objectContaining({
      seed_id: 's-held11', verb: 'harvest', reason: 'the work is complete', force: true,
    })]);
  });

  it('parks a seed with its comment in one move', async () => {
    const growing = planted('s-quiet1', 'quiet work', { status: 'growing' });
    const daemon = await openBoard([growing]);

    await move(daemon, 'quiet work', /Park/, 'Park s-quiet1: what you are leaving it at', 'waiting for product input');

    const [park] = daemon.sentOf('seed_transition');
    expect(park).toMatchObject({ seed_id: 's-quiet1', verb: 'park', comment: 'waiting for product input' });
    expect(park).not.toHaveProperty('force');
    expect(daemon.sentOf('seed_note')).toEqual([]);
  });
});
