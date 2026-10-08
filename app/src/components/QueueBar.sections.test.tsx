import type { ComponentProps } from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, onTestFinished, vi } from 'vitest';
import { Sidebar } from './Sidebar';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import type { AutomationProvenance } from '../types/generated';
import { buildQueueBands } from '../utils/queueBands';
import { agentDesktop, arrangeDesktops, desktopGroups, type TestDesktopGroup } from '../test/desktops';

interface TestSession {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
  desktopId?: string;
  chiefOfStaff?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  turnSnoozedUntil?: string;
  crewMember?: string;
  automation?: AutomationProvenance;
}

const baseProps = {
  selectedId: null,
  selectedDesktopId: null,
  collapsed: true,
  surface: 'queue-collapsed' as const,
  headerActions: [],
  onSelectSession: () => {},
  onSelectDesktop: () => {},
  onNewSession: () => {},
  onCloseSession: () => {},
  onReloadSession: () => {},
  onGoToDashboard: () => {},
  onToggleCollapse: () => {},
};

const fixtureDesktops: TestDesktopGroup[] = [
  { id: 'ws-a', title: 'alpha' },
  { id: 'ws-b', title: 'beta' },
];

function owed(id: string, hour: number, desktopId = 'ws-a'): TestSession {
  return {
    id,
    label: id,
    state: 'waiting_input',
    desktopId,
    turnOwed: true,
    turnOpenedAt: `2026-07-26T${String(hour).padStart(2, '0')}:00:00Z`,
  };
}

function run(id: string, definition: string, needsYou = false): TestSession {
  return {
    id,
    label: id,
    state: needsYou ? 'waiting_input' : 'working',
    turnOwed: needsYou,
    turnOpenedAt: needsYou ? '2026-07-26T08:00:00Z' : undefined,
    desktopId: 'ws-a',
    automation: {
      definition_id: definition === 'nightly' ? 1 : 2,
      definition_name: definition === 'nightly' ? 'Nightly docs sweep' : 'PR reviewer',
      run_id: `run-${id}`,
      trigger_type: 'schedule',
    },
  };
}

function renderBar(
  sessions: TestSession[],
  overrides: Partial<ComponentProps<typeof Sidebar>> = {},
  groups: TestDesktopGroup[] = fixtureDesktops,
) {
  const desktops = desktopGroups(groups, sessions);
  const slots = overrides.visualIndexByDesktopId ?? new Map(groups.map((group, index) => [group.id, index]));
  return render(
    <Sidebar
      {...baseProps}
      desktops={desktops}
      {...overrides}
      visualIndexByDesktopId={slots}
      queue={buildQueueBands(desktops)}
    />,
  );
}

function hover(testId: string) {
  fireEvent.pointerEnter(screen.getByTestId(testId).parentElement!);
}

function peekRowKeys(peek: HTMLElement) {
  return Array.from(peek.querySelectorAll('.queue-bar-peek-row, .unified-palette-divider')).map((row) =>
    row.classList.contains('unified-palette-divider') ? '—' : row.getAttribute('data-testid')!.replace('queue-bar-peek-', ''),
  );
}

describe('the queue bar', () => {
  it('replaces the open queue sidebar when collapsed and shows it again from ⇥', () => {
    const onToggleCollapse = vi.fn();
    renderBar([owed('a', 9)], { onToggleCollapse });

    expect(screen.getByTestId('queue-bar')).toBeTruthy();
    expect(screen.queryByTestId('queue-sidebar')).toBeNull();
    expect(document.querySelector('.icon-rail')).toBeNull();
    fireEvent.click(screen.getByTestId('queue-bar-show-sidebar'));
    expect(onToggleCollapse).toHaveBeenCalledOnce();
  });

  it('names the profile and switches it', () => {
    const onSwitchProfile = vi.fn();
    renderBar([], { profileName: 'Work', onSwitchProfile });

    fireEvent.click(screen.getByTestId('queue-profile-pill'));
    expect(screen.getByTestId('queue-profile-pill').textContent).toContain('Work');
    expect(onSwitchProfile).toHaveBeenCalledOnce();
  });
});

