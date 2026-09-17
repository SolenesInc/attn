import { act, renderHook } from '@testing-library/react';
import { StrictMode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockDaemon } from '../test/mocks/daemon';
import { useWorkspaceDrag } from './useWorkspaceDrag';

const daemon = createMockDaemon();
const api = {
  sendWorkspaceMoveLeafToWorkspace: daemon.createRequest('moveLeaf'),
  sendWorkspaceMoveLeafToNewWorkspace: daemon.createRequest('newWorkspace'),
};
vi.mock('../contexts/DaemonApiContext', () => ({ useDaemonApi: () => api }));

function setup() {
  const select = vi.fn();
  const hook = renderHook(
    () =>
      useWorkspaceDrag({
        activeWorkspaceIdRef: { current: 'source' },
        getWorkspaceLeafDropSnapshot: () => null,
        handleSelectWorkspace: select,
      }),
    { wrapper: StrictMode },
  );
  return { ...hook, select };
}

describe('workspace drag lifecycle', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    daemon.clearCalls();
    daemon.setResponse('moveLeaf', {});
    daemon.setResponse('newWorkspace', {});
  });
  afterEach(() => vi.useRealTimers());

  it('keeps a new gesture when the preceding drag has a deferred end', () => {
    const { result } = setup();
    act(() => {
      result.current.handleLeafDragStart('source', undefined, 'first');
      result.current.handleLeafDragEnd();
      result.current.handleLeafDragStart('source', undefined, 'second');
    });
    act(() => vi.runOnlyPendingTimers());
    expect(result.current.leafWorkspaceDrag?.leafId).toBe('second');
    act(() => result.current.handleWorkspaceDragDrop({ id: 'target' }));
    expect(daemon.getCalls('moveLeaf').map((call) => call.args)).toEqual([
      ['source', 'target', 'second', { anchorId: '', edge: 'left', ratio: 0.32 }],
    ]);
  });

  it('cancels hover selection and deferred completion when unmounted', () => {
    const { result, unmount, select } = setup();
    act(() => {
      result.current.handleLeafDragStart('source', undefined, 'pane');
      result.current.handleWorkspaceDragEnter({ id: 'target' });
    });
    const endDrag = result.current.handleLeafDragEnd;
    unmount();
    act(() => endDrag());
    expect(vi.getTimerCount()).toBe(0);
    expect(select).not.toHaveBeenCalled();
    expect(daemon.getCalls()).toEqual([]);
  });
});
