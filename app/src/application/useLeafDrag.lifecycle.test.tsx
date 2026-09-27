import { act, renderHook } from '@testing-library/react';
import { StrictMode, type ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DaemonApiProvider } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { agentDesktop, arrangeDesktops, TEST_PROFILE_ID } from '../test/desktops';
import { createMockDaemonApi } from '../test/mocks/daemon';
import { ProfileActionResultMessageEvent, type ProfileActionResultMessage } from '../types/generated';
import { useLeafDrag } from './useLeafDrag';

const ok: ProfileActionResultMessage = {
  event: ProfileActionResultMessageEvent.ProfileActionResult,
  request_id: 'r',
  action: 'x',
  success: true,
};

function setup() {
  const api = {
    sendDesktopMoveLeaf: vi.fn(async (_move: unknown): Promise<ProfileActionResultMessage> => ok),
    sendDesktopCreate: vi.fn(
      async (_profileId: string): Promise<ProfileActionResultMessage> => ({ ...ok, desktops: [agentDesktop('d-new', null, [])] }),
    ),
    sendDesktopSetCurrent: vi.fn(async (_profileId: string, _desktopId: string): Promise<ProfileActionResultMessage> => ok),
  };
  const handleSelectDesktop = vi.fn();
  const showError = vi.fn();
  const currentDesktopIdRef = { current: 'd1' as string | null };
  const wrapper = ({ children }: { children: ReactNode }) => (
    <StrictMode>
      <DaemonApiProvider api={createMockDaemonApi(api)}>{children}</DaemonApiProvider>
    </StrictMode>
  );
  const rendered = renderHook(
    () =>
      useLeafDrag({
        currentDesktopIdRef,
        getDesktopLeafDropSnapshot: () => null,
        handleSelectDesktop,
        showError,
      }),
    { wrapper },
  );
  return { ...rendered, api, handleSelectDesktop, showError, currentDesktopIdRef };
}

describe('leaf drag lifecycle', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('keeps a new gesture when the preceding drag has a deferred end', () => {
    const { result } = setup();
    act(() => {
      result.current.handleLeafDragStart('first');
      result.current.handleLeafDragEnd();
      result.current.handleLeafDragStart('second');
    });
    act(() => vi.runOnlyPendingTimers());
    expect(result.current.leafDragPreview?.draggingLeafId).toBe('second');
  });

  it('clears the preview after the drag ends', () => {
    const { result } = setup();
    act(() => {
      result.current.handleLeafDragStart('pane');
      result.current.handleLeafDragEnd();
    });
    act(() => vi.runOnlyPendingTimers());
    expect(result.current.leafDragPreview).toBeNull();
    expect(result.current.leafDesktopDrag).toBeNull();
  });

  it('schedules nothing when the drag ends after unmount', () => {
    const { result, unmount } = setup();
    act(() => result.current.handleLeafDragStart('pane'));
    const endDrag = result.current.handleLeafDragEnd;
    unmount();
    act(() => endDrag());
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe('dropping a leaf on a sidebar desktop', () => {
  beforeEach(() => {
    useProfilesStore.setState(useProfilesStore.getInitialState(), true);
    arrangeDesktops([
      { ...agentDesktop('d1', 1, ['s1', 's2']), revision: 4 },
      { ...agentDesktop('d2', 2, ['s3']), revision: 9 },
    ]);
  });

  it('moves the dragged pane to that desktop and shows it', async () => {
    const { result, api, handleSelectDesktop } = setup();
    act(() => result.current.handleLeafDragStart('pane-s2'));

    await act(async () => result.current.handleDesktopDragDrop({ id: 'd2' }));

    expect(handleSelectDesktop).toHaveBeenCalledWith('d2');
    expect(api.sendDesktopMoveLeaf).toHaveBeenCalledWith({
      sourceDesktopId: 'd1',
      targetDesktopId: 'd2',
      leafId: 'pane-s2',
      edge: 'left',
      leafShare: 0.32,
      expectedSourceRevision: 4,
      expectedTargetRevision: 9,
    });
  });

  it('moves a sidebar row dragged from another desktop', async () => {
    const { result, api } = setup();
    act(() => result.current.handleSessionDragStart('d2', undefined, 'pane-s3'));

    await act(async () => result.current.handleDesktopDragDrop({ id: 'd1' }));

    expect(api.sendDesktopMoveLeaf).toHaveBeenCalledWith(
      expect.objectContaining({ sourceDesktopId: 'd2', targetDesktopId: 'd1', leafId: 'pane-s3' }),
    );
  });

  it('ignores its own desktop and groups that are not desktops', async () => {
    const { result, api } = setup();
    act(() => result.current.handleLeafDragStart('pane-s2'));

    await act(async () => {
      result.current.handleDesktopDragDrop({ id: 'd1' });
      result.current.handleDesktopDragDrop({ id: 'unplaced' });
    });

    expect(api.sendDesktopMoveLeaf).not.toHaveBeenCalled();
  });

  it('creates a desktop for a drop on the new-desktop zone and shows it', async () => {
    const { result, api } = setup();
    act(() => result.current.handleLeafDragStart('pane-s2'));

    await act(async () => result.current.handleNewDesktopDrop());

    expect(api.sendDesktopCreate).toHaveBeenCalledWith(TEST_PROFILE_ID);
    expect(api.sendDesktopMoveLeaf).toHaveBeenCalledWith(
      expect.objectContaining({ targetDesktopId: 'd-new', expectedTargetRevision: 1, expectedSourceRevision: 4 }),
    );
    expect(api.sendDesktopSetCurrent).toHaveBeenCalledWith(TEST_PROFILE_ID, 'd-new');
  });

  it('moves within the desktop a surface drop lands on', async () => {
    const { result, api } = setup();
    act(() => result.current.handleLeafDragStart('pane-s2'));

    await act(async () => result.current.handleSurfaceLeafDrop('d1', 'pane-s2', 'pane-s1', 'top', 0.5));

    expect(api.sendDesktopMoveLeaf).toHaveBeenCalledWith({
      sourceDesktopId: 'd1', targetDesktopId: 'd1', leafId: 'pane-s2', anchorId: 'pane-s1', edge: 'top',
      leafShare: 0.5, expectedSourceRevision: 4, expectedTargetRevision: 4,
    });
  });

  it('moves across desktops when hovering switched to another desktop before the surface drop', async () => {
    const { result, api, currentDesktopIdRef } = setup();
    act(() => result.current.handleLeafDragStart('pane-s2'));
    currentDesktopIdRef.current = 'd2';

    await act(async () => result.current.handleSurfaceLeafDrop('d1', 'pane-s2', 'pane-s3', 'right', 0.4));

    expect(api.sendDesktopMoveLeaf).toHaveBeenCalledWith({
      sourceDesktopId: 'd1', targetDesktopId: 'd2', leafId: 'pane-s2', anchorId: 'pane-s3', edge: 'right',
      leafShare: 0.4, expectedSourceRevision: 4, expectedTargetRevision: 9,
    });
  });

  it('names a failed move', async () => {
    const { result, api, showError } = setup();
    api.sendDesktopMoveLeaf.mockRejectedValueOnce(new Error('pane is gone'));
    act(() => result.current.handleLeafDragStart('pane-s2'));

    await act(async () => result.current.handleDesktopDragDrop({ id: 'd2' }));

    expect(showError).toHaveBeenCalledWith('Could not move that pane: pane is gone');
  });
});
