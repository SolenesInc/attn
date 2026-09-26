import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gardenRegion, openGarden, openRow, partOf, planted, plot, row, seedHeading, trail } from './test/garden';
import { layOutBlocksAcrossSizedAncestors } from './test/layout';
import { gesture, pressShortcut } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const crown = plot('s-crown1', 'the migration');
const middle = plot('s-mid111', 'one panel', 's-crown1');
const inner = plot('s-inn111', 'its header', 's-mid111');
const fourth = plot('s-fou111', 'the overflow menu', 's-inn111');
const fifth = plot('s-fif111', 'the fold threshold', 's-fou111');
const leaf = partOf('s-fif111', 's-leaf11', 'the actual work');
const shipped = partOf('s-crown1', 's-done11', 'already shipped', { status: 'harvested' });
const loose = planted('s-alone1', 'unrelated work');
const deep = [crown, middle, inner, fourth, fifth, leaf, shipped, loose];

const TWO_COLUMNS = 1224;
const THREE_COLUMNS = 1804;

async function expandedGarden(windowWidth: number, seeds = deep) {
  layOutBlocksAcrossSizedAncestors(windowWidth);
  const garden = await openGarden(seeds);
  await click(garden.daemon, 'Expand the garden');
  return garden;
}

async function click(daemon: ScriptedDaemon, name: string | RegExp) {
  await gesture(daemon, () => fireEvent.click(within(gardenRegion()).getByRole('button', { name })));
}

async function walk(daemon: ScriptedDaemon, ...titles: string[]) {
  for (const title of titles) await openRow(daemon, title);
}

function escape(daemon: ScriptedDaemon) {
  return gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
}

const gardenShown = () => screen.queryByRole('region', { name: 'The garden' }) !== null;
const searchField = () => within(gardenRegion()).getByRole('combobox') as HTMLInputElement;

