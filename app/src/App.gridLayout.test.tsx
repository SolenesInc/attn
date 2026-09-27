import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, onTestFinished } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp, restartApp, type AppRender } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const SESSIONS = ['s1', 's2', 's3'];

function layOutGridStage(width: number, height: number) {
  for (const [axis, size] of [['clientWidth', width], ['clientHeight', height]] as const) {
    const native = Object.getOwnPropertyDescriptor(HTMLElement.prototype, axis)!;
    Object.defineProperty(HTMLElement.prototype, axis, {
      configurable: true,
      get(this: HTMLElement) {
        return this.classList.contains('grid-view-stage') ? size : native.get!.call(this);
      },
    });
    onTestFinished(() => {
      Object.defineProperty(HTMLElement.prototype, axis, native);
    });
  }
}

const LAUNCH = {
  initialState: {
    sessions: SESSIONS.map((id) => daemonSession(id, { state: 'idle' })),
    workspaces: SESSIONS.map(agentWorkspace),
  },
};

async function launchIntoGrid(running?: AppRender) {
  const view = running ? await restartApp(running, LAUNCH) : await renderApp(LAUNCH);
  await gesture(view.daemon, () => pressShortcut('view.toggleGrid'));
  expect(screen.getByRole('region', { name: 'Session grid' })).toBeInTheDocument();
  return view;
}

async function pickLayout(daemon: ScriptedDaemon, choice: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Grid layout' })));
  await gesture(daemon, () => fireEvent.click(choice === 'Auto' ? screen.getByRole('button', { name: 'Auto' }) : screen.getByRole('button', { name: choice })));
}

async function shownLayout(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Grid layout' })));
  const label = document.querySelector('.grid-layout-label')?.textContent;
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Grid layout' })));
  return label;
}

const offBoard = () => document.querySelector('.grid-view-offboard')?.textContent ?? null;

describe('App grid layout', () => {
  it('keeps the grid shape the user picked across restarts', async () => {
    const first = await launchIntoGrid();
    expect(await shownLayout(first.daemon)).toBe('Auto');
    expect(offBoard()).toBeNull();

    await pickLayout(first.daemon, '1 by 2');
    expect(offBoard()).toBe('1 more session not shown · enlarge the grid or pick Auto');

    const second = await launchIntoGrid(first);
    expect(await shownLayout(second.daemon)).toBe('1 × 2');
    expect(offBoard()).toBe('1 more session not shown · enlarge the grid or pick Auto');

    await pickLayout(second.daemon, 'Auto');

    const third = await launchIntoGrid(second);
    expect(await shownLayout(third.daemon)).toBe('Auto');
    expect(offBoard()).toBeNull();
  });

  it('keeps a session the user removed off the grid across restarts', async () => {
    layOutGridStage(900, 600);
    const removeTopLeftTile = (daemon: ScriptedDaemon) => {
      fireEvent.mouseMove(screen.getByRole('region', { name: 'Session grid' }), { clientX: 200, clientY: 250 });
      return gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Remove from grid' })));
    };
    const first = await launchIntoGrid();
    await removeTopLeftTile(first.daemon);
    expect(screen.getByRole('button', { name: '1 hidden' })).toBeInTheDocument();

    const second = await launchIntoGrid(first);
    expect(screen.getByRole('button', { name: '1 hidden' })).toBeInTheDocument();
    await removeTopLeftTile(second.daemon);
    await gesture(second.daemon, () => fireEvent.click(screen.getByRole('button', { name: '2 hidden' })));
    expect(screen.getByTitle('Restore s1')).toBeInTheDocument();
    expect(screen.getByTitle('Restore s2')).toBeInTheDocument();
  });
});
