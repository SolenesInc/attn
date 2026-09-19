import { afterEach, describe, expect, it, vi } from 'vitest';
import { waitForAutomationDom } from './uiAutomationDom';

afterEach(() => {
  document.body.innerHTML = '';
  vi.useRealTimers();
});

describe('automation DOM expectations', () => {
  it('matches an existing element immediately', async () => {
    document.body.innerHTML = '<div class="menu">Ready</div>';
    await expect(waitForAutomationDom({ selector: '.menu', textIncludes: 'Ready', timeoutMs: 1000 })).resolves.toEqual({ matched: true });
  });

  it('waits for insertion and the expected text without polling', async () => {
    vi.useFakeTimers();
    const waiting = waitForAutomationDom({ selector: '.menu', textIncludes: 'Ready', timeoutMs: 1000 });
    const element = document.createElement('div');
    element.className = 'menu';
    document.body.append(element);
    await Promise.resolve();
    element.textContent = 'Ready';
    await expect(waiting).resolves.toEqual({ matched: true });
    expect(vi.getTimerCount()).toBe(0);
  });

  it('waits for focus after insertion', async () => {
    vi.useFakeTimers();
    const waiting = waitForAutomationDom({ selector: '.menu:focus', timeoutMs: 1000 });
    const element = document.createElement('input');
    element.className = 'menu';
    document.body.append(element);
    await Promise.resolve();
    element.focus();
    await expect(waiting).resolves.toEqual({ matched: true });
    expect(vi.getTimerCount()).toBe(0);
  });

  it('waits for activeElement when focused is asked, without the window being key', async () => {
    document.body.innerHTML = '<input class="menu"><input class="other">';
    (document.querySelector('.other') as HTMLInputElement).focus();
    const waiting = waitForAutomationDom({ selector: '.menu', focused: true, timeoutMs: 1000 });
    (document.querySelector('.menu') as HTMLInputElement).focus();
    await expect(waiting).resolves.toEqual({ matched: true });
  });

  it('waits for removal', async () => {
    document.body.innerHTML = '<div class="menu"></div>';
    const waiting = waitForAutomationDom({ selector: '.menu', absent: true, timeoutMs: 1000 });
    document.querySelector('.menu')!.remove();
    await expect(waiting).resolves.toEqual({ matched: true });
  });

  it('reports the failed expectation and releases observers at the deadline', async () => {
    vi.useFakeTimers();
    const disconnect = vi.spyOn(MutationObserver.prototype, 'disconnect');
    const waiting = waitForAutomationDom({ selector: '.missing:focus', timeoutMs: 1000 });
    const rejected = expect(waiting).rejects.toThrow('dom_wait timeoutMs=1000 exceeded: selector=.missing:focus');
    await vi.advanceTimersByTimeAsync(1000);
    await rejected;
    expect(disconnect).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(0);
    disconnect.mockRestore();
  });
});
