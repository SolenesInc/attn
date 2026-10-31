import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useGhosttyPaneRuntime } from './useGhosttyPaneRuntime';
import {
  createPaneRuntimeEventRouterController,
  type PaneRuntimeEventRouter,
} from './paneRuntimeEventRouter';
import type { GhosttyTerminalHandle } from '../GhosttyTerminal';

const { mockPtyAttach, mockPtyDetach, mockPtyResize, mockPtyWrite } = vi.hoisted(() => ({
  mockPtyAttach: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtyDetach: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtyResize: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtyWrite: vi.fn((_request?: unknown) => Promise.resolve()),
}));

vi.mock('../../pty/bridge', () => ({
  ptyAttach: mockPtyAttach,
  ptyDetach: mockPtyDetach,
  ptyResize: mockPtyResize,
  ptyWrite: mockPtyWrite,
}));

function createTerminal(): GhosttyTerminalHandle {
  return {
    fit: vi.fn(),
    openFind: vi.fn(),
    focus: vi.fn(() => true),
    typeTextViaInput: vi.fn(() => true),
    isInputFocused: vi.fn(() => true),
    getBounds: vi.fn(() => null),
    write: vi.fn(() => Promise.resolve()),
    resizeLocal: vi.fn(() => Promise.resolve()),
    restoreSnapshot: vi.fn(() => Promise.resolve()),
    reset: vi.fn(),
    setSurfaceReleased: vi.fn(),
    scrollToTop: vi.fn(() => true),
    getText: vi.fn(() => ''),
    getSize: vi.fn(() => ({ cols: 120, rows: 40 })),
    hasMeasuredSize: vi.fn(() => true),
    overflowsContainer: vi.fn(() => false),
    getVisibleContent: vi.fn() as never,
    getVisibleStyleSummary: vi.fn() as never,
    getBlockState: vi.fn() as never,
    getPlacementState: vi.fn() as never,
    seedBlocks: vi.fn(() => Promise.resolve()),
    applyPlacements: vi.fn(() => Promise.resolve()),
    seedPlacements: vi.fn(() => Promise.resolve()),
    drain: vi.fn(() => Promise.resolve()),
  };
}

