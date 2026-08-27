import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { PaneSeedChip } from './PaneSeedChip';
import type { Seed } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'growing',
    step_slug: overrides.title,
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    ready: false,
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  };
}

describe('PaneSeedChip', () => {
  it('shows the seed title and id, and opens it', () => {
    const onOpen = vi.fn();
    render(
      <PaneSeedChip
        seedId="s-work11"
        seed={seed({ id: 's-work11', title: 'move the wire' })}
        unread={false}
        sessionId="sess-a"
        onOpen={onOpen}
      />,
    );

    expect(screen.getByText('move the wire')).toBeInTheDocument();
    expect(screen.getByText('s-work11')).toBeInTheDocument();
    expect(screen.queryByTestId('seed-chip-unread-sess-a')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('seed-chip-sess-a'));
    expect(onOpen).toHaveBeenCalled();
  });

  it('falls back to the id when the seed is not in the pushed list', () => {
    render(<PaneSeedChip seedId="s-late11" unread sessionId="sess-b" onOpen={vi.fn()} />);

    expect(screen.getByText('s-late11')).toBeInTheDocument();
    expect(screen.getByTestId('seed-chip-unread-sess-b')).toBeInTheDocument();
  });
});