describe('the waiting pill', () => {
  it('counts the turns and crumbs the three oldest, then +k', () => {
    renderBar([owed('d', 12), owed('a', 9), owed('c', 11), owed('b', 10), owed('e', 13)]);

    const pill = screen.getByTestId('queue-bar-pill');
    expect(pill.getAttribute('data-waiting')).toBe('5');
    expect(Array.from(pill.querySelectorAll('.queue-bar-crumb')).map((crumb) => crumb.textContent)).toEqual([
      'a',
      'b',
      'c',
      '+2',
    ]);
    expect(pill.textContent).toContain('5 waiting');
  });

  it('reads nothing owed with no turns', () => {
    renderBar([{ id: 'w', label: 'w', state: 'working', desktopId: 'ws-a' }]);
    expect(screen.getByTestId('queue-bar-pill').textContent).toContain('nothing owed');
  });

  it('opens the palette on agents when clicked', () => {
    const onOpenAgents = vi.fn();
    renderBar([owed('a', 9)], { onOpenAgents });
    fireEvent.click(screen.getByTestId('queue-bar-pill'));
    expect(onOpenAgents).toHaveBeenCalledOnce();
  });

  it('leaves runs out of the pill', () => {
    renderBar([owed('a', 9), run('r1', 'nightly', true)]);
    expect(screen.getByTestId('queue-bar-pill').getAttribute('data-waiting')).toBe('1');
  });
});

describe('the waiting peek', () => {
  const crewAndBands: TestSession[] = [
    { id: 'chief', label: 'chief', state: 'idle', desktopId: 'ws-a', chiefOfStaff: true },
    owed('newer', 11, 'ws-b'),
    owed('older', 9),
    { id: 'busy', label: 'busy', state: 'working', desktopId: 'ws-a' },
    { id: 'loose', label: 'loose', state: 'working', desktopId: 'ws-b' },
    run('r1', 'nightly', true),
  ];

  it('lists the crew block, a divider, then turns and working, without runs', () => {
    renderBar(crewAndBands, { crew: [{ id: 'scout' }] });
    expect(screen.queryByTestId('queue-bar-waiting-peek')).toBeNull();

    hover('queue-bar-pill');
    const peek = screen.getByTestId('queue-bar-waiting-peek');
    expect(peekRowKeys(peek)).toEqual([
      'agent:chief',
      'member:scout',
      '—',
      'agent:older',
      'agent:newer',
      'agent:busy',
      'agent:loose',
    ]);
  });

  it('tags the head of the queue and shows where each agent lives', () => {
    renderBar(crewAndBands, { visualIndexByDesktopId: new Map([['ws-a', 0]]) });
    hover('queue-bar-pill');

    const row = (key: string) => screen.getByTestId(`queue-bar-peek-agent:${key}`);
    expect(row('older').querySelector('.unified-palette-tag kbd')).toBeTruthy();
    expect(row('newer').querySelector('.unified-palette-tag kbd')).toBeNull();
    expect(row('older').querySelector('.unified-palette-slot')!.textContent).toBe('⌘1');
    expect(row('newer').querySelector('.unified-palette-slot')!.textContent).toBe('·');
    expect(row('loose').querySelector('.unified-palette-slot')!.textContent).toBe('·');
    expect(row('older').querySelector('.unified-palette-pill')!.textContent).toBe('waiting');
  });

  it('lists tiles after the agents', () => {
    const withTile: TestDesktopGroup[] = [
      {
        id: 'ws-a',
        title: 'alpha',
        tree: {
          type: 'split',
          split_id: 'split-root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', pane_id: 'pane-older' },
            { type: 'tile', tile_id: 'tile-doc', tile_kind: 'markdown', tile_params: '/notes/plan.md' },
          ],
        },
      },
    ];
    const onSelectTile = vi.fn();
    renderBar([owed('older', 9)], { onSelectTile }, withTile);
    hover('queue-bar-pill');

    expect(peekRowKeys(screen.getByTestId('queue-bar-waiting-peek'))).toEqual(['agent:older', 'tile:ws-a:tile-doc']);
    fireEvent.click(screen.getByTestId('queue-bar-peek-tile:ws-a:tile-doc'));
    expect(onSelectTile).toHaveBeenCalledWith('ws-a', 'tile-doc');
  });

  it('shows sixteen agents and counts the rest', () => {
    const many = Array.from({ length: 20 }, (_, index) => owed(`t${String(index).padStart(2, '0')}`, index));
    renderBar(many);
    hover('queue-bar-pill');

    const peek = screen.getByTestId('queue-bar-waiting-peek');
    expect(peek.querySelectorAll('.queue-bar-peek-row')).toHaveLength(16);
    expect(screen.getByTestId('queue-bar-peek-more')).toHaveTextContent(/^4 more\b/);
  });

  it('opens an agent, wakes a member, and closes after either', () => {
    const onSelectSession = vi.fn();
    const onWakeCrewMember = vi.fn();
    renderBar(crewAndBands, { crew: [{ id: 'scout' }], onSelectSession, onWakeCrewMember });

    hover('queue-bar-pill');
    fireEvent.click(screen.getByTestId('queue-bar-peek-agent:older'));
    expect(onSelectSession).toHaveBeenCalledWith('older');
    expect(screen.queryByTestId('queue-bar-waiting-peek')).toBeNull();

    hover('queue-bar-pill');
    fireEvent.click(screen.getByTestId('queue-bar-peek-member:scout'));
    expect(onWakeCrewMember).toHaveBeenCalledWith('scout');
  });

  it('closes when the pointer leaves and stays silent under an overlay', () => {
    const { rerender } = renderBar(crewAndBands);
    const anchor = screen.getByTestId('queue-bar-pill').parentElement!;

    fireEvent.pointerEnter(anchor);
    fireEvent.pointerLeave(anchor);
    expect(screen.queryByTestId('queue-bar-waiting-peek')).toBeNull();

    fireEvent.pointerEnter(anchor);
    const desktops = desktopGroups(fixtureDesktops, crewAndBands);
    rerender(
      <Sidebar
        {...baseProps}
        desktops={desktops}
        visualIndexByDesktopId={new Map([['ws-a', 0], ['ws-b', 1]])}
        queue={buildQueueBands(desktops)}
        peeksSilenced
      />,
    );
    expect(screen.queryByTestId('queue-bar-waiting-peek')).toBeNull();

    rerender(
      <Sidebar
        {...baseProps}
        desktops={desktops}
        visualIndexByDesktopId={new Map([['ws-a', 0], ['ws-b', 1]])}
        queue={buildQueueBands(desktops)}
      />,
    );
    expect(screen.queryByTestId('queue-bar-waiting-peek')).toBeNull();
  });

  it('closes when the pill is clicked to open the palette', () => {
    const onOpenAgents = vi.fn();
    renderBar(crewAndBands, { onOpenAgents });
    hover('queue-bar-pill');
    expect(screen.getByTestId('queue-bar-waiting-peek')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('queue-bar-pill'));
    expect(onOpenAgents).toHaveBeenCalledOnce();
    expect(screen.queryByTestId('queue-bar-waiting-peek')).toBeNull();
  });

  it('closes when the pill is activated from the keyboard', () => {
    renderBar(crewAndBands, { onOpenAgents: vi.fn() });
    hover('queue-bar-pill');
    expect(screen.getByTestId('queue-bar-waiting-peek')).toBeInTheDocument();

    fireEvent.keyDown(screen.getByTestId('queue-bar-pill'), { key: 'Enter' });
    expect(screen.queryByTestId('queue-bar-waiting-peek')).toBeNull();
  });
});

