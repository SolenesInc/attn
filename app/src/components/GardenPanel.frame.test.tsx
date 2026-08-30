import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { GardenPanel } from './GardenPanel';
import { useGardenWalk } from '../store/gardenWalk';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import type { Seed } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
    state_changed_at: '2026-08-20T09:00:00Z',
    state_changed_at_exact: true,
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
function plot(id: string, title: string, parent?: string): Seed {
  return seed({
    id,
    title,
    edges: parent ? [{ kind: 'part-of', to: parent }] : [],
    plot_progress: { total: 1, done: 0, withered: 0, growing: 0, dormant: 0, ready: 1, blocked: 0 },
  });
}

const crown = plot('s-crown1', 'the migration');
const middle = plot('s-mid111', 'one panel', 's-crown1');
const leaf = seed({ id: 's-leaf11', title: 'the actual work', edges: [{ kind: 'part-of', to: 's-mid111' }] });
const world = [crown, middle, leaf];

// The ResizeObserver stub in test/setup.ts never calls anyone back; this file installs a drivable one.
const observers: Array<{ target: Element; cb: ResizeObserverCallback }> = [];
class DrivableResizeObserver {
  constructor(private cb: ResizeObserverCallback) {}
  observe(target: Element) { observers.push({ target, cb: this.cb }); }
  unobserve() {}
  disconnect() {
    for (let i = observers.length - 1; i >= 0; i--) {
      if (observers[i].cb === this.cb) observers.splice(i, 1);
    }
  }
}

let boxWidth = 0;
function widen(width: number) {
  boxWidth = width;
  act(() => {
    for (const { target, cb } of observers.slice()) {
      cb([{ target, contentRect: { width } } as unknown as ResizeObserverEntry], {} as ResizeObserver);
    }
  });
}

function open(title: RegExp | string) {
  fireEvent.click(screen.getByRole('button', { name: title }));
}
function columns() {
  return document.querySelectorAll('.garden-column:not(.garden-column--reader)');
}

describe('GardenPanel in its frame', () => {
  beforeEach(() => {
    observers.length = 0;
    boxWidth = 0;
    useGardenWalk.setState({ trail: [] });
    vi.stubGlobal('ResizeObserver', DrivableResizeObserver);
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(() => boxWidth);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    _resetEscapeStackForTest();
  });

  describe('the walk follows the width, not the caller', () => {
    it('stacks in a dock-sized box and columns in a window-sized one', () => {
      boxWidth = 520;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      expect(document.querySelector('.garden-panel.is-columns')).toBeNull();

      widen(1400);
      expect(document.querySelector('.garden-panel.is-columns')).not.toBeNull();

      widen(520);
      expect(document.querySelector('.garden-panel.is-columns')).toBeNull();
    });

    it('leaves no observer behind when the renderer changes', () => {
      boxWidth = 520;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      expect(observers).toHaveLength(1);

      widen(1400);
      expect(observers).toHaveLength(1);

      widen(520);
      expect(observers).toHaveLength(1);
    });

    it("keeps the reader's place across the crossing, both ways", () => {
      boxWidth = 520;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      open(/the migration/);
      open(/one panel/);
      expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();

      widen(1400);
      expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();

      widen(520);
      expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();
    });

    it('adds the third column only once the box is wide enough for it', () => {
      boxWidth = 1200;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      open(/the migration/);
      open(/one panel/);
      expect(columns()).toHaveLength(2);

      widen(1780);
      expect(columns()).toHaveLength(3);
    });
  });

  describe('frame dismissal', () => {
    function framedPanel() {
      const onEscapeFloor = vi.fn();
      const onClose = vi.fn();
      boxWidth = 520;
      render(
        <GardenPanel
          isOpen
          onClose={onClose}
          onEscapeFloor={onEscapeFloor}
          seedsTotal={world.length}
          seeds={world}
        />,
      );
      return { onEscapeFloor, onClose };
    }
    const escape = () => fireEvent.keyDown(window, { key: 'Escape' });

    it('hands the first Escape to the frame without changing the walk or query', () => {
      const { onEscapeFloor, onClose } = framedPanel();
      open(/the migration/);
      open(/one panel/);
      const field = screen.getByRole('combobox') as HTMLInputElement;
      fireEvent.change(field, { target: { value: 'work' } });

      escape();
      expect(field.value).toBe('work');
      expect(useGardenWalk.getState().trail).toEqual(['s-crown1', 's-mid111']);
      expect(onEscapeFloor).toHaveBeenCalledTimes(1);
      expect(onClose).not.toHaveBeenCalled();
    });

    it('dismisses from a restored trail without climbing it first', () => {
      useGardenWalk.setState({ trail: ['s-crown1', 's-mid111'] });
      const { onEscapeFloor } = framedPanel();
      expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();

      escape();
      expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();
      expect(onEscapeFloor).toHaveBeenCalledTimes(1);
    });

    it('leaves Escape alone when there is no frame under it', () => {
      boxWidth = 520;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      escape();
      expect(document.querySelector('.garden-panel')).not.toBeNull();
    });
  });

  describe('the header control', () => {
    it('is absent unless there is another frame to go to', () => {
      boxWidth = 520;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      expect(screen.queryByLabelText(/expand the garden/i)).toBeNull();
    });

    it('promotes from the dock and returns from the window', () => {
      const onToggleFrame = vi.fn();
      boxWidth = 520;
      const { rerender } = render(
        <GardenPanel
          isOpen
          frame="dock"
          onToggleFrame={onToggleFrame}
          onClose={vi.fn()}
          seedsTotal={world.length}
          seeds={world}
        />,
      );
      fireEvent.click(screen.getByLabelText(/expand the garden/i));
      expect(onToggleFrame).toHaveBeenCalledTimes(1);

      rerender(
        <GardenPanel
          isOpen
          frame="full"
          onToggleFrame={onToggleFrame}
          onClose={vi.fn()}
          seedsTotal={world.length}
          seeds={world}
        />,
      );
      fireEvent.click(screen.getByLabelText(/return the garden to the dock/i));
      expect(onToggleFrame).toHaveBeenCalledTimes(2);
    });
  });
});
