import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { openGarden, planted, renderGarden } from './test/garden';
import { gesture, pressShortcut } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const seeds = [planted('s-alone1', 'unrelated work')];

const list = () => screen.queryByRole('region', { name: 'The garden' });
const board = () => screen.queryByRole('region', { name: 'The garden board' });
const viewSwitch = () => screen.queryByRole('group', { name: 'Garden view' });
const fullscreen = () => screen.queryByRole('dialog', { name: 'The garden' });

function shown(): 'closed' | 'dock' | 'full' {
  if (!list() && !board()) return 'closed';
  return fullscreen() ? 'full' : 'dock';
}

async function click(daemon: ScriptedDaemon, name: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name })));
}

const toggleFrame = (daemon: ScriptedDaemon) => gesture(daemon, () => pressShortcut('board.open'));
const escape = (daemon: ScriptedDaemon) => gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));

describe('App garden frame', () => {
  it.each([
    ['attention', 'list'],
    ['attention', 'board'],
    ['garden', 'list'],
    ['garden', 'board'],
  ])('preserves opening order after restoring the garden (%s first, %s fullscreen)', async (first, view) => {
    const { daemon } = await renderGarden(seeds);
    const attention = () => gesture(daemon, () => pressShortcut('dock.attention'));
    if (first === 'attention') {
      await attention();
      await click(daemon, 'Show the garden');
    } else {
      await click(daemon, 'Show the garden');
      await attention();
    }
    await click(daemon, 'Expand the garden');
    if (view === 'board') await click(daemon, 'board');
    if (view === 'board') await toggleFrame(daemon);
    else await click(daemon, 'Return the garden to the dock');

    await escape(daemon);

    expect(shown()).toBe(first === 'attention' ? 'closed' : 'dock');
    expect(screen.getByRole('button', { name: first === 'attention' ? 'Hide PRs Drawer' : 'Show PRs Drawer' })).toBeInTheDocument();
    await escape(daemon);
    expect(shown()).toBe('closed');
    expect(screen.getByRole('button', { name: 'Show PRs Drawer' })).toBeInTheDocument();
  });

  it('dismisses an expanded garden before a dock panel opened after its list', async () => {
    const { daemon } = await openGarden(seeds);
    await gesture(daemon, () => pressShortcut('dock.attention'));
    await click(daemon, 'Expand the garden');

    await escape(daemon);

    expect(shown()).toBe('closed');
    expect(screen.getByText('Needs Attention')).toBeVisible();
    await escape(daemon);
    expect(screen.queryByRole('button', { name: 'Hide PRs Drawer' })).toBeNull();
  });

  it('holds the window as a modal only once expanded, and offers the board only there', async () => {
    const { daemon } = await openGarden(seeds);
    expect(shown()).toBe('dock');
    expect(viewSwitch()).toBeNull();

    await click(daemon, 'Expand the garden');

    expect(fullscreen()).toHaveAttribute('aria-modal', 'true');
    expect(viewSwitch()).not.toBeNull();
  });

  it('reopens in the frame the user last chose, however it was closed', async () => {
    const { daemon } = await openGarden(seeds);
    await toggleFrame(daemon);
    expect(shown()).toBe('full');

    await escape(daemon);
    expect(shown()).toBe('closed');
    await toggleFrame(daemon);
    expect(shown()).toBe('full');

    await click(daemon, 'Return the garden to the dock');
    await click(daemon, 'Close');
    expect(shown()).toBe('closed');
    await toggleFrame(daemon);
    expect(shown()).toBe('dock');
  });

  it('opens and closes from the sidebar icon without turning a close into a frame change', async () => {
    const { daemon } = await openGarden(seeds);
    await toggleFrame(daemon);
    await escape(daemon);

    await click(daemon, 'Show the garden');
    expect(shown()).toBe('full');

    await click(daemon, 'Return the garden to the dock');
    await click(daemon, 'Hide the garden');
    expect(shown()).toBe('closed');

    await click(daemon, 'Show the garden');
    expect(shown()).toBe('dock');
  });

  it('keeps the chosen fullscreen view through dismissal, the dock, and a relaunch', async () => {
    const first = await openGarden(seeds);
    await toggleFrame(first.daemon);
    await click(first.daemon, 'board');
    expect(board()).not.toBeNull();

    await escape(first.daemon);
    expect(shown()).toBe('closed');
    await toggleFrame(first.daemon);
    expect(board()).not.toBeNull();

    await toggleFrame(first.daemon);
    expect(shown()).toBe('dock');
    expect(board()).toBeNull();
    expect(list()).not.toBeNull();

    await toggleFrame(first.daemon);
    expect(board()).not.toBeNull();
    first.unmount();

    const relaunched = await renderGarden(seeds);
    await toggleFrame(relaunched.daemon);
    expect(shown()).toBe('full');
    expect(board()).not.toBeNull();
  });
});
