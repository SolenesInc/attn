import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { openUrl } from '@tauri-apps/plugin-opener';
import type { SessionPullRequest } from '../types/generated';
import { SessionProvenance } from './SessionProvenance';

vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));

const provenance = {
  run_id: 'run-1',
  definition_id: 'requested-pr-review-sol-medium',
  definition_name: 'Requested PR review - GPT Sol medium',
  trigger_type: 'github_review_requested',
  pull_request: {
    repository: 'ghe.spotify.net/audiobook-feed-mgmt/feed-nexus-web',
    number: 101,
    url: 'https://ghe.spotify.net/audiobook-feed-mgmt/feed-nexus-web/pull/101',
    title: 'Fix validation race',
    head_sha: '82f1c7a000000000000000000000000000000000',
  },
};

function pr(overrides: Partial<SessionPullRequest> = {}): SessionPullRequest {
  return {
    repository: 'github.com/victorarias/attn',
    number: 71,
    url: 'https://github.com/victorarias/attn/pull/71',
    title: 'feat(garden): sweep the agent ledger nightly',
    created_at: '2026-08-30T12:00:00Z',
    state: 'open',
    status_fetched_at: '2026-08-30T12:05:00Z',
    ...overrides,
  };
}

describe('SessionProvenance', () => {
  it('renders automation, definition, PR identity, and title as one line', () => {
    render(<SessionProvenance automation={provenance} />);

    expect(screen.getByText('Automation')).toBeInTheDocument();
    expect(screen.getByText('GPT Sol medium')).toBeInTheDocument();
    expect(screen.getByText('feed-nexus-web#101')).toBeInTheDocument();
    expect(screen.getByText('Fix validation race')).toBeInTheDocument();
  });

  it('opens the exact PR from an interactive surface', () => {
    render(<SessionProvenance automation={provenance} interactive />);

    fireEvent.click(screen.getByRole('button', { name: 'feed-nexus-web#101 ↗' }));
    expect(openUrl).toHaveBeenCalledWith(provenance.pull_request.url);
  });

  it('keeps the sidebar badge compact while exposing full provenance accessibly', () => {
    render(<SessionProvenance automation={provenance} density="badge" />);

    expect(screen.getByLabelText(/Requested PR review - GPT Sol medium/)).toHaveTextContent('⚡');
  });

  it('puts the automation run and the session PR on the same line', () => {
    render(<SessionProvenance automation={provenance} pullRequests={[pr({ ci_status: 'failure' })]} />);

    expect(screen.getByText('Automation')).toBeInTheDocument();
    expect(screen.getByText('PR')).toBeInTheDocument();
    expect(screen.getByText('attn#71')).toBeInTheDocument();
    expect(screen.getByText('checks failed')).toBeInTheDocument();
  });

  it('shows the newest open PR, not the newer merged one', () => {
    render(<SessionProvenance pullRequests={[
      pr({ number: 74, state: 'merged', created_at: '2026-08-30T14:00:00Z' }),
      pr({ number: 71, state: 'open', ci_status: 'pending', created_at: '2026-08-30T10:00:00Z' }),
    ]} />);

    expect(screen.getByText('attn#71')).toBeInTheDocument();
    expect(screen.getByText('checks running')).toBeInTheDocument();
    expect(screen.queryByText('attn#74')).not.toBeInTheDocument();
  });

  it('keeps a merged PR on the line when no open one is left', () => {
    render(<SessionProvenance pullRequests={[pr({ state: 'merged' })]} />);

    expect(screen.getByText('attn#71')).toBeInTheDocument();
    expect(screen.getByText('merged')).toBeInTheDocument();
  });

  it('renders nothing when every PR of the session is closed', () => {
    const { container } = render(<SessionProvenance pullRequests={[pr({ state: 'closed' })]} />);

    expect(container).toBeEmptyDOMElement();
  });

  it('opens the popover from the PR entry and lists every PR of the session', () => {
    render(<SessionProvenance interactive pullRequests={[
      pr({ number: 74, state: 'open', ci_status: 'pending', created_at: '2026-08-30T14:00:00Z' }),
      pr({ number: 71, state: 'merged', created_at: '2026-08-30T10:00:00Z' }),
    ]} />);

    fireEvent.click(screen.getByTestId('session-provenance-pr'));

    const popover = screen.getByTestId('session-pr-popover');
    expect(popover).toBeInTheDocument();
    const numbers = Array.from(popover.querySelectorAll('.session-pr-popover__item-number'));
    expect(numbers.map((node) => node.textContent)).toEqual(['#74', '#71']);
  });

  it('leaves the popover shut on surfaces that are not interactive', () => {
    render(<SessionProvenance pullRequests={[pr()]} />);

    expect(screen.queryByTestId('session-provenance-pr')).not.toBeInTheDocument();
    expect(screen.queryByTestId('session-pr-popover')).not.toBeInTheDocument();
  });
});
