import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DelegationChainController, type ChainDismissal } from './delegationChainController';

let controller: DelegationChainController;
let host: HTMLDivElement;
let terminal: HTMLInputElement;
let row: HTMLButtonElement;
let header: HTMLButtonElement;
let disconnect: () => void;
const sessionIds = new Set(['build', 'review']);
const scope = { navigationKey: 'session:build', blocked: false, sessionIds };

beforeEach(() => {
  vi.useFakeTimers();
  controller = new DelegationChainController();
  controller.configure(scope);
  disconnect = controller.connect();
  host = document.createElement('div');
  terminal = document.createElement('input');
  row = document.createElement('button');
  header = document.createElement('button');
  host.append(terminal, row, header);
  document.body.append(host);
  terminal.focus();
});

afterEach(() => {
  disconnect();
  host.remove();
  vi.useRealTimers();
});

describe('delegation popup ownership', () => {
  it('transfers hover ownership and ignores a late leave from the previous anchor', () => {
    controller.show('build', row, 'hover');
    controller.leave('build', row);
    controller.show('build', header, 'hover');
    controller.leave('build', row);
    vi.runOnlyPendingTimers();
    expect(controller.getSnapshot()?.anchor).toBe(header);
    controller.leave('build', header);
    vi.runOnlyPendingTimers();
    expect(controller.getSnapshot()).toBeNull();
  });

  it('pins a hover without surrendering ownership to another hovered agent', () => {
    controller.show('build', row, 'hover');
    controller.show('build', row, 'pinned');
    controller.leave('build', row);
    controller.show('review', header, 'hover');
    vi.runOnlyPendingTimers();
    expect(controller.getSnapshot()).toMatchObject({ sessionId: 'build', mode: 'pinned', anchor: row });
  });

  it.each(['dismiss', 'outside-scroll', 'anchor-removed'] satisfies ChainDismissal[])('%s returns focus once and suppresses stationary hover', (reason) => {
    controller.show('build', row, 'hover');
    const focus = controller.getSnapshot()!.focus;
    controller.dismiss(reason);
    expect(controller.returnFocus(focus)).toBe(terminal);
    expect(controller.returnFocus(focus)).toBe(false);
    controller.show('build', row, 'hover');
    expect(controller.getSnapshot()).toBeNull();
    window.dispatchEvent(new PointerEvent('pointermove', { clientX: 1, clientY: 1 }));
    controller.show('build', row, 'hover');
    expect(controller.getSnapshot()?.sessionId).toBe('build');
  });

  it.each(['outside-pointer', 'handoff', 'selection', 'context'] satisfies ChainDismissal[])('%s leaves focus to its destination', (reason) => {
    controller.show('build', row, 'pinned');
    const focus = controller.getSnapshot()!.focus;
    controller.dismiss(reason);
    expect(controller.returnFocus(focus)).toBe(false);
  });

  it('does not let a previous popup reclaim focus after a newer popup closes', () => {
    controller.show('build', row, 'hover');
    const previous = controller.getSnapshot()!.focus;
    controller.show('review', header, 'hover');
    const current = controller.getSnapshot()!.focus;
    controller.dismiss();
    expect(controller.returnFocus(previous)).toBe(false);
    expect(controller.returnFocus(current)).toBe(terminal);
  });

  it('keeps the original focus target through the Action Menu handoff', () => {
    controller.show('build', row, 'hover');
    controller.prepareCommand();
    controller.configure({ ...scope, blocked: true });
    header.focus();
    controller.open('build');
    controller.configure(scope);
    const focus = controller.getSnapshot()!.focus;
    controller.dismiss();
    expect(controller.returnFocus(focus)).toBe(terminal);
  });

  it.each([{ blocked: true }, { navigationKey: 'session:review' }, { sessionIds: new Set<string>() }])('invalidates an open popup when its scope changes: %j', (change) => {
    controller.show('build', row, 'pinned');
    const focus = controller.getSnapshot()!.focus;
    controller.configure({ ...scope, ...change });
    expect(controller.getSnapshot()).toBeNull();
    expect(controller.returnFocus(focus)).toBe(false);
    controller.configure(scope);
    expect(controller.getSnapshot()).toBeNull();
  });

  it('ignores unrelated detach and restores the fallback after its own anchor disappears', () => {
    const fallback = vi.fn(() => terminal.focus());
    controller.configure({ ...scope, onRestoreFocusFallback: fallback });
    controller.show('build', row, 'pinned');
    const focus = controller.getSnapshot()!.focus;
    controller.detach(header);
    expect(controller.getSnapshot()?.anchor).toBe(row);
    row.remove();
    controller.detach(row);
    expect(controller.getSnapshot()).toBeNull();
    expect(controller.returnFocus(focus)).toBe(false);
    expect(fallback).toHaveBeenCalledOnce();
    expect(terminal).toHaveFocus();
  });

  it('cancels pending hover work when disconnected', () => {
    controller.show('build', row, 'hover');
    controller.leave('build', row);
    disconnect();
    vi.runOnlyPendingTimers();
    expect(controller.getSnapshot()?.sessionId).toBe('build');
  });

  it('selects only after the old focus trap has released the keyboard', () => {
    const select = vi.fn();
    controller.show('build', row, 'pinned');
    const focus = controller.getSnapshot()!.focus;
    controller.select('review', select);
    expect(controller.getSnapshot()).toBeNull();
    expect(select).not.toHaveBeenCalled();
    expect(controller.returnFocus(focus)).toBe(false);
    controller.finishClose(focus);
    expect(select).toHaveBeenCalledExactlyOnceWith('review');
    controller.finishClose(focus);
    expect(select).toHaveBeenCalledOnce();
  });

  it('cancels a pending selection if another surface takes ownership first', () => {
    const select = vi.fn();
    controller.show('build', row, 'pinned');
    const focus = controller.getSnapshot()!.focus;
    controller.select('review', select);
    controller.configure({ ...scope, blocked: true });
    controller.finishClose(focus);
    expect(select).not.toHaveBeenCalled();
  });
});
