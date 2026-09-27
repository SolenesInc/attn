import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gardenRegion, openGarden, partOf, planted, plot, row, seedHeading } from './test/garden';
import { layOutBlocksAcrossSizedAncestors } from './test/layout';
import { gesture } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const crown = plot('s-crown1', 'the migration', '', { total: 2, ready: 2 });
const first = partOf('s-crown1', 's-one111', 'the first child');
const second = partOf('s-crown1', 's-two111', 'the second child');
const loose = planted('s-loose1', 'a loose seed');
const garden = [crown, first, second, loose];

const field = () => within(gardenRegion()).getByRole('combobox');

function keyTarget(): Element {
  const active = document.activeElement;
  return active && gardenRegion().contains(active) ? active : gardenRegion();
}

function press(daemon: ScriptedDaemon, key: string) {
  return gesture(daemon, () => fireEvent.keyDown(keyTarget(), { key }));
}

describe('App garden keyboard', () => {
  it('walks the rows of the place it is in, and climbs back out', async () => {
    const { daemon } = await openGarden(garden);

    await press(daemon, 'ArrowDown');
    expect(row('the migration')).toHaveFocus();
    await press(daemon, 'ArrowDown');
    expect(row('a loose seed')).toHaveFocus();
    await press(daemon, 'ArrowUp');
    expect(row('the migration')).toHaveFocus();

    await gesture(daemon, () => fireEvent.click(row('the migration')!));
    expect(seedHeading()).toBe('the migration');
    await press(daemon, 'ArrowLeft');
    expect(seedHeading()).toBeNull();
  });

  it('walks a column, drills right and climbs left', async () => {
    layOutBlocksAcrossSizedAncestors(1224);
    const { daemon } = await openGarden(garden);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Expand the garden' })));

    await press(daemon, 'ArrowDown');
    expect(row('the migration')).toHaveFocus();
    await press(daemon, 'ArrowRight');
    expect(seedHeading()).toBe('the migration');

    await press(daemon, 'ArrowDown');
    expect(row('the first child')).toHaveFocus();
    await press(daemon, 'ArrowDown');
    expect(row('the second child')).toHaveFocus();

    await press(daemon, 'ArrowLeft');
    expect(seedHeading()).toBeNull();
  });

  it('leaves the arrows to the search field while it has answers to walk', async () => {
    const { daemon } = await openGarden(garden);
    fireEvent.change(field(), { target: { value: 'child' } });
    field().focus();

    await press(daemon, 'ArrowDown');

    expect(field()).toHaveFocus();
    expect(field()).toHaveAttribute('aria-activedescendant', 'garden-row-s-two111');
  });

  it('hands the arrows back to the walk when the field has no answers to walk', async () => {
    const { daemon } = await openGarden(garden);
    field().focus();

    await press(daemon, 'ArrowDown');

    expect(row('the migration')).toHaveFocus();
  });

  it('returns to the search field from anywhere in the panel', async () => {
    const { daemon } = await openGarden(garden);
    await press(daemon, 'ArrowDown');
    expect(row('the migration')).toHaveFocus();

    await press(daemon, '/');

    expect(field()).toHaveFocus();
  });
});
