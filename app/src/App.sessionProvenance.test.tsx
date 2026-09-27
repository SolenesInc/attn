import { fireEvent, screen, within } from '@testing-library/react';
import { openUrl } from '@tauri-apps/plugin-opener';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, daemonSession, type DaemonSession } from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

type SessionPullRequest = NonNullable<DaemonSession['pull_requests']>[number];
type Automation = NonNullable<DaemonSession['automation']>;

const reviewRun: Automation = {
  run_id: 'run-1',
  definition_id: 'requested-pr-review-sol-medium',
  definition_name: 'Requested PR review - GPT Sol medium',
  trigger_type: 'github_review_requested',
  pull_request: {
    repository: 'ghe.example.net/audiobook/feed-nexus-web',
    number: 101,
    url: 'https://ghe.example.net/audiobook/feed-nexus-web/pull/101',
    title: 'Fix validation race',
    head_sha: '82f1c7a000000000000000000000000000000000',
  },
};

function pr(overrides: Partial<SessionPullRequest> = {}): SessionPullRequest {
  const number = overrides.number ?? 71;
  return {
    repository: 'github.com/victorarias/attn',
    number,
    url: `https://github.com/victorarias/attn/pull/${number}`,
    title: 'feat(garden): sweep the agent ledger nightly',
    created_at: '2026-08-30T12:00:00Z',
    state: 'open',
    status_fetched_at: '2026-08-30T12:05:00Z',
    ...overrides,
  };
}

async function openSession(session: Partial<DaemonSession>) {
  const view = await renderApp({
    initialState: { sessions: [daemonSession('s1', session)], workspaces: [agentWorkspace('s1')] },
  });
  await gesture(view.daemon, () => fireEvent.click(screen.getByTestId('session-s1')));
  return view.daemon;
}

const header = () => within(document.querySelector<HTMLElement>('[data-pane-id="pane-s1"]')!);
const popover = () => screen.queryByRole('dialog', { name: /^Pull request / });

async function openPopover(daemon: ScriptedDaemon, target = /Pull request attn#\d+ details/) {
  await gesture(daemon, () => fireEvent.click(header().getByRole('button', { name: target })));
  return within(popover()!);
}

