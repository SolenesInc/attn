import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { agentPane, soloDesktop, daemonSession, daemonDesktop, type DaemonSession } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

const WORKSPACE = 'desktop-main';
const FIRES_AT = '2999-01-01T00:00:00.000Z';

const owed = { turn_owed: true, turn_opened_at: '2026-08-03T09:00:00Z' };

const splitDesktop = daemonDesktop(WORKSPACE, {
  root: {
    type: 'split',
    split_id: 'split-1',
    direction: 'horizontal',
    ratio: 0.5,
    children: [
      { type: 'pane', pane_id: 'pane-target' },
      { type: 'pane', pane_id: 'pane-other' },
    ],
  },
  panes: [agentPane('target', WORKSPACE), agentPane('other', WORKSPACE)],
});

async function showTargetBesideOther(target: Partial<DaemonSession>) {
  const { daemon } = await renderApp({
    initialState: {
      sessions: [
        daemonSession('target', { state: 'waiting_input', ...owed, ...target }),
        daemonSession('other', { state: 'idle' }),
      ],
      desktops: [splitDesktop],
    },
  });
  const agentList = screen.queryByRole('button', { name: /more agents/i });
  if (agentList) await gesture(daemon, () => fireEvent.click(agentList));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open other' })));
  return daemon;
}

async function queueTargetOffScreen(target: Partial<DaemonSession>) {
  const { daemon } = await renderApp({
    initialState: {
      settings: { queue_mode_enabled: 'true' },
      sessions: [
        daemonSession('target', { state: 'waiting_input', ...owed, ...target }),
        daemonSession('other', { state: 'idle' }),
      ],
      desktops: [soloDesktop('target'), soloDesktop('other')],
    },
  });
  const agentList = screen.queryByRole('button', { name: /more agents/i });
  if (agentList) await gesture(daemon, () => fireEvent.click(agentList));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open other' })));
  return daemon;
}

const header = () => within(document.querySelector<HTMLElement>('[data-pane-id="pane-target"]')!);
const headerFill = () => document.querySelector<HTMLElement>('[data-pane-id="pane-target"] .settling-header-track-fill')!;
const sidebarBar = () => within(screen.getByTestId('queue-turn-target')).queryByTestId('settling-sidebar-bar');

describe('App auto-settle', () => {
  it.each([
    ['counting down', { auto_settle_fires_at: FIRES_AT }, 'Keep this turn', 'Settling…', 'keep'],
    ['held while the user interacts', { auto_settle_held: true }, 'Keep this turn', 'Settling paused', 'keep'],
    ['already kept', { auto_settle_dismiss_armed: true }, 'Turn kept; undo to let it auto-settle again', 'Turn kept', 'undo'],
  ] as const)('says in words on the pane header when a turn is %s, and answers it from there without touching the pane', async (_, settle, name, words, verb) => {
    const daemon = await showTargetBesideOther(settle);
    const chip = header().getByRole('button', { name });

    expect(chip).toHaveTextContent(words);
    expect(chip).toHaveTextContent(`⌘.${verb}`);
    expect(chip.textContent!.replace('⌘.', '')).not.toMatch(/\d/);

    const before = daemon.sent.length;
    fireEvent.pointerDown(chip);
    await gesture(daemon, () => fireEvent.click(chip));

    expect(daemon.sent.slice(before)).toEqual([{ cmd: 'cancel_countdown', session_id: 'target' }]);
  });

  it('shows nothing on the pane header while no settle is pending', async () => {
    await showTargetBesideOther({});

    expect(header().queryByTestId('settling-indicator')).toBeNull();
    expect(header().queryByTestId('settle-kept-chip')).toBeNull();
  });


  it('holds the header track full and still while the settle is paused', async () => {
    await showTargetBesideOther({ auto_settle_held: true });

    expect(headerFill().style.transition).toBe('');
    expect(headerFill()).toHaveClass('settling-track-fill--held');
  });


  it('announces a settle on the queue row of an off-screen turn as a bare bar, held full while paused', async () => {
    const daemon = await queueTargetOffScreen({ auto_settle_fires_at: FIRES_AT });

    expect(sidebarBar()).toBeInTheDocument();
    expect(sidebarBar()!.textContent).toBe('');

    daemon.emit({ event: 'session_state_changed', session: daemonSession('target', { state: 'waiting_input', ...owed, auto_settle_held: true }) });
    await daemon.idle();

    const fill = sidebarBar()!.querySelector<HTMLElement>('.settling-sidebar-bar-fill')!;
    expect(fill.style.transition).toBe('');
    expect(fill).toHaveClass('settling-track-fill--held');
  });

  it('leaves the bar off the queue row of a turn already on screen', async () => {
    const daemon = await queueTargetOffScreen({ auto_settle_fires_at: FIRES_AT });

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open target' })));

    expect(sidebarBar()).toBeNull();
  });
});