describe('the runs chip', () => {
  const runs = [run('n1', 'nightly', true), run('n2', 'nightly'), run('p1', 'reviewer', true), owed('a', 9)];

  it('is absent with no runs', () => {
    renderBar([owed('a', 9)]);
    expect(screen.queryByTestId('queue-bar-runs')).toBeNull();
  });

  it('counts the runs, badges the ones needing you, and walks them on click', () => {
    const onWalkRuns = vi.fn();
    renderBar(runs, { onWalkRuns });

    const chip = screen.getByTestId('queue-bar-runs');
    expect(chip.getAttribute('data-runs')).toBe('3');
    expect(chip.querySelector('.queue-bar-runs-needing')!.textContent).toBe('2');
    fireEvent.click(chip);
    expect(onWalkRuns).toHaveBeenCalledOnce();
  });

  it('comes back closed after its last run leaves while the peek was open', () => {
    const { rerender } = renderBar(runs);
    hover('queue-bar-runs');
    expect(screen.getByTestId('queue-bar-runs-peek')).toBeInTheDocument();

    const without = desktopGroups(fixtureDesktops, [owed('a', 9)]);
    rerender(<Sidebar {...baseProps} desktops={without} visualIndexByDesktopId={new Map(fixtureDesktops.map((group, index) => [group.id, index]))} queue={buildQueueBands(without)} />);
    expect(screen.queryByTestId('queue-bar-runs')).toBeNull();

    const back = desktopGroups(fixtureDesktops, runs);
    rerender(<Sidebar {...baseProps} desktops={back} visualIndexByDesktopId={new Map(fixtureDesktops.map((group, index) => [group.id, index]))} queue={buildQueueBands(back)} />);
    expect(screen.getByTestId('queue-bar-runs')).toBeInTheDocument();
    expect(screen.queryByTestId('queue-bar-runs-peek')).toBeNull();
  });

  it('closes its peek when clicked to walk to the next run', () => {
    renderBar(runs, { onWalkRuns: vi.fn() });
    hover('queue-bar-runs');
    expect(screen.getByTestId('queue-bar-runs-peek')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('queue-bar-runs'));
    expect(screen.queryByTestId('queue-bar-runs-peek')).toBeNull();
  });

  it('peeks every run by definition and tags the one the walk opens next from the agent on screen', () => {
    const onSelectSession = vi.fn();
    arrangeDesktops([agentDesktop('d1', 1, ['n1'])]);
    useSessionStore.setState({ view: 'session' });
    onTestFinished(() => useProfilesStore.setState(useProfilesStore.getInitialState(), true));
    renderBar(runs, { onSelectSession, selectedId: null });
    hover('queue-bar-runs');

    const peek = screen.getByTestId('queue-bar-runs-peek');
    const nightly = within(peek).getByTestId('queue-bar-runs-group-1');
    expect(nightly.querySelector('.queue-bar-peek-group')!.textContent).toBe('Nightly docs sweep1 need you · 2 runs');
    expect(within(peek).getByTestId('queue-bar-runs-group-2').textContent).toContain('1 need you · 1 run');
    expect(screen.getByTestId('queue-bar-peek-run-p1').querySelector('.unified-palette-tag kbd')).toBeTruthy();
    expect(screen.getByTestId('queue-bar-peek-run-n1').querySelector('.unified-palette-tag kbd')).toBeNull();
    expect(peek.textContent).toContain('Automation runs never join the queue');

    fireEvent.click(screen.getByTestId('queue-bar-peek-run-n2'));
    expect(onSelectSession).toHaveBeenCalledWith('n2');
    expect(screen.queryByTestId('queue-bar-runs-peek')).toBeNull();
  });
});

