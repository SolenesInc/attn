import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GardenFrame, type FrameRect } from './GardenFrame';
import { useGardenWalk } from '../store/gardenWalk';
import type { Seed } from '../hooks/useDaemonSocket';
import {
  GARDEN_FRAME_MODE_STORAGE_KEY,
  GARDEN_FULLSCREEN_VIEW_STORAGE_KEY,
} from '../hooks/useGardenPresentation';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
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
    created_at: '2026-08-20T09:00:00Z',
    updated_at: '2026-08-20T09:00:00Z',
    ...overrides,
  };
}
const world = [seed({ id: 's-alone1', title: 'unrelated work' })];
const dockRect: FrameRect = { top: 0, left: 1110, width: 560, height: 1008 };

function frameEl() {
  return screen.getByTestId('garden-frame');
}
function props(mode: 'closed' | 'dock' | 'full') {
  return {
    mode,
    dockRect,
    onToggleFrame: vi.fn(),
    onEscapeFloor: vi.fn(),
    onClose: vi.fn(),
    seeds: world,
    seedsTotal: world.length,
  };
}

describe('GardenFrame', () => {
  beforeEach(() => {
    useGardenWalk.setState({ trail: [] });
    window.localStorage.removeItem(GARDEN_FRAME_MODE_STORAGE_KEY);
    window.localStorage.removeItem(GARDEN_FULLSCREEN_VIEW_STORAGE_KEY);
  });
  afterEach(() => vi.restoreAllMocks());

  it('carries the very same panel across the promotion', () => {
    const { rerender } = render(<GardenFrame {...props('dock')} />);
    const docked = document.querySelector('.garden-panel');
    expect(docked).not.toBeNull();

    rerender(<GardenFrame {...props('full')} />);
    expect(document.querySelector('.garden-panel')).toBe(docked);

    rerender(<GardenFrame {...props('dock')} />);
    expect(document.querySelector('.garden-panel')).toBe(docked);
  });

  it('takes the rectangle the dock reserved, and the window when promoted', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(1670);
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(1008);
    const { rerender } = render(<GardenFrame {...props('dock')} />);
    expect(frameEl().style.left).toBe('1110px');
    expect(frameEl().style.width).toBe('560px');

    rerender(<GardenFrame {...props('full')} />);
    // The window, minus the 12px gutter every fullscreen surface leaves.
    expect(frameEl().style.left).toBe('12px');
    expect(frameEl().style.width).toBe('1646px');
    expect(frameEl().style.height).toBe('984px');
  });

  it('stays mounted and inert when closed', () => {
    render(<GardenFrame {...props('closed')} />);
    expect(frameEl()).toHaveClass('is-closed');
    expect(frameEl().getAttribute('aria-hidden')).toBe('true');
    expect(document.querySelector('.garden-panel')).toBeNull();
  });

  it('dismisses fullscreen in place instead of collapsing toward the dock', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(1670);
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(1008);
    const base = props('full');
    const { rerender } = render(<GardenFrame {...base} />);

    rerender(<GardenFrame {...base} mode="closed" />);
    expect(frameEl()).toHaveClass('is-full-dismissal');
    expect(frameEl().style.left).toBe('12px');
    expect(frameEl().style.width).toBe('1646px');
  });

  it('is a modal only while it holds the window', () => {
    const { rerender } = render(<GardenFrame {...props('dock')} />);
    expect(frameEl().getAttribute('aria-modal')).toBeNull();

    rerender(<GardenFrame {...props('full')} />);
    expect(frameEl().getAttribute('aria-modal')).toBe('true');
  });

  it('renders nothing at all until the dock has been measured', () => {
    render(<GardenFrame {...props('dock')} dockRect={null} />);
    expect(screen.queryByTestId('garden-frame')).toBeNull();
  });

  describe('the list/board switch', () => {
    const board = { moveSeed: vi.fn(), noteSeed: vi.fn(), loaded: true };

    it('is offered in the window and not in the dock', () => {
      const { rerender } = render(<GardenFrame {...props('dock')} {...board} />);
      expect(screen.queryByRole('group', { name: 'Garden view' })).toBeNull();

      rerender(<GardenFrame {...props('full')} {...board} />);
      expect(screen.getByRole('group', { name: 'Garden view' })).toBeInTheDocument();
    });

    it('keeps the chosen fullscreen view through dismissal and a sidebar visit', () => {
      const base = props('full');
      const { rerender, unmount } = render(<GardenFrame {...base} {...board} />);
      expect(document.querySelector('.garden-panel')).not.toBeNull();

      fireEvent.click(screen.getByRole('button', { name: 'board' }));
      expect(document.querySelector('.garden-panel')).toBeNull();
      expect(document.querySelector('.garden-board')).not.toBeNull();
      expect(window.localStorage.getItem(GARDEN_FULLSCREEN_VIEW_STORAGE_KEY)).toBe('board');

      rerender(<GardenFrame {...base} {...board} mode="closed" />);
      expect(document.querySelector('.garden-board')).toBeNull();

      rerender(<GardenFrame {...base} {...board} mode="full" />);
      expect(document.querySelector('.garden-board')).not.toBeNull();

      rerender(<GardenFrame {...base} {...board} mode="dock" />);
      expect(document.querySelector('.garden-board')).toBeNull();
      expect(document.querySelector('.garden-panel')).not.toBeNull();

      rerender(<GardenFrame {...base} {...board} mode="full" />);
      expect(document.querySelector('.garden-board')).not.toBeNull();

      unmount();
      render(<GardenFrame {...base} {...board} />);
      expect(document.querySelector('.garden-board')).not.toBeNull();
    });

    it('hands Escape from the board directly to frame dismissal', () => {
      const base = props('full');
      window.localStorage.setItem(GARDEN_FULLSCREEN_VIEW_STORAGE_KEY, 'board');
      render(<GardenFrame {...base} {...board} />);

      fireEvent.keyDown(window, { key: 'Escape' });
      expect(base.onEscapeFloor).toHaveBeenCalledTimes(1);
    });
  });
});
