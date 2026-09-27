import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { daemonPR, type DaemonPR } from './test/daemonFixtures';
import { serveLaunches } from './test/locations';
import { gesture, renderApp } from './test/renderApp';
import type { Reply, ScriptedDaemon } from './test/scriptedDaemon';

const widgets = daemonPR('github.com:acme/widgets#42', {
  repo: 'acme/widgets',
  number: 42,
  title: 'Make widgets faster',
  head_branch: 'faster-widgets',
});

const withoutBranch = { ...widgets, head_branch: undefined };

async function renderPRs(pr: DaemonPR = widgets, settings: Record<string, string> = { projects_directory: '/code' }) {
  const view = await renderApp({ initialState: { prs: [pr], settings } });
  const alert = vi.fn();
  vi.stubGlobal('alert', alert);
  return { ...view, alerted: () => alert.mock.calls.map(([message]) => message) };
}

function prCard() {
  return screen.getAllByTestId('pr-card').find((element) => element.textContent?.includes('Make widgets faster'))!;
}

async function openPR(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.click(within(prCard()).getByRole('button', { name: 'New' })));
}

const opening = () => screen.queryByRole('status', { name: 'Opening PR 42' });

function detailsReply(pr: DaemonPR): Reply {
  return { event: 'fetch_pr_details_result', success: true, prs: [pr] };
}

describe('App open pull request', () => {
  it('shows which PR is being opened and the step it is on', async () => {
    const { daemon } = await renderPRs();
    daemon.on('ensure_repo', () => ({ event: 'ensure_repo_result', success: true, cloned: false }));

    await openPR(daemon);

    const status = opening()!;
    expect(status).toHaveTextContent('acme/widgets#42');
    expect(status).toHaveTextContent('Make widgets faster');
    expect(status).toHaveTextContent('Creating worktree');
    expect(daemon.sentOf('ensure_repo')).toEqual([expect.objectContaining({ target_path: '/code/widgets', clone_url: 'https://github.com/acme/widgets.git' })]);
    expect(daemon.sentOf('create_worktree_from_branch')).toEqual([expect.objectContaining({ branch: 'origin/faster-widgets' })]);
    expect(daemon.sentOf('fetch_pr_details')).toEqual([]);
  });

  it('fetches a PR’s branch before opening it when the daemon has not reported one, and starts the session in its worktree', async () => {
    const { daemon, alerted } = await renderPRs(withoutBranch);
    serveLaunches(daemon);
    daemon.on('ensure_repo', () => ({ event: 'ensure_repo_result', success: true, cloned: false }));
    daemon.on('create_worktree_from_branch', () => ({ event: 'create_worktree_result', success: true, path: '/code/widgets--faster-widgets' }));

    await openPR(daemon);
    expect(opening()).toHaveTextContent('Fetching branch details');
    expect(daemon.sentOf('fetch_pr_details')).toEqual([{ cmd: 'fetch_pr_details', id: widgets.id }]);
    expect(daemon.sentOf('ensure_repo')).toEqual([]);

    await gesture(daemon, () => daemon.emit(detailsReply(widgets)));

    expect(daemon.sentOf('create_worktree_from_branch')).toEqual([expect.objectContaining({ main_repo: '/code/widgets', branch: 'origin/faster-widgets' })]);
    expect(daemon.sentOf('spawn_session')).toEqual([expect.objectContaining({ cwd: '/code/widgets--faster-widgets', label: 'widgets#42' })]);
    expect(opening()).toBeNull();
    expect(alerted()).toEqual([]);
  });

  it.each<[string, DaemonPR, Record<string, string>, (daemon: ScriptedDaemon) => void, string, string[]]>([
    ['no projects directory is set', widgets, {}, () => {}, 'Please configure your Projects Directory in Settings first.', []],
    [
      'GitHub has no branch for the PR',
      withoutBranch,
      { projects_directory: '/code' },
      (daemon) => daemon.on('fetch_pr_details', () => detailsReply(withoutBranch)),
      'PR branch information not available.',
      ['fetch_pr_details'],
    ],
    [
      'fetching the PR’s details fails',
      withoutBranch,
      { projects_directory: '/code' },
      (daemon) => daemon.on('fetch_pr_details', () => ({ event: 'fetch_pr_details_result', success: false, error: 'API rate limit exceeded' })),
      'Failed to fetch PR details.\n\nAPI rate limit exceeded',
      ['fetch_pr_details'],
    ],
    [
      'the repository cannot be cloned',
      widgets,
      { projects_directory: '/code' },
      (daemon) => daemon.on('ensure_repo', () => ({ event: 'ensure_repo_result', success: false, error: 'clone failed: could not resolve host' })),
      'Failed to clone repository acme/widgets.',
      ['ensure_repo'],
    ],
    [
      'the worktree already exists',
      widgets,
      { projects_directory: '/code' },
      (daemon) => {
        daemon.on('ensure_repo', () => ({ event: 'ensure_repo_result', success: true }));
        daemon.on('create_worktree_from_branch', () => ({ event: 'create_worktree_result', success: false, error: "fatal: 'faster-widgets' already exists" }));
      },
      'A worktree for this branch may already exist.',
      ['ensure_repo', 'create_worktree_from_branch'],
    ],
  ])('stops and tells the user when %s', async (_, pr, settings, script, message, sent) => {
    const { daemon, alerted } = await renderPRs(pr, settings);
    script(daemon);

    await openPR(daemon);

    expect(alerted()).toEqual([expect.stringContaining(message)]);
    expect(opening()).toBeNull();
    const steps = ['fetch_pr_details', 'ensure_repo', 'create_worktree_from_branch', 'spawn_session'] as const;
    expect(steps.filter((cmd) => daemon.sentOf(cmd).length > 0)).toEqual(sent);
  });
});
