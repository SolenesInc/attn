import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { useUiAutomationBridge } from './useUiAutomationBridge';

vi.mock('@tauri-apps/api/event', () => ({
  emit: vi.fn(async () => {}),
  listen: vi.fn(async () => () => {}),
}));

vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: vi.fn(() => ({
    innerPosition: async () => ({ x: 0, y: 0 }),
    innerSize: async () => ({ width: 0, height: 0 }),
    outerPosition: async () => ({ x: 0, y: 0 }),
    outerSize: async () => ({ width: 0, height: 0 }),
    scaleFactor: async () => 1,
    isVisible: async () => true,
  })),
}));

interface AutomationRequest {
  request_id: string;
  action: string;
  payload?: Record<string, unknown> | null;
}

function mountBridge(): (request: AutomationRequest) => Promise<void> {
  (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED = true;
  vi.mocked(isTauri).mockReturnValue(true);

  const args = {
    sessions: [],
    activeSessionId: null,
    getActivePaneIdForSession: () => '',
    createSession: vi.fn(async () => ''),
    selectSession: vi.fn(),
    selectWorkspace: vi.fn(),
    moveWorkspaceLeafToWorkspace: vi.fn(async () => undefined),
    closeSession: vi.fn(async () => {}),
    splitPane: vi.fn(async () => undefined),
    closePane: vi.fn(async () => undefined),
    focusPane: vi.fn(),
    typeInSessionPaneViaUI: vi.fn(() => true),
    isSessionPaneInputFocused: vi.fn(() => false),
    scrollSessionPaneToTop: vi.fn(() => true),
    getPaneText: vi.fn(() => ''),
    getPaneSize: vi.fn(() => null),
    getPaneVisibleContent: vi.fn(),
    getPaneVisibleStyleSummary: vi.fn(),
    getPaneBlockState: vi.fn(() => null),
    getPanePlacementState: vi.fn(() => null),
    fitSessionActivePane: vi.fn(),
    sendRuntimeInput: vi.fn(),
    isRuntimeAttached: vi.fn(() => false),
    resetSessionPaneTerminal: vi.fn(() => true),
    injectSessionPaneBytes: vi.fn(async () => true),
    injectSessionPaneBase64: vi.fn(async () => true),
    drainSessionPaneTerminal: vi.fn(async () => true),
  } as unknown as Parameters<typeof useUiAutomationBridge>[0];

  renderHook(() => useUiAutomationBridge(args));

  const calls = vi.mocked(listen).mock.calls;
  const handler = calls.length > 0 ? calls[calls.length - 1][1] : undefined;
  if (!handler) throw new Error('the bridge never subscribed to automation requests');
  return (request) => handler({ payload: request } as never) as unknown as Promise<void>;
}

describe('bridge dispatch', () => {
  beforeEach(() => {
    vi.mocked(listen).mockClear();
    document.body.innerHTML = '';
  });

  // The grid element stands in for any DOM change the app makes while a request is in
  // flight: an unsettled dispatcher answers from the DOM as it was when the request arrived.
  it('reads state the app committed after the request arrived', async () => {
    const dispatch = mountBridge();
    let answered: Record<string, unknown> | undefined;
    const emitted = vi.mocked(await import('@tauri-apps/api/event')).emit;

    const inFlight = dispatch({ request_id: 'r1', action: 'get_state' });
    const grid = document.createElement('div');
    grid.className = 'grid-view';
    document.body.appendChild(grid);
    await inFlight;

    const emits = emitted.mock.calls;
    answered = emits[emits.length - 1][1] as Record<string, unknown>;
    expect((answered.result as { gridActive: boolean }).gridActive).toBe(true);
  });

  it('answers the liveness probe without waiting for a frame', async () => {
    const dispatch = mountBridge();
    let frameRan = false;
    requestAnimationFrame(() => { frameRan = true; });

    await dispatch({ request_id: 'r2', action: 'ping' });

    expect(frameRan).toBe(false);
  });
});
