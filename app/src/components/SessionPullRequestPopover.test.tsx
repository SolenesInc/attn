import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { openUrl } from '@tauri-apps/plugin-opener';
import type { SessionPullRequest } from '../types/generated';
import { writeClipboardText } from '../utils/clipboardBridge';
import { SessionPullRequestPopover } from './SessionPullRequestPopover';

vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));
vi.mock('../utils/clipboardBridge', () => ({ writeClipboardText: vi.fn(async () => {}) }));

function pr(overrides: Partial<SessionPullRequest> = {}): SessionPullRequest {
  return {
    repository: 'github.com/victorarias/attn',
    number: 71,
    url: 'https://github.com/victorarias/attn/pull/71',
    title: 'feat(garden): sweep the agent ledger nightly',
    created_at: '2026-08-30T12:00:00Z',
    state: 'open',
    status_fetched_at: '2026-08-30T12:05:00Z',
    ci_status: 'failure',
    review_status: 'pending',
    mergeable_state: 'clean',
    ...overrides,
  };
}

function show(pullRequests: SessionPullRequest[], autoFocus = true) {
  return render(
    <SessionPullRequestPopover
      pullRequests={pullRequests}
      anchor={{ top: 40, left: 40 }}
      autoFocus={autoFocus}
      onClose={vi.fn()}
      onPointerEnter={vi.fn()}
      onPointerLeave={vi.fn()}
    />,
  );
}

describe('SessionPullRequestPopover', () => {
  beforeEach(() => vi.clearAllMocks());

  it('spells out every dimension the daemon knows', () => {
    show([pr()]);

    expect(screen.getByText('failed')).toBeInTheDocument();
    expect(screen.getByText('waiting on a reviewer')).toBeInTheDocument();
    expect(screen.getByText('no conflicts')).toBeInTheDocument();
    expect(screen.getByText('state').nextElementSibling).toHaveTextContent('open');
    expect(screen.getByText('opened').nextElementSibling?.textContent).toMatch(/ago · /);
  });

  it('says it is still waiting rather than inventing a status', () => {
    show([pr({ status_fetched_at: undefined, ci_status: undefined, review_status: undefined, mergeable_state: undefined })]);

    expect(screen.getByText('waiting for GitHub')).toBeInTheDocument();
    expect(screen.queryByText('none reported')).not.toBeInTheDocument();
  });

  it('opens the PR on GitHub from its title', () => {
    show([pr()]);

    fireEvent.click(screen.getByRole('button', { name: /sweep the agent ledger nightly/ }));
    expect(openUrl).toHaveBeenCalledWith('https://github.com/victorarias/attn/pull/71');
  });

  it('hides the list when the session opened a single PR', () => {
    show([pr()]);

    expect(document.querySelector('.session-pr-popover__list')).toBeNull();
    expect(screen.queryByText('pick')).not.toBeInTheDocument();
  });

  it('walks the list with the arrows and opens the highlighted one', () => {
    const second = pr({
      number: 68,
      url: 'https://github.com/victorarias/attn/pull/68',
      state: 'merged',
      title: 'docs: ledger sweep glossary',
      created_at: '2026-08-30T09:00:00Z',
    });
    show([pr(), second]);

    const popover = screen.getByTestId('session-pr-popover');
    fireEvent.keyDown(popover, { key: 'ArrowDown' });
    fireEvent.keyDown(popover, { key: 'Enter' });

    expect(openUrl).toHaveBeenCalledWith(second.url);
  });

  it('copies the highlighted URL on c', () => {
    show([pr()]);

    fireEvent.keyDown(screen.getByTestId('session-pr-popover'), { key: 'c' });
    expect(writeClipboardText).toHaveBeenCalledWith('https://github.com/victorarias/attn/pull/71');
  });

  it('leaves a modified c to the app, so Cmd+C still copies a selection', () => {
    show([pr()]);

    fireEvent.keyDown(screen.getByTestId('session-pr-popover'), { key: 'c', metaKey: true });
    expect(writeClipboardText).not.toHaveBeenCalled();
  });
});