describe('App garden walk', () => {
  it('opens on crowns and loose seeds, keeping plot children inside their plot', async () => {
    const orphan = partOf('s-gone99', 's-orph11', 'crown got capped away');
    await openGarden([crown, middle, shipped, loose, orphan]);

    expect(row('the migration')).not.toBeNull();
    expect(row('unrelated work')).not.toBeNull();
    expect(row('crown got capped away')).not.toBeNull();
    expect(row('one panel')).toBeNull();
    expect(row('already shipped')).toBeNull();
  });

  it('counts a plot on its row and spells it out on its page', async () => {
    const progressing = plot('s-crown1', 'ship the thing', '', { total: 3, done: 1, growing: 1, ready: 1 });
    const { daemon } = await openGarden([progressing, loose]);

    expect(row('ship the thing')).toHaveTextContent('1/3');
    expect(row('unrelated work')).not.toHaveTextContent('/');
    expect(screen.queryByText(/1\/3 done/)).toBeNull();

    await openRow(daemon, 'ship the thing');

    expect(screen.getByText('1/3 done · 1 growing · 1 ready')).toBeInTheDocument();
  });

  it('drills into a plot, keeps its closed seeds behind a toggle, and climbs back out', async () => {
    const { daemon } = await openGarden(deep);

    await openRow(daemon, 'the migration');
    expect(row('one panel')).not.toBeNull();
    expect(row('unrelated work')).toBeNull();
    expect(row('already shipped')).toBeNull();
    await click(daemon, '1 closed');
    expect(row('already shipped')).not.toBeNull();

    await click(daemon, 'Garden');
    expect(row('unrelated work')).not.toBeNull();
  });

  it('walks a plot inside a plot and climbs several levels at once', async () => {
    const { daemon } = await openGarden(deep);

    await walk(daemon, 'the migration', 'one panel');
    expect(seedHeading()).toBe('one panel');
    expect(row('its header')).not.toBeNull();
    expect(trail()).toEqual(['Garden', 'the migration']);

    await click(daemon, 'Garden');

    expect(row('the migration')).not.toBeNull();
    expect(seedHeading()).toBeNull();
  });

  it('climbs out on its own when the plot it is inside disappears', async () => {
    const garden = await openGarden(deep);
    await walk(garden.daemon, 'the migration');

    garden.push([loose]);

    expect(seedHeading()).toBeNull();
    expect(row('unrelated work')).not.toBeNull();
  });

  it('crosses from one plot into a related one', async () => {
    const next = plot('s-crown2', 'the next plot');
    const linked = partOf('s-crown1', 's-child3', 'holds the next plot up', { edges: [{ kind: 'blocks', to: 's-crown2' }] });
    const { daemon } = await openGarden([crown, linked, next, loose]);

    await walk(daemon, 'the migration', 'holds the next plot up');
    await click(daemon, 'the next plot');

    expect(seedHeading()).toBe('the next plot');
    expect(trail()).toEqual(['Garden', 'the migration', 'holds the next plot up']);
  });

  it('names the way in when the garden is empty, and the way into an empty plot', async () => {
    const garden = await openGarden([]);
    expect(screen.getByText(/The garden is empty/)).toBeInTheDocument();
    expect(screen.getByText('attn seed plant "what this is"')).toBeInTheDocument();

    garden.push([plot('s-crown9', 'nothing in it yet', '', { total: 0, ready: 0 })]);
    await openRow(garden.daemon, 'nothing in it yet');

    expect(screen.getByText(/Nothing planted in this plot yet/)).toBeInTheDocument();
    expect(screen.getByText('attn seed plant "what this is" --part-of s-crown9')).toBeInTheDocument();
  });

  it('opens a seed to its own page, and the trail is the way back', async () => {
    const { daemon } = await openGarden([planted('s-body11', 'has a body', { body: 'the plan itself' }), loose]);
    expect(screen.queryByText('the plan itself')).toBeNull();

    await openRow(daemon, 'has a body');
    expect(seedHeading()).toBe('has a body');
    expect(screen.getByText('the plan itself')).toBeInTheDocument();
    expect(row('unrelated work')).toBeNull();

    await click(daemon, 'Garden');
    expect(row('unrelated work')).not.toBeNull();
  });

  it('opens a found seed under the whole part-of path from the Garden root', async () => {
    const { daemon } = await openGarden(deep);
    fireEvent.change(searchField(), { target: { value: 'actual work' } });

    await openRow(daemon, 'the actual work');

    expect(seedHeading()).toBe('the actual work');
    expect(trail()).toEqual(['Garden', '…', 'the overflow menu', 'the fold threshold']);
  });

  it('opens a found seed whose parent the push left out as a root of its own', async () => {
    const { daemon } = await openGarden([partOf('s-missed', 's-leaf11', 'the actual work'), loose]);
    fireEvent.change(searchField(), { target: { value: 'actual work' } });

    await openRow(daemon, 'the actual work');

    expect(seedHeading()).toBe('the actual work');
    expect(trail()).toEqual(['Garden']);
  });

  it('stacks in the dock and lays the walk out in columns in the window, keeping the reader’s place', async () => {
    layOutBlocksAcrossSizedAncestors(TWO_COLUMNS);
    const { daemon } = await openGarden(deep);
    await walk(daemon, 'the migration', 'one panel');
    expect(row('its header')).not.toBeNull();
    expect(row('one panel')).toBeNull();

    await click(daemon, 'Expand the garden');
    expect(seedHeading()).toBe('one panel');
    expect(row('one panel')).not.toBeNull();
    expect(row('its header')).not.toBeNull();

    await click(daemon, 'Return the garden to the dock');
    expect(seedHeading()).toBe('one panel');
    expect(row('one panel')).toBeNull();
    expect(row('its header')).not.toBeNull();
  });

  it('draws as many trailing levels as the width holds, and names only the ancestors no column shows', async () => {
    const { daemon } = await expandedGarden(TWO_COLUMNS);
    await walk(daemon, 'the migration');
    expect(row('unrelated work')).not.toBeNull();
    expect(row('one panel')).not.toBeNull();

    await walk(daemon, 'one panel');
    expect(row('unrelated work')).toBeNull();
    expect(row('its header')).not.toBeNull();
    expect(trail()).toEqual(['Garden', 'the migration']);
  });

  it('shows a third level once there is room for it', async () => {
    const { daemon } = await expandedGarden(THREE_COLUMNS);
    await walk(daemon, 'the migration', 'one panel');
    expect(row('unrelated work')).not.toBeNull();
    expect(trail()).toEqual(['Garden']);

    await walk(daemon, 'its header');
    expect(row('unrelated work')).toBeNull();
    expect(trail()).toEqual(['Garden', 'the migration']);
  });

  it('folds the trail once it outruns three steps, and opens it again', async () => {
    const { daemon } = await expandedGarden(TWO_COLUMNS);
    await walk(daemon, 'the migration', 'one panel', 'its header', 'the overflow menu', 'the fold threshold');
    expect(trail()).toEqual(['Garden', '…', 'its header', 'the overflow menu']);

    await click(daemon, 'Show 2 more steps');

    expect(trail()).toEqual(['Garden', 'the migration', 'one panel', 'its header', 'the overflow menu']);
  });

  it('switches siblings when a row in an earlier column is clicked', async () => {
    const sibling = plot('s-mid222', 'another panel', 's-crown1');
    const { daemon } = await expandedGarden(THREE_COLUMNS, [...deep, sibling]);
    await walk(daemon, 'the migration', 'one panel');

    await walk(daemon, 'another panel');

    expect(seedHeading()).toBe('another panel');
    expect(trail()).toEqual(['Garden']);
  });

  it('keeps the closed lens across a walk that leaves the column behind', async () => {
    const { daemon } = await expandedGarden(TWO_COLUMNS);
    await walk(daemon, 'the migration');
    await click(daemon, '1 closed');
    expect(row('already shipped')).not.toBeNull();

    await walk(daemon, 'one panel', 'its header');
    expect(row('already shipped')).toBeNull();

    await click(daemon, 'the migration');

    expect(row('already shipped')).not.toBeNull();
  });

  it('hands Escape to the frame without climbing the walk or clearing the question', async () => {
    const { daemon } = await openGarden(deep);
    await walk(daemon, 'the migration', 'one panel');
    fireEvent.change(searchField(), { target: { value: 'header' } });

    await escape(daemon);
    expect(gardenShown()).toBe(false);

    await gesture(daemon, () => pressShortcut('board.open'));
    expect(searchField()).toHaveValue('header');
    fireEvent.change(searchField(), { target: { value: '' } });
    expect(seedHeading()).toBe('one panel');

    await escape(daemon);
    expect(gardenShown()).toBe(false);
  });
});