describe('App session provenance', () => {
  it('opens the pull request an automation run is about from Home', async () => {
    const { daemon } = await renderApp({ initialState: { sessions: [daemonSession('review-1', { automation: reviewRun })], workspaces: [agentWorkspace('review-1')] } });
    const row = within(screen.getByTestId('session-review-1'));
    expect(row.getByText('Automation')).toBeInTheDocument();
    expect(row.getByText('GPT Sol medium')).toBeInTheDocument();
    expect(row.getByText('Fix validation race')).toBeInTheDocument();

    await gesture(daemon, () => fireEvent.click(row.getByRole('button', { name: 'feed-nexus-web#101 ↗' })));

    expect(openUrl).toHaveBeenCalledWith(reviewRun.pull_request!.url);
  });

  it('puts the automation run and the session’s pull request on one line', async () => {
    await openSession({ automation: reviewRun, pull_requests: [pr({ ci_status: 'failure' })] });

    expect(header().getByText('Automation')).toBeInTheDocument();
    expect(header().getByText('PR')).toBeInTheDocument();
    expect(header().getByRole('button', { name: 'Pull request attn#71 details' })).toBeInTheDocument();
    expect(header().getByText('checks failed')).toBeInTheDocument();
  });

  it('shows the newest open pull request rather than a newer merged one', async () => {
    await openSession({
      pull_requests: [
        pr({ number: 74, state: 'merged', created_at: '2026-08-30T14:00:00Z' }),
        pr({ number: 71, state: 'open', ci_status: 'pending', created_at: '2026-08-30T10:00:00Z' }),
      ],
    });

    expect(header().getByRole('button', { name: 'Pull request attn#71 details' })).toBeInTheDocument();
    expect(header().getByText('checks running')).toBeInTheDocument();
    expect(header().queryByText(/attn#74/)).toBeNull();
  });

  it.each<[string, SessionPullRequest[], string, string]>([
    ['the newest of several open pull requests', [pr({ number: 71, created_at: '2026-08-30T10:00:00Z' }), pr({ number: 74, created_at: '2026-08-30T12:00:00Z' })], 'attn#74', 'open'],
    ['a draft over a newer merged one, as a draft', [pr({ number: 71, state: 'merged', created_at: '2026-08-30T13:00:00Z' }), pr({ number: 74, state: 'draft' })], 'attn#74', 'draft'],
    ['conflicts before failing checks or requested changes', [pr({ mergeable_state: 'dirty', ci_status: 'failure', review_status: 'changes_requested' })], 'attn#71', 'conflicts'],
    ['failing checks before requested changes', [pr({ ci_status: 'failure', review_status: 'changes_requested' })], 'attn#71', 'checks failed'],
    ['requested changes while checks still run', [pr({ ci_status: 'pending', review_status: 'changes_requested' })], 'attn#71', 'changes requested'],
    ['ready to merge once approved with a clean merge', [pr({ review_status: 'approved', mergeable_state: 'clean' })], 'attn#71', 'ready to merge'],
    ['only approved while the merge is still blocked', [pr({ review_status: 'approved', mergeable_state: 'blocked' })], 'attn#71', 'approved'],
    ['merged regardless of the checks that ran on it', [pr({ state: 'merged', ci_status: 'failure' })], 'attn#71', 'merged'],
  ])('summarises %s', async (_, pullRequests, shown, status) => {
    await openSession({ pull_requests: pullRequests });

    expect(header().getByRole('button', { name: `Pull request ${shown} details` })).toBeInTheDocument();
    expect(header().getByText(status)).toBeInTheDocument();
  });

  it('keeps a merged pull request when no open one is left', async () => {
    await openSession({ pull_requests: [pr({ state: 'merged' })] });
    expect(header().getByText('merged')).toBeInTheDocument();
  });

  it('shows nothing for a session whose pull requests are all closed', async () => {
    await openSession({ pull_requests: [pr({ state: 'closed' })] });

    expect(header().queryByText('PR')).toBeNull();
  });

  describe('the pull request popover', () => {
    const second = pr({ number: 68, state: 'merged', title: 'docs: ledger sweep glossary', created_at: '2026-08-30T09:00:00Z' });

    it('spells out every status the daemon reported', async () => {
      const daemon = await openSession({ pull_requests: [pr({ ci_status: 'failure', review_status: 'pending', mergeable_state: 'clean' })] });

      const card = await openPopover(daemon);

      expect(card.getByText('failed')).toBeInTheDocument();
      expect(card.getByText('waiting on a reviewer')).toBeInTheDocument();
      expect(card.getByText('no conflicts')).toBeInTheDocument();
      expect(card.getByText('state').nextElementSibling).toHaveTextContent('open');
      expect(card.getByText('opened').nextElementSibling?.textContent).toMatch(/ago · /);
      expect(card.queryByRole('list', { name: 'Pull requests from this session' })).toBeNull();
      expect(card.queryByText('pick')).toBeNull();
    });

    it('says it is waiting for GitHub rather than inventing a status, or that polling is off', async () => {
      const daemon = await openSession({ pull_requests: [pr({ status_fetched_at: undefined })] });
      const card = await openPopover(daemon);
      expect(card.getByText('waiting for GitHub')).toBeInTheDocument();

      daemon.emit({ event: 'github_hosts_updated', github_hosts: [], github_polling_off_reason: 'GitHub polling is off for instance dev.' });

      expect(card.getByText('GitHub polling is off for this instance')).toBeInTheDocument();
      expect(card.queryByText('waiting for GitHub')).toBeNull();
    });

    it('lists every pull request of the session, open then merged then closed, newest first within each, and opens one from its title', async () => {
      const daemon = await openSession({
        pull_requests: [
          pr({ number: 60, state: 'closed', created_at: '2026-08-30T15:00:00Z' }),
          second,
          pr({ number: 72, created_at: '2026-08-30T10:00:00Z' }),
          pr({ number: 74, created_at: '2026-08-30T14:00:00Z' }),
          pr({ number: 69, state: 'merged', created_at: '2026-08-30T11:00:00Z' }),
        ],
      });
      const card = await openPopover(daemon);

      const list = within(card.getByRole('list', { name: 'Pull requests from this session' }));
      expect(list.getAllByRole('button').map((item) => item.textContent?.match(/#\d+/)?.[0])).toEqual(['#74', '#72', '#69', '#68', '#60']);

      fireEvent.click(card.getByTitle('Open attn#74 on GitHub'));
      expect(openUrl).toHaveBeenCalledWith('https://github.com/victorarias/attn/pull/74');
    });

    it('walks the list with the arrows and opens the highlighted one, but leaves ↵ to a button the user tabbed onto', async () => {
      const daemon = await openSession({ pull_requests: [pr(), second] });
      await openPopover(daemon);
      vi.mocked(openUrl).mockClear();

      fireEvent.keyDown(within(popover()!).getAllByRole('button', { name: /#\d+/ })[0], { key: 'Enter' });
      expect(openUrl).not.toHaveBeenCalled();

      fireEvent.keyDown(popover()!, { key: 'ArrowDown' });
      fireEvent.keyDown(popover()!, { key: 'Enter' });
      expect(openUrl).toHaveBeenCalledWith(second.url);
    });

    it('copies the highlighted URL on c, and leaves Cmd+C to the app', async () => {
      const copied = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
      const daemon = await openSession({ pull_requests: [pr()] });
      await openPopover(daemon);

      fireEvent.keyDown(popover()!, { key: 'c', metaKey: true });
      expect(copied).not.toHaveBeenCalled();

      fireEvent.keyDown(popover()!, { key: 'c' });
      expect(copied).toHaveBeenCalledWith('https://github.com/victorarias/attn/pull/71');
    });
  });
});
