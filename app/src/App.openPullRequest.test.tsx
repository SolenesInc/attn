import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonPR } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

const widgets = daemonPR('github.com:acme/widgets#42', {
  repo: 'acme/widgets',
  number: 42,
  title: 'Make widgets faster',
  head_branch: 'faster-widgets',
});

describe('App open pull request', () => {
  it('shows which PR is being opened and the step it is on', async () => {
    const { daemon } = await renderApp({ initialState: { prs: [widgets], settings: { projects_directory: '/code' } } });
    daemon.on('ensure_repo', () => ({ event: 'ensure_repo_result', success: true, cloned: false }));
    const card = screen.getAllByTestId('pr-card').find((element) => element.textContent?.includes('Make widgets faster'))!;

    await gesture(daemon, () => fireEvent.click(within(card).getByRole('button', { name: 'New' })));

    const status = screen.getByRole('status', { name: 'Opening PR 42' });
    expect(status).toHaveTextContent('acme/widgets#42');
    expect(status).toHaveTextContent('Make widgets faster');
    expect(status).toHaveTextContent('Creating worktree');
    expect(daemon.sentOf('ensure_repo')).toEqual([expect.objectContaining({ target_path: '/code/widgets' })]);
    expect(daemon.sentOf('create_worktree_from_branch')).toEqual([expect.objectContaining({ branch: 'origin/faster-widgets' })]);
  });
});
