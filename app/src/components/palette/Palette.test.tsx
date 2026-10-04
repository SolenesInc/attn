import { describe, it, expect, vi } from 'vitest';
import { useState } from 'react';
import { render, screen, fireEvent } from '../../test/utils';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { Palette } from './Palette';

interface Row { path: string }
function Harness({
  rows,
  onPick = () => {},
  onClose = () => {},
  filter = true,
  onKeyDown,
}: {
  rows: string[];
  onPick?: (row: Row) => void;
  onClose?: () => void;
  filter?: boolean;
  onKeyDown?: (event: React.KeyboardEvent<HTMLInputElement>) => boolean;
}) {
  const [query, setQuery] = useState('');
  const items: Row[] = rows
    .filter((path) => (filter ? path.includes(query) : true))
    .map((path) => ({ path }));
  return (
    <Palette
      variant="test-palette"
      ariaLabel="Find a thing"
      placeholder="Find…"
      query={query}
      onQueryChange={setQuery}
      items={items}
      itemKey={(row) => row.path}
      renderItem={(row) => <span className="row">{row.path}</span>}
      emptyLabel="Nothing matches."
      onPick={onPick}
      onClose={onClose}
      onKeyDown={onKeyDown}
    />
  );
}

const options = () => screen.getAllByRole('option');
const input = () => screen.getByRole('combobox');

describe('Palette', () => {
  it('keeps default and keyboard selection under a stationary pointer, then selects on movement', () => {
    render(<Harness rows={['a.md', 'b.md', 'c.md']} />);
    const rows = options();
    fireEvent.mouseEnter(rows[2], { clientX: 80, clientY: 120 });
    expect(rows[0]).toHaveAttribute('aria-selected', 'true');
    fireEvent.mouseMove(rows[2], { clientX: 80, clientY: 120 });
    expect(rows[0]).toHaveAttribute('aria-selected', 'true');

    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.mouseEnter(rows[2], { clientX: 80, clientY: 120 });
    fireEvent.mouseMove(rows[2], { clientX: 80, clientY: 120 });
    expect(rows[1]).toHaveAttribute('aria-selected', 'true');

    fireEvent.mouseMove(rows[2], { clientX: 81, clientY: 120 });
    expect(rows[2]).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(input(), { key: 'ArrowUp' });
    fireEvent.mouseMove(rows[2], { clientX: 81, clientY: 120 });
    expect(rows[1]).toHaveAttribute('aria-selected', 'true');
    fireEvent.mouseMove(rows[2], { clientX: 81, clientY: 121 });
    expect(rows[2]).toHaveAttribute('aria-selected', 'true');
  });

  it('focuses the input on mount so typing lands in the palette', () => {
    render(<Harness rows={['a.md']} />);
    expect(input()).toHaveFocus();
  });

  it('picks the highlighted row on Enter, moving with the arrow keys', () => {
    const onPick = vi.fn();
    render(<Harness rows={['a.md', 'b.md', 'c.md']} onPick={onPick} />);

    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.keyDown(input(), { key: 'ArrowUp' });
    fireEvent.keyDown(input(), { key: 'Enter' });

    expect(onPick).toHaveBeenCalledWith({ path: 'b.md' });
  });

  it('keeps the first row highlighted when a row arrives above it before any navigation', () => {
    const onPick = vi.fn();
    const { rerender } = render(<Harness rows={['b.md', 'c.md']} onPick={onPick} />);
    rerender(<Harness rows={['a.md', 'b.md', 'c.md']} onPick={onPick} />);

    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith({ path: 'b.md' });
  });

  it('highlights the first result again after the query changes', () => {
    const onPick = vi.fn();
    render(<Harness rows={['alpha.md', 'beta.md', 'alphabet.md']} onPick={onPick} />);
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.change(input(), { target: { value: 'alpha' } });

    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith({ path: 'alpha.md' });
  });

  it('clamps the highlight when the list shrinks under it', () => {
    const onPick = vi.fn();
    render(<Harness rows={['alpha.md', 'beta.md', 'alphabet.md']} onPick={onPick} />);

    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    fireEvent.change(input(), { target: { value: 'alpha' } });
    expect(options()).toHaveLength(2);

    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith({ path: 'alpha.md' });
  });

  it('does nothing on Enter when nothing matches', () => {
    const onPick = vi.fn();
    render(<Harness rows={['a.md']} onPick={onPick} />);

    fireEvent.change(input(), { target: { value: 'zzz' } });
    expect(screen.getByText('Nothing matches.')).toBeInTheDocument();

    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onPick).not.toHaveBeenCalled();
  });

  it('closes on Escape without letting it reach a surrounding handler', () => {
    const onClose = vi.fn();
    const outerEscape = vi.fn();
    render(
      <div onKeyDown={outerEscape}>
        <Harness rows={['a.md']} onClose={onClose} />
      </div>,
    );

    fireEvent.keyDown(input(), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(outerEscape).not.toHaveBeenCalled();
  });

  it('takes Escape ahead of a surface it opened over, leaving that surface open', () => {
    const onClose = vi.fn();
    const surfaceEscape = vi.fn();
    function SurfaceBeneath() {
      useEscapeStack(surfaceEscape, true);
      return null;
    }
    const { rerender } = render(<SurfaceBeneath />);
    rerender(
      <>
        <SurfaceBeneath />
        <Harness rows={['a.md']} onClose={onClose} />
      </>,
    );

    fireEvent.keyDown(input(), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(surfaceEscape).not.toHaveBeenCalled();
  });

  it('closes on a backdrop click but not on a click inside the box', () => {
    const onClose = vi.fn();
    const { container } = render(<Harness rows={['a.md']} onClose={onClose} />);

    fireEvent.mouseDown(container.querySelector('.palette-box')!);
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.mouseDown(container.querySelector('.palette')!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('lets a caller intercept keys the shell does not own', () => {
    const onPick = vi.fn();
    const onKeyDown = vi.fn((event: React.KeyboardEvent<HTMLInputElement>) => event.key === 'Enter');
    render(<Harness rows={['a.md']} onPick={onPick} onKeyDown={onKeyDown} />);

    fireEvent.keyDown(input(), { key: 'Enter' });
    expect(onKeyDown).toHaveBeenCalled();
    expect(onPick).not.toHaveBeenCalled();

    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    expect(options()[0]).toHaveAttribute('aria-selected', 'true');
  });

  it('namespaces classes and ARIA wiring by variant', () => {
    const { container } = render(<Harness rows={['a.md', 'b.md']} />);

    expect(container.querySelector('.palette.test-palette')).toBeInTheDocument();
    expect(container.querySelector('.palette-option.test-palette-option')).toBeInTheDocument();

    expect(input()).toHaveAttribute('aria-controls', 'test-palette-list');
    expect(input()).toHaveAttribute('aria-activedescendant', 'test-palette-opt-0');
    fireEvent.keyDown(input(), { key: 'ArrowDown' });
    expect(input()).toHaveAttribute('aria-activedescendant', 'test-palette-opt-1');
  });

  it('drops aria-activedescendant when there is no row to point at', () => {
    render(<Harness rows={['a.md']} />);
    fireEvent.change(input(), { target: { value: 'zzz' } });
    expect(input()).not.toHaveAttribute('aria-activedescendant');
  });
});
