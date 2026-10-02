import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { EventMessage } from './test/protocol';
import { soloDesktop, daemonSession } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

type Definition = NonNullable<EventMessage<'automation_definitions_result'>['definitions']>[number];
type Run = NonNullable<EventMessage<'automation_runs_result'>['runs']>[number];

const AT = '2026-01-01T00:00:00Z';

function definition(id: string, name: string, over: Partial<Definition> = {}): Definition {
  return { id, profile_id: 'default', name, enabled: true, revision: 1, trigger_type: 'manual', updated_at: AT, ...over };
}

function run(id: string, over: Partial<Run> = {}): Run {
  return { id, definition_id: 'd1', state: 'delivered', created_at: AT, updated_at: AT, ...over };
}

interface Scene {
  definitions: Definition[];
  runs?: Run[];
}

function serveAutomations(daemon: ScriptedDaemon, scene: Scene) {
  daemon.on('automation_definitions_get', () => ({ event: 'automation_definitions_result', success: true, definitions: scene.definitions }));
  daemon.on('automation_runs_get', ({ definition_id }) => ({ event: 'automation_runs_result', success: true, definition_id, runs: scene.runs ?? [] }));
}

async function openAutomations(scene: Scene, script: (daemon: ScriptedDaemon) => void = () => {}) {
  const view = await renderApp({
    initialState: { sessions: [daemonSession('s1'), daemonSession('s2')], desktops: [soloDesktop('s1'), soloDesktop('s2')] },
  });
  serveAutomations(view.daemon, scene);
  script(view.daemon);
  await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Show Automations' })));
  return view.daemon;
}

const changed = async (daemon: ScriptedDaemon) => {
  daemon.emit({ event: 'automations_changed', definition_ids: ['d1'] });
  await daemon.idle();
};

const runRequestIds = (daemon: ScriptedDaemon) => daemon.sentOf('automation_run').map(({ request_id }) => request_id);

const refuseRuns = (daemon: ScriptedDaemon, error: string) =>
  daemon.on('automation_run', () => ({ event: 'automation_run_result', success: false, error }));

const selectedAgent = () => document.querySelector('.session-item.selected .session-label')?.textContent ?? null;

