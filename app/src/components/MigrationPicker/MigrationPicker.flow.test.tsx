import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider } from '../../contexts/DaemonApiContext';
import { _resetEscapeStackForTest } from '../../hooks/useEscapeStack';
import { useProfilesStore } from '../../store/profiles';
import { MigrationPhase, ProfileErrorCode } from '../../types/generated';
import {
  commandError,
  confirm,
  fakeMigrationDaemon,
  importedGroup,
  migrationState,
  resetMigrationStore,
  slotsWith,
} from '../../test/migration';
import { MigrationGate } from './MigrationGate';
import { INTRO_SENTENCE } from './MigrationPicker';

function renderGate(daemon: ReturnType<typeof fakeMigrationDaemon>, extra = {}) {
  const api = daemon.daemonApi(extra);
  const view = render(
    <DaemonApiProvider api={api}>
      <MigrationGate>
        <div data-testid="normal-shell">shell</div>
      </MigrationGate>
    </DaemonApiProvider>,
  );
  return { ...view, api };
}

async function startPlacing(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByText(INTRO_SENTENCE);
  await user.keyboard('{Enter}');
  await screen.findByRole('heading', { name: 'Confirm your desktops' });
}

function sourceRow(title: string): HTMLElement {
  const row = screen.getByText(title, { selector: '.mp-source-name' }).closest<HTMLElement>('.mp-source');
  if (!row) throw new Error(`no source row for ${title}`);
  return row;
}

beforeEach(() => {
  resetMigrationStore();
});

afterEach(() => {
  _resetEscapeStackForTest();
  vi.restoreAllMocks();
});

