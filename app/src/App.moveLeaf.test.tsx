import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, waitFor } from '@testing-library/react';
import App from './App';
import { PROTOCOL_VERSION } from './hooks/useDaemonSocket';

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  sent: string[] = [];

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
    queueMicrotask(() => {
      this.readyState = FakeWebSocket.OPEN;
      this.onopen?.(new Event('open'));
    });
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.(new CloseEvent('close'));
  }

  emit(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) } as MessageEvent);
  }
}

vi.mock('@tauri-apps/plugin-deep-link', () => ({
  onOpenUrl: vi.fn(async () => () => {}),
  getCurrent: vi.fn(async () => []),
}));
vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));
vi.mock('./components/GhosttyTerminal', async () => {
  const React = await import('react');
  return { GhosttyTerminal: React.forwardRef(function MockTerminal() { return null; }) };
});
vi.mock('./pty/bridge', async () => {
  const actual = await vi.importActual<typeof import('./pty/bridge')>('./pty/bridge');
  return { ...actual, ptySpawn: vi.fn(async () => {}) };
});

const SOURCE = 'ws-source';
const TARGET = 'ws-target';

function session(id: string, label: string, workspaceId: string) {
  return {
    id,
    label,
    agent: 'claude',
    directory: '/tmp/repo',
    workspace_id: workspaceId,
    state: 'idle',
  };
}

function pane(paneId: string, sessionId: string, workspaceId: string, title: string) {
  return {
    pane_id: paneId,
    session_id: sessionId,
    runtime_id: sessionId,
    workspace_id: workspaceId,
    kind: 'agent',
    status: 'ready',
    title,
  };
}

function lonePaneWorkspace(id: string, title: string, paneId: string, sessionId: string) {
  return {
    id,
    title,
    directory: '/tmp/repo',
    status: 'idle',
    muted: false,
    layout: {
      workspace_id: id,
      active_pane_id: paneId,
      layout_json: JSON.stringify({ type: 'pane', pane_id: paneId }),
      panes: [pane(paneId, sessionId, id, title)],
    },
  };
}

function paneSessionIds(root: ParentNode): string[] {
  return Array.from(root.querySelectorAll('[data-pane-kind="agent"]'))
    .map((node) => node.getAttribute('data-pane-session-id') || '')
    .sort();
}

function renderedPanes(workspaceId: string): string[] {
  const workspace = document.querySelector(
    `.session-terminal-workspace[data-workspace-id="${workspaceId}"]`,
  );
  return workspace ? paneSessionIds(workspace) : [];
}

function renderedWorkspaceIds(): string[] {
  return Array.from(document.querySelectorAll('.session-terminal-workspace'))
    .map((node) => node.getAttribute('data-workspace-id') || '')
    .sort();
}

describe('a pane moved to another workspace', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  it('renders in the target workspace once the layout and its session ownership arrive', async () => {
    const { unmount } = render(<App />);

    await waitFor(() => {
      expect(FakeWebSocket.instances.length).toBeGreaterThan(0);
    });
    const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
    await waitFor(() => {
      expect(ws.readyState).toBe(FakeWebSocket.OPEN);
    });

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: PROTOCOL_VERSION,
        sessions: [
          session('s-source', 'source agent', SOURCE),
          session('s-target', 'target agent', TARGET),
        ],
        workspaces: [
          lonePaneWorkspace(SOURCE, 'Source', 'pane-source', 's-source'),
          lonePaneWorkspace(TARGET, 'Target', 'pane-target', 's-target'),
        ],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    await waitFor(() => {
      expect(renderedWorkspaceIds()).toEqual([SOURCE, TARGET]);
    });
    expect(renderedPanes(SOURCE)).toEqual(['s-source']);
    expect(renderedPanes(TARGET)).toEqual(['s-target']);

    // The daemon broadcasts the target layout before the session's new owner; the
    // reverse order hides the moved session, which filters through layouts.
    act(() => {
      ws.emit({
        event: 'workspace_layout_updated',
        workspace_layout: {
          workspace_id: TARGET,
          active_pane_id: 'pane-source',
          layout_json: JSON.stringify({
            type: 'split',
            split_id: 'split-1',
            direction: 'horizontal',
            ratio: 0.5,
            children: [
              { type: 'pane', pane_id: 'pane-target' },
              { type: 'pane', pane_id: 'pane-source' },
            ],
          }),
          panes: [
            pane('pane-target', 's-target', TARGET, 'target agent'),
            pane('pane-source', 's-source', TARGET, 'source agent'),
          ],
        },
      });
      ws.emit({
        event: 'session_state_changed',
        session: session('s-source', 'source agent', TARGET),
      });
    });

    await waitFor(() => {
      expect(renderedPanes(TARGET)).toEqual(['s-source', 's-target']);
    });
    // The checkpoint above rendered the source workspace holding this pane, so its
    // absence here is the move landing rather than a workspace that never mounted.
    expect(renderedWorkspaceIds()).toEqual([TARGET]);
    expect(paneSessionIds(document)).toEqual(['s-source', 's-target']);

    unmount();
  });
});
