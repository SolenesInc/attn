import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gardenRegion, openGarden, openRow, partOf, planted, plot } from './test/garden';
import { gesture } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const shipIt = plot('s-crown1', 'ship the search', '', { total: 3, done: 1, ready: 1 });
const wiring = partOf('s-crown1', 's-wire01', 'wire the field', { body: 'the input is a line of type, not a box' });
const ranking = partOf('s-crown1', 's-rank01', 'rank the answers', { ready: true });
const shipped = partOf('s-crown1', 's-done01', 'draw the field', { status: 'harvested' });
const elsewhere = planted('s-else01', 'unrelated field work', { tender_member: 'hazel' });
const dropped = planted('s-drop01', 'a dropped idea', { status: 'withered' });
const garden = [shipIt, wiring, ranking, shipped, elsewhere, dropped];

const field = () => within(gardenRegion()).getByRole('combobox') as HTMLInputElement;
const ask = (query: string) => fireEvent.change(field(), { target: { value: query } });
const listed = () => within(gardenRegion()).queryAllByRole('listitem').map((item) => item.textContent ?? '').join('\n');
const answers = () => within(gardenRegion()).queryAllByRole('option');
const activeAnswer = () => field().getAttribute('aria-activedescendant');
const press = (key: string, init: { altKey?: boolean } = {}) => fireEvent.keyDown(field(), { key, ...init });

async function click(daemon: ScriptedDaemon, name: string | RegExp) {
  await gesture(daemon, () => fireEvent.click(within(gardenRegion()).getByRole('button', { name })));
}

async function searchInsideThePlot(query: string) {
  const { daemon } = await openGarden(garden);
  await openRow(daemon, 'ship the search');
  ask(query);
  return daemon;
}

describe('App garden search', () => {
  it('flattens the garden into the answer, out of every plot at once', async () => {
    await openGarden(garden);
    ask('field');

    expect(listed()).toContain('wire the field');
    expect(listed()).toContain('unrelated field work');
  });

  it('shows the line of body that answered the query', async () => {
    await openGarden(garden);
    ask('line of type');

    expect(listed()).toContain('a line of type, not a box');
  });

  it('names a filter it does not have rather than answering with an empty list', async () => {
    await openGarden(garden);
    ask('is:done');

    expect(screen.getByText(/no filter called is:done/)).toBeInTheDocument();
  });

  it('offers the values of an operator being typed, and puts one into the query', async () => {
    const { daemon } = await openGarden(garden);
    ask('is:');

    await click(daemon, 'ready');

    expect(field()).toHaveValue('is:ready ');
  });

  it('names the query and the scope when nothing matches, and clears it on request', async () => {
    const { daemon } = await openGarden(garden);
    ask('nowhere');

    expect(within(gardenRegion()).getByText(/Nothing in the garden matches/)).toHaveTextContent('nowhere');
    await click(daemon, /Clear the search/);

    expect(field()).toHaveValue('');
  });

  it('toggles the closed lens by writing it into the query, without flattening the plots', async () => {
    const { daemon } = await openGarden(garden);
    const show = within(gardenRegion()).getByRole('button', { name: '1 closed' });
    expect(show).toHaveAttribute('aria-pressed', 'false');

    await click(daemon, '1 closed');
    expect(field()).toHaveValue('is:any');
    expect(listed()).toContain('a dropped idea');
    expect(listed()).not.toContain('wire the field');
    expect(listed()).not.toContain('draw the field');
    expect(within(gardenRegion()).getByRole('button', { name: 'hide 1 closed' })).toHaveAttribute('aria-pressed', 'true');

    await click(daemon, 'hide 1 closed');
    expect(listed()).not.toContain('a dropped idea');
  });

  it('counts what a search is hiding, and takes it back when asked', async () => {
    const { daemon } = await openGarden(garden);
    ask('field');
    expect(listed()).not.toContain('draw the field');

    await click(daemon, '1 closed');

    expect(field()).toHaveValue('field is:any');
    expect(listed()).toContain('draw the field');
  });

  it('stands down when the query names a lens of its own', async () => {
    await openGarden(garden);
    ask('field is:closed');

    expect(within(gardenRegion()).queryByRole('button', { name: /closed$/ })).toBeNull();
  });

  it('searches the plot you are standing in, and says what the garden holds', async () => {
    await searchInsideThePlot('field');

    expect(answers()).toHaveLength(1);
    expect(within(gardenRegion()).getByRole('button', { name: /\+1 in the whole garden/ })).toBeInTheDocument();
  });

  it('keeps the trail when the search widens out of the plot, and goes back to the plot when cleared', async () => {
    const daemon = await searchInsideThePlot('field');

    await click(daemon, /\+1 in the whole garden/);
    expect(answers()).toHaveLength(2);
    expect(screen.getByRole('navigation', { name: /Standing here/ })).toBeInTheDocument();
    expect(within(gardenRegion()).getByRole('button', { name: /1 in this plot/ })).toBeInTheDocument();

    ask('');
    expect(listed()).toContain('rank the answers');
    expect(listed()).not.toContain('unrelated field work');
  });

  it('widens the scope with one key, and narrows it back with the same one', async () => {
    await searchInsideThePlot('field');

    press('Enter', { altKey: true });
    expect(answers()).toHaveLength(2);
    press('Enter', { altKey: true });
    expect(answers()).toHaveLength(1);
  });

  it('walks the answers with the arrows without wrapping past the ends', async () => {
    await openGarden(garden);
    ask('field');
    expect(activeAnswer()).toBe('garden-row-s-wire01');

    press('ArrowUp');
    expect(activeAnswer()).toBe('garden-row-s-wire01');
    press('ArrowDown');
    expect(activeAnswer()).toBe('garden-row-s-else01');
    press('ArrowDown');
    expect(activeAnswer()).toBe('garden-row-s-else01');
    press('ArrowUp');
    expect(activeAnswer()).toBe('garden-row-s-wire01');
  });

  it('starts a new question at its best answer', async () => {
    await openGarden(garden);
    ask('field');
    press('ArrowDown');

    ask('field w');

    expect(activeAnswer()).toBe('garden-row-s-wire01');
  });
});