describe('the desktop chips', () => {
  const threeDesktops: TestDesktopGroup[] = [...fixtureDesktops, { id: 'ws-c', title: 'gamma' }, { id: 'ws-x', title: 'extra' }];
  const slots = new Map([['ws-a', 0], ['ws-b', 1], ['ws-c', 2]]);

  it('badges each desktop with its owed turns and marks the current one', () => {
    renderBar(
      [
        owed('a1', 9),
        owed('a2', 10),
        { id: 'b', label: 'b', state: 'waiting_input', desktopId: 'ws-b' },
        owed('x', 11, 'ws-x'),
      ],
      { visualIndexByDesktopId: slots, selectedDesktopId: 'ws-b' },
      threeDesktops,
    );

    const chip = (slot: number) => screen.getByTestId(`queue-bar-desktop-${slot}`);
    expect(chip(1).querySelector('.queue-bar-desktop-waiting')!.textContent).toBe('2');
    expect(chip(2).querySelector('.queue-bar-desktop-waiting')).toBeNull();
    expect(chip(2).classList.contains('is-current')).toBe(true);
    expect(chip(3).classList.contains('has-panes')).toBe(false);
    const extras = screen.getByTestId('queue-bar-desktop-extras');
    expect(extras.textContent).toMatch(/^\+1/);
    expect(extras.querySelector('.queue-bar-desktop-waiting')!.textContent).toBe('1');
  });

  it('switches desktops and opens the overview from the extras', () => {
    const onSelectDesktop = vi.fn();
    const onOpenOverview = vi.fn();
    renderBar([], { visualIndexByDesktopId: slots, onSelectDesktop, onOpenOverview }, threeDesktops);

    fireEvent.click(screen.getByTestId('queue-bar-desktop-2'));
    fireEvent.click(screen.getByTestId('queue-bar-desktop-extras'));
    expect(onSelectDesktop).toHaveBeenCalledWith('ws-b');
    expect(onOpenOverview).toHaveBeenCalledOnce();
  });

  it('takes a dragged pane on another desktop', () => {
    const onDesktopDragDrop = vi.fn();
    renderBar([owed('a1', 9)], {
      leafDrag: { sourceDesktopId: 'ws-a' },
      onDesktopDragDrop,
    });

    const target = screen.getByTestId('queue-bar-desktop-2');
    expect(target.classList.contains('is-drop-target')).toBe(true);
    fireEvent.pointerUp(target);
    expect(onDesktopDragDrop).toHaveBeenCalledWith(expect.objectContaining({ id: 'ws-b' }), false);
  });
});
