import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { useRowMotion, type RowMotionLayout } from './useRowMotion';

function row(...leafIds: string[]): RowMotionLayout {
  return {
    rowKey: 'row',
    firstColumnIndex: 0,
    widthPx: 900,
    heightPx: 600,
    bounds: new Map(leafIds.map((leafId, index) => {
      const left = index / 3;
      const width = 1 / 3;
      return [leafId, { left, top: 0, width, height: 1, right: left + width, bottom: 1, centerX: left + width / 2, centerY: 0.5 }];
    })),
  };
}

function stillFrame(): HTMLCanvasElement {
  const frame = document.createElement('canvas');
  frame.width = 1476;
  frame.height = 1974;
  return frame;
}

describe('a tile that leaves the row', () => {
  let container: HTMLDivElement;
  const containerRef = { get current() { return container; } };

  function tile(leafId: string): HTMLElement {
    const element = document.createElement('div');
    element.dataset.paneId = leafId;
    element.innerHTML = '<div class="desktop-pane-body"></div>';
    container.appendChild(element);
    return element;
  }

  function mountRow(first: RowMotionLayout) {
    return renderHook(
      ({ layout, paused }: { layout: RowMotionLayout | null; paused: boolean }) =>
        useRowMotion({ containerRef, layout, paused, mountedLeavesKey: layout ? [...layout.bounds.keys()].join() : '' }),
      { initialProps: { layout: first, paused: false } as { layout: RowMotionLayout | null; paused: boolean } },
    );
  }

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
  });

  afterEach(() => container.remove());

  it('gives up its still frame once the slide has finished', async () => {
    const leaving = tile('a');
    tile('b');
    const hook = mountRow(row('a', 'b'));
    hook.rerender({ layout: row('a', 'b'), paused: false });
    const frame = stillFrame();
    act(() => {
      hook.result.current.keepLeavingFrame('a', frame);
      leaving.remove();
      hook.rerender({ layout: row('b'), paused: false });
    });
    expect(frame.isConnected).toBe(true);
    expect([frame.width, frame.height]).toEqual([1476, 1974]);
    await act(async () => {});
    expect(frame.isConnected).toBe(false);
    expect([frame.width, frame.height]).toEqual([0, 0]);
  });

  it('gives up a still frame nothing will show', () => {
    tile('a');
    const hook = mountRow(row('a'));
    const replaced = stillFrame();
    const reclaimed = stillFrame();
    act(() => {
      hook.result.current.keepLeavingFrame('a', replaced);
      hook.result.current.keepLeavingFrame('a', reclaimed);
    });
    expect([replaced.width, replaced.height]).toEqual([0, 0]);
    expect(reclaimed.width).toBe(1476);
    act(() => hook.result.current.keepLeavingFrame('a', null));
    expect([reclaimed.width, reclaimed.height]).toEqual([0, 0]);
  });
});