describe('MigrationPicker', () => {
  it('reads the shared draft once per connection and shows the intro before any choice', async () => {
    const daemon = fakeMigrationDaemon(migrationState());
    const view = renderGate(daemon);

    expect(await screen.findByText(INTRO_SENTENCE)).toBeInTheDocument();
    expect(screen.queryByTestId('normal-shell')).not.toBeInTheDocument();
    expect(daemon.api.sendMigrationGet).toHaveBeenCalledTimes(1);

    view.rerender(
      <DaemonApiProvider api={daemon.daemonApi({ connectionGeneration: 2 })}>
        <MigrationGate>
          <div data-testid="normal-shell">shell</div>
        </MigrationGate>
      </DaemonApiProvider>,
    );
    await waitFor(() => expect(daemon.api.sendMigrationGet).toHaveBeenCalledTimes(2));
  });

  it('resumes on the board when the draft already holds a choice', async () => {
    const daemon = fakeMigrationDaemon(confirm(migrationState(), ['g1']));
    renderGate(daemon);

    expect(await screen.findByRole('heading', { name: 'Confirm your desktops' })).toBeInTheDocument();
    expect(screen.getByText('1 of 3', { exact: false })).toBeInTheDocument();
  });

  it('completes with the keyboard only: keep, merge through the dialog, bulk keep and finish', async () => {
    const user = userEvent.setup();
    const initial = migrationState();
    const daemon = fakeMigrationDaemon(initial);
    daemon.respond('migration_move', (state) => ({
      ...confirm(state, ['g2']),
      desktops: slotsWith(
        { 1: ['d1', { direction: 'horizontal', ratio: 0.5, children: [{ group: 'g1' }, { group: 'g2' }] }], 2: ['d2', null] },
        [['d3', { group: 'g3' }]],
      ),
    }));
    renderGate(daemon);
    await startPlacing(user);

    await user.keyboard('k');
    expect(daemon.api.sendMigrationKeep).toHaveBeenLastCalledWith(['g1'], 3);
    await screen.findByText('Daemon lifecycle stays on Desktop 1.');

    expect(sourceRow('Garden & crew')).toHaveClass('selected');
    await user.keyboard('1');
    const dialog = await screen.findByRole('dialog', { name: 'Merge Garden & crew' });
    await user.keyboard('{ArrowDown}');
    expect(within(dialog).getByRole('button', { name: 'Below ↓' })).toHaveAttribute('aria-pressed', 'true');
    await user.keyboard('{Enter}');
    expect(daemon.api.sendMigrationMove).toHaveBeenCalledWith({
      groupId: 'g2',
      targetKey: 'd1',
      anchorGroupId: undefined,
      edge: 'bottom',
      share: 0.5,
      expectedRevision: 4,
    });
    await screen.findByText('Garden & crew merged into Desktop 1.');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Keep the remaining 1 where they are' }));
    expect(daemon.api.sendMigrationKeep).toHaveBeenLastCalledWith(['g3'], 5);

    const finish = await screen.findByRole('button', { name: 'Finish →' });
    await waitFor(() => expect(finish).toBeEnabled());
    await user.click(finish);
    expect(daemon.api.sendMigrationFinish).toHaveBeenCalledWith(6);

    expect(await screen.findByRole('heading', { name: 'Your Default profile is ready.' })).toBeInTheDocument();
    expect(screen.getByText('You can now have different attn profiles, for example, one for work, and one for personal agents.')).toBeInTheDocument();
    expect(screen.queryByTestId('normal-shell')).not.toBeInTheDocument();
    await user.keyboard('{Enter}');
    expect(await screen.findByTestId('normal-shell')).toBeInTheDocument();
    expect(daemon.api.sendMigrationGet).toHaveBeenCalledTimes(1);
  });

  it('keeps Finish disabled while any group is unconfirmed and pulses the next required keep', async () => {
    const user = userEvent.setup();
    const daemon = fakeMigrationDaemon(migrationState());
    renderGate(daemon);
    await startPlacing(user);

    expect(screen.getByRole('button', { name: 'Finish →' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Keep Daemon lifecycle on Desktop 1' })).toHaveClass('pulse');

    await user.click(screen.getByRole('button', { name: 'Keep Daemon lifecycle on Desktop 1' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Keep Garden & crew on Desktop 2' })).toHaveClass('pulse'));
    expect(screen.getByRole('button', { name: 'Keep Side project as an extra desktop' })).not.toHaveClass('pulse');
  });

  it('renders another client’s edits as they arrive and cancels a merge the new draft invalidates', async () => {
    const user = userEvent.setup();
    const initial = migrationState();
    const daemon = fakeMigrationDaemon(initial);
    renderGate(daemon);
    await startPlacing(user);

    await user.click(sourceRow('Side project').querySelector('.mp-source-main')!);
    await user.keyboard('2');
    await screen.findByRole('dialog', { name: 'Merge Side project' });

    act(() => {
      daemon.broadcast({
        ...confirm(initial, ['g2']),
        desktops: slotsWith({ 1: ['d1', { group: 'g1' }], 2: ['d2', { group: 'g2' }] }, [['d3', { group: 'g3' }]]),
      });
    });
    expect(await screen.findByText('The draft changed in another window. Merging re-checks it before anything applies.')).toBeInTheDocument();
    expect(sourceRow('Garden & crew')).toHaveClass('confirmed');

    act(() => {
      daemon.broadcast({
        ...confirm(initial, ['g2']),
        revision: 5,
        desktops: slotsWith({ 1: ['d1', { group: 'g1' }], 2: ['d2', null] }, [['d3', { direction: 'vertical', ratio: 0.5, children: [{ group: 'g3' }, { group: 'g2' }] }]]),
      });
    });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.getByText('The draft changed in another window, so that move was cancelled. Nothing was applied.')).toBeInTheDocument();
    expect(daemon.api.sendMigrationMove).not.toHaveBeenCalled();
  });

  it('refuses a stale edit truthfully and re-reads the draft', async () => {
    const user = userEvent.setup();
    const daemon = fakeMigrationDaemon(migrationState());
    daemon.respond('migration_keep', () => commandError('migration_keep', ProfileErrorCode.StaleRevision, 'stale revision'));
    renderGate(daemon);
    await startPlacing(user);

    await user.keyboard('k');
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Another window changed the draft while you were editing. Showing the current draft; nothing was applied.',
    );
    await waitFor(() => expect(daemon.api.sendMigrationGet).toHaveBeenCalledTimes(2));
  });

  it('keeps every choice after a failed finish and finishes on retry', async () => {
    const user = userEvent.setup();
    const daemon = fakeMigrationDaemon(confirm(migrationState(), ['g1', 'g2', 'g3']));
    let failures = 1;
    daemon.respond('migration_finish', (state) => {
      if (failures-- > 0) return commandError('migration_finish', ProfileErrorCode.Internal, 'database is locked');
      return { ...state, phase: MigrationPhase.Complete, revision: state.revision + 1, groups: [], desktops: [] };
    });
    renderGate(daemon);

    await user.click(await screen.findByRole('button', { name: 'Finish →' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('database is locked. Nothing was applied. Retry when you’re ready.');
    expect(screen.getByText('3 of 3', { exact: false })).toBeInTheDocument();
    expect(useProfilesStore.getState().migrationPhase).toBe(MigrationPhase.PlacementRequired);

    await user.click(screen.getByRole('button', { name: 'Finish →' }));
    expect(await screen.findByRole('heading', { name: 'Your Default profile is ready.' })).toBeInTheDocument();
  });

  it('says when an imported group left the draft because its agents closed', async () => {
    const user = userEvent.setup();
    const initial = migrationState();
    const daemon = fakeMigrationDaemon(initial);
    renderGate(daemon);
    await startPlacing(user);

    act(() => {
      daemon.broadcast({
        ...initial,
        groups: initial.groups.filter((group) => group.group_id !== 'g3'),
        desktops: slotsWith({ 1: ['d1', { group: 'g1' }], 2: ['d2', { group: 'g2' }] }),
      });
    });
    expect(await screen.findByText('Side project has no agents left to place, so it no longer needs confirming.')).toBeInTheDocument();
    expect(screen.queryByText('Side project', { selector: '.mp-source-name' })).not.toBeInTheDocument();
  });

  it('orders drafts by revision within a connection and takes a new connection’s draft whatever its revision', async () => {
    const user = userEvent.setup();
    const initial = migrationState({ revision: 9 });
    const daemon = fakeMigrationDaemon(initial);
    renderGate(daemon);
    await startPlacing(user);

    act(() => daemon.broadcast({ ...confirm(initial, ['g1']), revision: 8 }));
    expect(sourceRow('Daemon lifecycle')).not.toHaveClass('confirmed');

    act(() => {
      useProfilesStore.getState().connectionReportedMigrationPhase(MigrationPhase.PlacementRequired);
      daemon.broadcast({ ...confirm(initial, ['g1']), revision: 2 });
    });
    await waitFor(() => expect(sourceRow('Daemon lifecycle')).toHaveClass('confirmed'));
  });

  it('moves a group with a pointer drag onto the nearest edge of another group', async () => {
    const user = userEvent.setup();
    const initial = migrationState();
    const daemon = fakeMigrationDaemon(initial);
    daemon.respond('migration_move', (state) => confirm(state, ['g3']));
    renderGate(daemon);
    await startPlacing(user);

    const target = document.querySelector<HTMLElement>('[data-migration-desktop="d1"] [data-migration-group="g1"]')!;
    vi.spyOn(target, 'getBoundingClientRect').mockReturnValue(new DOMRect(100, 100, 200, 100));
    const elementFromPoint = vi.fn(() => target);
    Object.defineProperty(document, 'elementFromPoint', { configurable: true, value: elementFromPoint });

    const source = sourceRow('Side project');
    fireEvent.pointerDown(source, { button: 0, pointerId: 7, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(document, { pointerId: 7, clientX: 280, clientY: 150 });
    expect(await screen.findByText('Merge right of Daemon lifecycle')).toBeInTheDocument();
    fireEvent.pointerUp(document, { pointerId: 7, clientX: 290, clientY: 150 });
    await screen.findByText('Side project merged into Desktop 1.');
    Reflect.deleteProperty(document, 'elementFromPoint');

    expect(daemon.api.sendMigrationMove).toHaveBeenCalledWith({
      groupId: 'g3',
      targetKey: 'd1',
      anchorGroupId: 'g1',
      edge: 'right',
      share: 0.5,
      expectedRevision: 3,
    });
  });

  it('shows the importing group’s agents and their launch state', async () => {
    const user = userEvent.setup();
    const initial = migrationState({
      groups: [
        importedGroup('g1', 'Daemon lifecycle', 'd1', { agents: 2 }),
        importedGroup('g2', 'Garden & crew', 'd2', { status: 'spawning' as never }),
        importedGroup('g3', 'Side project', 'd3'),
      ],
    });
    renderGate(fakeMigrationDaemon(initial));
    await startPlacing(user);

    expect(within(sourceRow('Daemon lifecycle')).getByText('2 agents', { exact: false })).toBeInTheDocument();
    expect(within(sourceRow('Garden & crew')).getByText('Launching')).toBeInTheDocument();
    expect(within(sourceRow('Side project')).getByText('Desktop 10')).toBeInTheDocument();
  });
});

describe('MigrationGate', () => {
  it('keeps the shell unmounted until the daemon reports a phase, and shows why it waits', () => {
    resetMigrationStore(null);
    const daemon = fakeMigrationDaemon(migrationState());
    const view = renderGate(daemon, { connectionError: 'Version mismatch: daemon v1, app v2.' });
    expect(screen.getByRole('status')).toHaveTextContent('Version mismatch: daemon v1, app v2.');
    expect(screen.queryByTestId('normal-shell')).not.toBeInTheDocument();

    view.rerender(
      <DaemonApiProvider api={daemon.daemonApi({ hasReceivedInitialState: true })}>
        <MigrationGate>
          <div data-testid="normal-shell">shell</div>
        </MigrationGate>
      </DaemonApiProvider>,
    );
    expect(screen.getByTestId('normal-shell')).toBeInTheDocument();
  });

  it('mounts the normal shell directly when the migration was already complete', () => {
    resetMigrationStore(MigrationPhase.Complete);
    renderGate(fakeMigrationDaemon(migrationState()));
    expect(screen.getByTestId('normal-shell')).toBeInTheDocument();
  });

  it('shows the done screen when another client finishes', async () => {
    const user = userEvent.setup();
    const daemon = fakeMigrationDaemon(migrationState());
    renderGate(daemon);
    await startPlacing(user);

    act(() => {
      daemon.broadcast({ ...migrationState(), phase: MigrationPhase.Complete, revision: 9, groups: [], desktops: [] });
    });
    expect(await screen.findByRole('heading', { name: 'Your Default profile is ready.' })).toBeInTheDocument();
    expect(screen.queryByTestId('normal-shell')).not.toBeInTheDocument();
  });
});
