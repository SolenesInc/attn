import { Component, type ReactNode } from 'react';
import { render, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
import { emit, listen } from '@tauri-apps/api/event';
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

const BRIDGE_ARGS = {
  sessions: [],
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
};

function mountBridge(initialActiveSessionId: string | null) {
  (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED = true;
  vi.mocked(isTauri).mockReturnValue(true);

  const { rerender } = renderHook(
    ({ activeSessionId }: { activeSessionId: string | null }) =>
      useUiAutomationBridge({ ...BRIDGE_ARGS, activeSessionId } as unknown as Parameters<
        typeof useUiAutomationBridge
      >[0]),
    { initialProps: { activeSessionId: initialActiveSessionId } },
  );

  const calls = vi.mocked(listen).mock.calls;
  const handler = calls.length > 0 ? calls[calls.length - 1][1] : undefined;
  if (!handler) throw new Error('the bridge never subscribed to automation requests');

  return {
    rerender,
    dispatch: (request: AutomationRequest) =>
      handler({ payload: request } as never) as unknown as Promise<void>,
  };
}

function latestDispatch() {
  const calls = vi.mocked(listen).mock.calls;
  const handler = calls.length > 0 ? calls[calls.length - 1][1] : undefined;
  if (!handler) throw new Error('the bridge never subscribed to automation requests');
  return (request: AutomationRequest) => handler({ payload: request } as never) as unknown as Promise<void>;
}

function BridgeHost({ activeSessionId, abandon }: { activeSessionId: string; abandon: boolean }) {
  useUiAutomationBridge({ ...BRIDGE_ARGS, activeSessionId } as unknown as Parameters<
    typeof useUiAutomationBridge
  >[0]);
  if (abandon) throw new Error('render abandoned before commit');
  return null;
}

class DiscardRender extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? null : this.props.children;
  }
}

function lastAnswer(): Record<string, unknown> {
  const emits = vi.mocked(emit).mock.calls;
  return (emits[emits.length - 1][1] as { result: Record<string, unknown> }).result;
}

describe('bridge dispatch', () => {
  beforeEach(() => {
    vi.mocked(listen).mockClear();
    vi.mocked(emit).mockClear();
    document.body.innerHTML = '';
  });

  it('reads DOM the app changed after the request arrived', async () => {
    const { dispatch } = mountBridge(null);

    const inFlight = dispatch({ request_id: 'r1', action: 'get_state' });
    const grid = document.createElement('div');
    grid.className = 'grid-view';
    document.body.appendChild(grid);
    await inFlight;

    expect((lastAnswer() as { gridActive: boolean }).gridActive).toBe(true);
  });

  it('answers with props from the render that committed during the settle', async () => {
    const { dispatch, rerender } = mountBridge('before');

    const inFlight = dispatch({ request_id: 'r2', action: 'get_state' });
    rerender({ activeSessionId: 'after' });
    await inFlight;

    expect(lastAnswer().activeSessionId).toBe('after');
  });

  it('ignores a render React threw away while the settle was in flight', async () => {
    (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED = true;
    vi.mocked(isTauri).mockReturnValue(true);
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { rerender } = render(
      <DiscardRender>
        <BridgeHost activeSessionId="committed" abandon={false} />
      </DiscardRender>,
    );

    const inFlight = latestDispatch()({ request_id: 'r4', action: 'get_state' });
    rerender(
      <DiscardRender>
        <BridgeHost activeSessionId="discarded" abandon />
      </DiscardRender>,
    );
    await inFlight;

    consoleError.mockRestore();
    expect(lastAnswer().activeSessionId).toBe('committed');
  });

  it('answers the liveness probe without waiting for a frame', async () => {
    const { dispatch } = mountBridge(null);
    let frameRan = false;
    requestAnimationFrame(() => { frameRan = true; });

    await dispatch({ request_id: 'r3', action: 'ping' });

    expect(frameRan).toBe(false);
  });
});