describe('App automations panel', () => {
  it('reads nothing until the panel opens', async () => {
    const { daemon } = await renderApp();
    await daemon.idle();

    expect(daemon.sentOf('automation_definitions_get')).toEqual([]);
  });

  it('offers to create the first automation in the app rather than pointing at the CLI', async () => {
    await openAutomations({ definitions: [] });

    expect(screen.getByTestId('automations-panel-empty')).toBeInTheDocument();
    expect(screen.queryByText(/attn automation apply/i)).toBeNull();
    expect(screen.getByTestId('automation-new-empty')).toBeInTheDocument();
  });

  it('lists each definition with what triggers it, offering Run now only for manual ones', async () => {
    await openAutomations({
      definitions: [
        definition('d1', 'PR reviewer'),
        definition('d2', 'Nightly digest', { trigger_type: 'scheduled', schedule_cron: '0 9 * * *', schedule_time_zone: 'UTC' }),
        definition('d3', 'Recurring reviewer', { trigger_type: 'github_review_requested' }),
      ],
    });

    expect(screen.getAllByTestId('automation-definition-row')).toHaveLength(3);
    expect(screen.getByText('Scheduled — 0 9 * * * (UTC)')).toBeInTheDocument();
    expect(screen.getByText('GitHub')).toBeInTheDocument();
    expect(screen.getByTestId('automation-run-now-d1')).toBeInTheDocument();
    expect(screen.queryByTestId('automation-run-now-d2')).toBeNull();
    expect(screen.queryByTestId('automation-run-now-d3')).toBeNull();
  });

  it('marks a definition whose last run failed, with the reason', async () => {
    await openAutomations({
      definitions: [definition('d1', 'PR reviewer', { last_run: run('r1', { state: 'failed', last_error: 'boom' }) })],
    });

    expect(screen.getByTestId('automation-failure-badge-d1')).toBeInTheDocument();
    expect(screen.getByText('boom')).toBeInTheDocument();
  });

  it('lists the definitions again when the daemon says automations changed', async () => {
    const scene: Scene = { definitions: [definition('d1', 'PR reviewer')] };
    const daemon = await openAutomations(scene);
    const listed = daemon.sentOf('automation_definitions_get').length;

    scene.definitions = [...scene.definitions, definition('d2', 'Nightly sweep')];
    await changed(daemon);

    expect(daemon.sentOf('automation_definitions_get')).toHaveLength(listed + 1);
    expect(screen.getByText('Nightly sweep')).toBeInTheDocument();
  });

  it('shows a newly delivered run of the selected definition without reselecting it', async () => {
    const scene: Scene = { definitions: [definition('d1', 'PR reviewer')], runs: [run('r1', { state: 'pending' })] };
    const daemon = await openAutomations(scene);
    await gesture(daemon, () => fireEvent.click(screen.getByText('PR reviewer')));
    expect(screen.getAllByTestId('automation-run-row')).toHaveLength(1);

    scene.runs = [run('r1'), run('r2')];
    await changed(daemon);

    expect(screen.getAllByTestId('automation-run-row')).toHaveLength(2);
    expect(daemon.sentOf('automation_runs_get').map(({ definition_id }) => definition_id)).toEqual(['d1', 'd1']);
  });

  describe('run history', () => {
    it('opens the session a run started, even when the run also planted a seed', async () => {
      const daemon = await openAutomations({ definitions: [definition('d1', 'PR reviewer')], runs: [run('r1', { session_id: 's2', seed_id: 's-seed01' })] });
      await gesture(daemon, () => fireEvent.click(screen.getByText('PR reviewer')));

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-open-r1')));

      expect(selectedAgent()).toBe('s2');
    });

    it('offers no way to open a run that started no session', async () => {
      const daemon = await openAutomations({ definitions: [definition('d1', 'PR reviewer')], runs: [run('r1')] });
      await gesture(daemon, () => fireEvent.click(screen.getByText('PR reviewer')));

      expect(screen.getByTestId('automation-run-row')).toBeInTheDocument();
      expect(screen.queryByTestId('automation-run-open-r1')).toBeNull();
    });
  });

  describe('Run now', () => {
    it('asks for a new run after the daemon refuses one', async () => {
      const daemon = await openAutomations({ definitions: [definition('d1', 'PR reviewer')] }, (scripted) => refuseRuns(scripted, 'automation is disabled'));

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));
      expect(screen.getByTestId('automation-run-error-d1')).toHaveTextContent('automation is disabled');
      expect(screen.getByTestId('automation-run-now-d1')).toBeEnabled();
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));

      const [first, second] = runRequestIds(daemon);
      expect(second).not.toBe(first);
    });

    it('retries the same run when the daemon never answered', async () => {
      const daemon = await openAutomations({ definitions: [definition('d1', 'PR reviewer')] });

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));
      await act(() => vi.advanceTimersByTimeAsync(30_000));
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));

      const [first, second] = runRequestIds(daemon);
      expect(second).toBe(first);
    });

    it('asks for a new run once the one it retried shows up delivered', async () => {
      const scene: Scene = { definitions: [definition('d1', 'PR reviewer')] };
      const daemon = await openAutomations(scene);
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));
      await act(() => vi.advanceTimersByTimeAsync(30_000));
      const [pending] = runRequestIds(daemon);

      scene.definitions = [definition('d1', 'PR reviewer', { last_run: run('r1', { occurrence_key: `manual:${pending}`, state: 'pending' }) })];
      await changed(daemon);
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));
      await act(() => vi.advanceTimersByTimeAsync(30_000));
      expect(runRequestIds(daemon)[1]).toBe(pending);

      scene.definitions = [definition('d1', 'PR reviewer', { last_run: run('r1', { occurrence_key: `manual:${pending}`, state: 'delivered' }) })];
      await changed(daemon);
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));
      expect(runRequestIds(daemon)[2]).not.toBe(pending);
    });

    it('after a relaunch, retries the manual run still pending in history instead of starting another', async () => {
      const daemon = await openAutomations({
        definitions: [definition('d1', 'PR reviewer', { last_run: run('r1', { occurrence_key: 'manual:restart-key-1', state: 'pending' }) })],
      });

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));

      expect(runRequestIds(daemon)).toEqual(['restart-key-1']);
    });

    it('does not take a pending scheduled run for a manual one', async () => {
      const daemon = await openAutomations({
        definitions: [definition('d1', 'PR reviewer', { last_run: run('r1', { occurrence_key: 'sched:2026-01-01T00:00:00Z', state: 'pending' }) })],
      });

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));

      expect(runRequestIds(daemon)).toHaveLength(1);
      expect(runRequestIds(daemon)[0]).not.toBe('2026-01-01T00:00:00Z');
    });

    it('keeps retrying its own pending run when history shows a different one pending', async () => {
      const scene: Scene = { definitions: [definition('d1', 'PR reviewer')] };
      const daemon = await openAutomations(scene);
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));
      await act(() => vi.advanceTimersByTimeAsync(30_000));
      const [own] = runRequestIds(daemon);

      scene.definitions = [definition('d1', 'PR reviewer', { last_run: run('r9', { occurrence_key: 'manual:some-other-pending-run', state: 'pending' }) })];
      await changed(daemon);
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('automation-run-now-d1')));

      expect(runRequestIds(daemon)).toEqual([own, own]);
    });
  });
});
