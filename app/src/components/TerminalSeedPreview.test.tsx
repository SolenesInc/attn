import { fireEvent, render, screen } from '../test/utils';
import { describe, expect, it, vi } from 'vitest';
import type { Seed } from '../types/generated';
import { TerminalSeedPreview, terminalSeedBodyExcerpt } from './TerminalSeedPreview';

function seed(overrides: Partial<Seed> = {}): Seed {
  return {
    id: 's-7k3f9m',
    title: 'Make seed IDs navigable from terminals',
    body: 'Recognize **valid** seed IDs and show a compact [preview](https://example.test).',
    status: 'growing',
    tender_member: 'trellis',
    tender_session: '',
    planter_member: '',
    planter_session: '',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    edges: [],
    gate: false,
    ready: false,
    rev: 1,
    step_slug: '',
    template: false,
    vars: [],
    ...overrides,
  };
}

const anchor = {
  left: 200,
  right: 270,
  top: 180,
  bottom: 198,
  bounds: { left: 20, right: 800, top: 20, bottom: 600 },
};

describe('TerminalSeedPreview', () => {
  it('shows the seed summary and keeps the tile action icon-only', () => {
    render(
      <TerminalSeedPreview
        seed={seed()}
        anchor={anchor}
        onOpen={vi.fn()}
        onClose={vi.fn()}
        onPointerEnter={vi.fn()}
        onPointerLeave={vi.fn()}
      />,
    );

    expect(screen.getByRole('heading')).toHaveTextContent('Make seed IDs navigable');
    expect(screen.getByText('s-7k3f9m')).toBeInTheDocument();
    expect(screen.getByText(/Recognize valid seed IDs/)).toBeInTheDocument();
    expect(screen.getByText('tended by Trellis')).toBeInTheDocument();
    const open = screen.getByRole('button', { name: 'Open as tile' });
    expect(open).toHaveTextContent('');
    expect(open.querySelector('svg')).not.toBeNull();
  });

  it('opens the seed through the icon action', () => {
    const onOpen = vi.fn();
    render(
      <TerminalSeedPreview
        seed={seed()}
        anchor={anchor}
        onOpen={onOpen}
        onClose={vi.fn()}
        onPointerEnter={vi.fn()}
        onPointerLeave={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Open as tile' }));
    expect(onOpen).toHaveBeenCalledWith('s-7k3f9m');
  });
});

describe('terminalSeedBodyExcerpt', () => {
  it('removes markdown furniture and clips at a word boundary', () => {
    const excerpt = terminalSeedBodyExcerpt(
      '# Goal\n\nUse **the garden** and [open it](https://example.test). ' + 'word '.repeat(80),
      70,
    );
    expect(excerpt).toMatch(/^Goal Use the garden and open it\./);
    expect(excerpt.endsWith('…')).toBe(true);
    expect(excerpt.length).toBeLessThanOrEqual(71);
  });
});
