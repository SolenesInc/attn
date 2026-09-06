import { afterEach, describe, expect, it } from 'vitest';
import {
  armRefreshWitness,
  disarmRefreshWitness,
  readRefreshWitness,
} from './worktreesRefreshWitness';

const INDICATOR = '.worktrees-panel__refreshing';

function mountPanel(): HTMLElement {
  const panel = document.createElement('div');
  panel.innerHTML = '<ul><li class="row"></li><li class="row"></li></ul>';
  document.body.append(panel);
  return panel;
}

function showRefreshing(panel: HTMLElement, rows: number): void {
  panel.querySelectorAll('.row').forEach((row, index) => {
    if (index >= rows) return;
    const chip = document.createElement('span');
    chip.className = 'worktrees-panel__refreshing';
    row.append(chip);
  });
}

function hideRefreshing(panel: HTMLElement): void {
  panel.querySelectorAll(INDICATOR).forEach((chip) => chip.remove());
}

// MutationObserver callbacks are microtasks, so a turn of the event loop is
// what makes them run.
const flush = () => new Promise((resolve) => { setTimeout(resolve, 0); });

afterEach(() => {
  disarmRefreshWitness();
  document.body.innerHTML = '';
});

describe('worktrees refresh witness', () => {
  it('records a refresh that is gone before anyone reads the surface', async () => {
    const panel = mountPanel();
    armRefreshWitness(panel, INDICATOR);

    showRefreshing(panel, 2);
    await flush();
    hideRefreshing(panel);
    await flush();

    expect(panel.querySelectorAll(INDICATOR)).toHaveLength(0);
    const reading = readRefreshWitness();
    expect(reading?.sawRefreshing).toBe(true);
    expect(reading?.peakRefreshing).toBe(2);
  });

  it('sees a refresh that starts and ends inside one batch of mutations', async () => {
    const panel = mountPanel();
    armRefreshWitness(panel, INDICATOR);

    showRefreshing(panel, 1);
    hideRefreshing(panel);
    await flush();

    expect(readRefreshWitness()?.sawRefreshing).toBe(true);
  });

  it('counts a refresh already on screen when it is armed', () => {
    const panel = mountPanel();
    showRefreshing(panel, 1);

    const reading = armRefreshWitness(panel, INDICATOR);

    expect(reading.sawRefreshing).toBe(true);
    expect(reading.firstSeenMs).toBe(0);
  });

  it('says nothing was seen when the surface never showed a refresh', async () => {
    const panel = mountPanel();
    armRefreshWitness(panel, INDICATOR);

    panel.querySelector('.row')?.append(document.createElement('span'));
    await flush();

    const reading = readRefreshWitness();
    expect(reading?.sawRefreshing).toBe(false);
    expect(reading?.mutations).toBeGreaterThan(0);
  });

  it('starts clean when it is armed again, and reads nothing once disarmed', async () => {
    const panel = mountPanel();
    armRefreshWitness(panel, INDICATOR);
    showRefreshing(panel, 1);
    await flush();
    expect(readRefreshWitness()?.sawRefreshing).toBe(true);

    hideRefreshing(panel);
    armRefreshWitness(panel, INDICATOR);
    expect(readRefreshWitness()?.sawRefreshing).toBe(false);

    disarmRefreshWitness();
    expect(readRefreshWitness()).toBeNull();
  });
});
