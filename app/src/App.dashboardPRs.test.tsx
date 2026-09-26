import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { daemonPR as pr } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';

const card = (title: string) => screen.queryAllByTestId('pr-card').find((element) => element.textContent?.includes(title)) ?? null;
const cardsIn = (section: 'yours' | 'review') => within(screen.getByTestId(`pr-section-${section}`)).getAllByTestId('pr-card');
const attentionCount = () => screen.getByRole('heading', { name: 'Pull Requests' }).parentElement!.textContent;

describe('App dashboard pull requests', () => {
  it('leads with the PRs the user opened, flat and named by repo, and groups review requests by repo', async () => {
    await renderApp({
      initialState: {
        prs: [
          pr('r1', { number: 10, title: 'review this' }),
          pr('a1', { number: 20, role: 'author', author: 'victorarias', title: 'my attn change' }),
          pr('a2', { number: 21, repo: 'other/tool', role: 'author', author: 'victorarias', title: 'my tool change' }),
        ],
      },
    });

    const yours = screen.getByTestId('pr-section-yours');
    expect(yours).toHaveTextContent(/^Yours2/);
    expect(cardsIn('yours')).toEqual([card('my attn change'), card('my tool change')]);
    expect(card('my attn change')).toHaveTextContent('attn#20');
    expect(card('my tool change')).toHaveTextContent('tool#21');

    const review = screen.getByTestId('pr-section-review');
    expect(within(review).getByTitle('Mute all PRs from this repo').parentElement).toHaveTextContent(/attn1 review/);
    expect(cardsIn('review')).toEqual([card('review this')]);
    expect(card('review this')).not.toHaveTextContent('attn#10');
  });

  it('omits a section with nothing in it, and says so when both are empty', async () => {
    const { daemon } = await renderApp({ initialState: { prs: [pr('r1', { title: 'review this' })] } });
    expect(screen.queryByTestId('pr-section-yours')).toBeNull();
    expect(screen.getByTestId('pr-section-review')).toBeInTheDocument();

    daemon.emit({ event: 'prs_updated', prs: [] });

    expect(screen.getByText('No PRs need attention')).toBeInTheDocument();
  });

  it('says why when GitHub polling is off for this instance', async () => {
    const { daemon } = await renderApp({ initialState: { prs: [] } });

    daemon.emit({
      event: 'github_hosts_updated',
      github_hosts: [],
      github_polling_off_reason: 'GitHub polling is off for instance dev. Start its daemon with ATTN_GITHUB_POLLING=on.',
    });

    expect(screen.getByTestId('github-polling-off')).toHaveTextContent('ATTN_GITHUB_POLLING=on');
    expect(screen.queryByText('No PRs need attention')).toBeNull();
  });

  it('drops PRs the moment the daemon reports their repo or author muted', async () => {
    const { daemon } = await renderApp({
      initialState: {
        prs: [
          pr('p1', { title: 'from the noisy repo', repo: 'org/noisy' }),
          pr('p2', { title: 'from the noisy author', author: 'chatty-bot' }),
          pr('p3', { title: 'muted on its own', muted: true }),
          pr('p4', { title: 'still wanted' }),
        ],
      },
    });
    expect(card('muted on its own')).toBeNull();

    daemon.emit({ event: 'repos_updated', repos: [{ repo: 'org/noisy', muted: true, collapsed: false }] });
    expect(card('from the noisy repo')).toBeNull();
    expect(card('from the noisy author')).not.toBeNull();

    daemon.emit({ event: 'authors_updated', authors: [{ author: 'chatty-bot', muted: true }] });
    expect(card('from the noisy author')).toBeNull();
    expect(card('still wanted')).not.toBeNull();
  });

  it('stops counting a PR the user approved until new changes land on it', async () => {
    const approved = pr('p1', { title: 'approved already', approved_by_me: true });
    const { daemon } = await renderApp({ initialState: { prs: [approved, pr('p2', { title: 'still to review' })] } });
    expect(attentionCount()).toBe('Pull Requests1');
    expect(card('approved already')).not.toBeNull();

    daemon.emit({ event: 'prs_updated', prs: [{ ...approved, has_new_changes: true }, pr('p2', { title: 'still to review' })] });

    expect(attentionCount()).toBe('Pull Requests2');
  });

  it('takes a merged PR off the list before the daemon stops reporting it', async () => {
    const mine = pr('p1', { title: 'ship it', role: 'author', author: 'victorarias' });
    const { daemon } = await renderApp({ initialState: { prs: [mine] } });
    daemon.on('merge_pr', ({ id }) => ({ event: 'pr_action_result', action: 'merge', id, success: true }));

    fireEvent.click(within(card('ship it')!).getByRole('button', { name: 'Merge' }));
    expect(screen.getByText('Merge PR #1?')).toBeInTheDocument();
    const confirm = screen.getAllByRole('button', { name: 'Merge' }).find((button) => !card('ship it')!.contains(button))!;
    await gesture(daemon, () => fireEvent.click(confirm));
    expect(daemon.sentOf('merge_pr')).toEqual([expect.objectContaining({ id: 'p1', method: 'squash' })]);
    expect(card('ship it')).not.toBeNull();

    await act(() => vi.advanceTimersByTimeAsync(1500 + 350));

    expect(card('ship it')).toBeNull();
  });
});