describe('useGhosttyPaneRuntime shared runtime holds', () => {
  const pane = { paneId: 'pane-shared', runtimeId: 'runtime-shared', paneKind: 'agent' as const };
  let router: PaneRuntimeEventRouter;

  beforeEach(() => {
    mockPtyAttach.mockReset();
    mockPtyAttach.mockImplementation(() => Promise.resolve());
    mockPtyDetach.mockClear();
    router = { registerBinding: vi.fn(() => () => {}) };
  });

  it('keeps the daemon attachment while another desktop still holds the runtime', async () => {
    const first = renderHook(() => useGhosttyPaneRuntime([pane], pane.paneId, router, { current: true }));
    const second = renderHook(() => useGhosttyPaneRuntime([pane], pane.paneId, router, { current: true }));

    await act(async () => {
      await first.result.current.handleTerminalReady(pane.paneId)(createTerminal());
      await second.result.current.handleTerminalReady(pane.paneId)(createTerminal());
    });
    expect(mockPtyAttach).toHaveBeenCalledTimes(2);

    second.unmount();
    expect(mockPtyDetach).not.toHaveBeenCalled();

    first.unmount();
    expect(mockPtyDetach).toHaveBeenCalledTimes(1);
    expect(mockPtyDetach).toHaveBeenCalledWith({ id: pane.runtimeId });
  });

  it('keeps delivering output to the surviving view after a transient duplicate view unmounts', async () => {
    const controller = createPaneRuntimeEventRouterController();
    const survivingTerminal = createTerminal();
    const transientTerminal = createTerminal();
    const first = renderHook(() => useGhosttyPaneRuntime([pane], pane.paneId, controller, { current: true }));
    const second = renderHook(() => useGhosttyPaneRuntime([pane], pane.paneId, controller, { current: true }));

    await act(async () => {
      await first.result.current.handleTerminalReady(pane.paneId)(survivingTerminal);
      await second.result.current.handleTerminalReady(pane.paneId)(transientTerminal);
    });

    second.unmount();
    expect(mockPtyDetach).not.toHaveBeenCalled();

    controller.handleEvent({ event: 'data', id: pane.runtimeId, data: btoa('after') });

    const writtenText = (terminal: GhosttyTerminalHandle) => vi.mocked(terminal.write).mock.calls
      .map(([bytes]) => new TextDecoder().decode(bytes as Uint8Array));
    expect(writtenText(survivingTerminal)).toContain('after');
    expect(writtenText(transientTerminal)).not.toContain('after');
  });

  it('detaches when a failed duplicate attach drops the last hold after the attached view left', async () => {
    const attached = renderHook(() => useGhosttyPaneRuntime([pane], pane.paneId, router, { current: true }));
    await act(async () => {
      await attached.result.current.handleTerminalReady(pane.paneId)(createTerminal());
    });

    let rejectAttach: (error: Error) => void = () => {};
    mockPtyAttach.mockImplementationOnce(() => new Promise<void>((_, reject) => {
      rejectAttach = reject;
    }));
    const duplicate = renderHook(() => useGhosttyPaneRuntime([pane], pane.paneId, router, { current: true }));
    let duplicateReady: Promise<void> = Promise.resolve();
    await act(async () => {
      duplicateReady = duplicate.result.current.handleTerminalReady(pane.paneId)(createTerminal());
    });

    attached.unmount();
    expect(mockPtyDetach).not.toHaveBeenCalled();

    await act(async () => {
      rejectAttach(new Error('daemon busy'));
      await duplicateReady;
    });
    expect(mockPtyDetach).toHaveBeenCalledTimes(1);
    expect(mockPtyDetach).toHaveBeenCalledWith({ id: pane.runtimeId });
  });

  it('keeps the stream from a stale successful attach when the replacement model fails to attach', async () => {
    const settlers: Array<{ resolve: () => void; reject: (error: Error) => void }> = [];
    mockPtyAttach.mockImplementation(() => new Promise<void>((resolve, reject) => {
      settlers.push({ resolve, reject });
    }));
    const view = renderHook(() => useGhosttyPaneRuntime([pane], pane.paneId, router, { current: true }));
    let firstReady: Promise<void> = Promise.resolve();
    let replacementReady: Promise<void> = Promise.resolve();
    await act(async () => {
      firstReady = view.result.current.handleTerminalReady(pane.paneId)(createTerminal());
      replacementReady = view.result.current.handleTerminalReady(pane.paneId)(createTerminal());
    });
    expect(settlers).toHaveLength(1);

    await act(async () => {
      settlers[0].resolve();
      await firstReady;
    });
    expect(settlers).toHaveLength(2);

    await act(async () => {
      settlers[1].reject(new Error('daemon busy'));
      await replacementReady;
    });
    expect(mockPtyDetach).not.toHaveBeenCalled();

    view.unmount();
    expect(mockPtyDetach).toHaveBeenCalledTimes(1);
    expect(mockPtyDetach).toHaveBeenCalledWith({ id: pane.runtimeId });
  });

  it('never detaches a runtime it did not attach while another desktop holds it', async () => {
    const holder = renderHook(() => useGhosttyPaneRuntime([pane], pane.paneId, router, { current: true }));
    const bystander = renderHook(() => useGhosttyPaneRuntime([pane], pane.paneId, router, { current: true }));

    await act(async () => {
      await holder.result.current.handleTerminalReady(pane.paneId)(createTerminal());
    });

    bystander.unmount();
    expect(mockPtyDetach).not.toHaveBeenCalled();

    holder.unmount();
    expect(mockPtyDetach).toHaveBeenCalledWith({ id: pane.runtimeId });
  });

  it('releases the hold when the pane leaves the desktop so a later holder detaches cleanly', async () => {
    const first = renderHook(
      ({ panes }) => useGhosttyPaneRuntime(panes, pane.paneId, router, { current: true }),
      { initialProps: { panes: [pane] } },
    );
    await act(async () => {
      await first.result.current.handleTerminalReady(pane.paneId)(createTerminal());
    });

    first.rerender({ panes: [] });
    expect(mockPtyDetach).toHaveBeenCalledTimes(1);

    const second = renderHook(() => useGhosttyPaneRuntime([pane], pane.paneId, router, { current: true }));
    await act(async () => {
      await second.result.current.handleTerminalReady(pane.paneId)(createTerminal());
    });
    second.unmount();
    expect(mockPtyDetach).toHaveBeenCalledTimes(2);
  });
});
