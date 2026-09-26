import { act } from '@testing-library/react';
import { onTestFinished, vi } from 'vitest';

interface Observation {
  target: Element;
  width: number;
  callback: ResizeObserverCallback;
  observer: ResizeObserver;
}

function laidOutWidth(element: Element): number {
  const sized = element.closest<HTMLElement>('[style*="width"]');
  return sized ? parseFloat(sized.style.width) || 0 : 0;
}

export function layOutBlocksAcrossSizedAncestors(windowWidth: number) {
  const observations = new Set<Observation>();
  let pending = false;

  const deliver = (observation: Observation) => {
    const width = laidOutWidth(observation.target);
    if (width === observation.width) return;
    observation.width = width;
    const entry = { target: observation.target, contentRect: { width } } as ResizeObserverEntry;
    act(() => observation.callback([entry], observation.observer));
  };
  const relayout = () => {
    if (pending) return;
    pending = true;
    queueMicrotask(() => {
      pending = false;
      for (const observation of Array.from(observations)) {
        if (observation.target.isConnected) deliver(observation);
      }
    });
  };

  const browserResizeObserver = globalThis.ResizeObserver;
  globalThis.ResizeObserver = class {
    private readonly mine = new Set<Observation>();
    constructor(private readonly callback: ResizeObserverCallback) {}
    observe(target: Element) {
      const observation = { target, width: laidOutWidth(target), callback: this.callback, observer: this };
      this.mine.add(observation);
      observations.add(observation);
    }
    unobserve(target: Element) {
      for (const observation of this.mine) {
        if (observation.target === target) {
          this.mine.delete(observation);
          observations.delete(observation);
        }
      }
    }
    disconnect() {
      for (const observation of this.mine) observations.delete(observation);
      this.mine.clear();
    }
  };

  const width = Object.getOwnPropertyDescriptor(CSSStyleDeclaration.prototype, 'width')!;
  const spies = [
    vi.spyOn(CSSStyleDeclaration.prototype, 'width', 'set').mockImplementation(function (this: CSSStyleDeclaration, value: string) {
      width.set!.call(this, value);
      relayout();
    }),
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
      return laidOutWidth(this);
    }),
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(windowWidth),
  ];
  onTestFinished(() => {
    globalThis.ResizeObserver = browserResizeObserver;
    for (const spy of spies) spy.mockRestore();
  });
}
