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

const PROFILE = { id: 'profile-main', name: 'Main', current_desktop_id: 'desktop-source', revision: 1 };
const SOURCE = 'desktop-source';
const TARGET = 'desktop-target';

type PaneRef = { paneId: string; sessionId: string };

function session(id: string, label: string) {
  return {
    id,
    label,
    agent: 'claude',
    directory: '/tmp/repo',
    workspace_id: '',
    profile_id: PROFILE.id,
    state: 'idle',
  };
}

function nestedSplits(id: string, panes: PaneRef[]): unknown {
  if (panes.length === 1) {
    return { type: 'pane', pane_id: panes[0].paneId };
  }
  return {
    type: 'split',
    split_id: `${id}-split-${panes.length}`,
    direction: 'horizontal',
    ratio: 0.5,
    children: [{ type: 'pane', pane_id: panes[0].paneId }, nestedSplits(id, panes.slice(1))],
  };
}

function desktop(id: string, slot: number, panes: PaneRef[], revision = 1) {
  return {
    id,
    profile_id: PROFILE.id,
    name: '',
    shortcut_slot: slot,
    order_key: id,
    tree_json: panes.length > 0 ? JSON.stringify(nestedSplits(id, panes)) : '',
    active_pane_id: panes[0]?.paneId ?? '',
    revision,
    panes: panes.map((entry) => ({
      pane_id: entry.paneId,
      desktop_id: id,
      session_id: entry.sessionId,
      kind: 'agent',
      status: 'ready',
      title: entry.sessionId,
    })),
  };
}

function paneSessionIds(root: ParentNode): string[] {
  return Array.from(root.querySelectorAll('[data-pane-kind="agent"]'))
    .map((node) => node.getAttribute('data-pane-session-id') || '')
    .sort();
}

function renderedPanes(desktopId: string): string[] {
  const surface = document.querySelector(
    `.session-terminal-workspace[data-workspace-id="${desktopId}"]`,
  );
  return surface ? paneSessionIds(surface) : [];
}

function renderedDesktopIds(): string[] {
  return Array.from(document.querySelectorAll('.session-terminal-workspace'))
    .map((node) => node.getAttribute('data-workspace-id') || '')
    .sort();
}

async function connect(sessions: ReturnType<typeof session>[], desktops: ReturnType<typeof desktop>[]) {
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
      sessions,
      workspaces: [],
      profiles: [PROFILE],
      selected_profile_id: PROFILE.id,
      desktops,
      prs: [],
      repos: [],
      authors: [],
      settings: {},
    });
  });
  return ws;
}

describe('a pane moved to another desktop', () => {
  beforeEach(() => {
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  const stay = { paneId: 'pane-stay', sessionId: 's-stay' };
  const moved = { paneId: 'pane-moved', sessionId: 's-moved' };
  const target = { paneId: 'pane-target', sessionId: 's-target' };

  it('renders on the target desktop once the arrangement moves it and follows there', async () => {
    const { unmount } = render(<App />);
    const ws = await connect(
      [session(stay.sessionId, 'staying'), session(moved.sessionId, 'moved'), session(target.sessionId, 'target')],
      [desktop(SOURCE, 1, [stay, moved]), desktop(TARGET, 2, [target])],
    );

    await waitFor(() => {
      expect(renderedPanes(SOURCE)).toEqual(['s-moved', 's-stay']);
    });
    expect(renderedDesktopIds()).toEqual([SOURCE]);

    act(() => {
      ws.emit({
        event: 'profile_arrangement_changed',
        profile: { ...PROFILE, current_desktop_id: TARGET, revision: 2 },
        desktops: [desktop(SOURCE, 1, [stay], 2), desktop(TARGET, 2, [target, moved], 2)],
      });
    });

    await waitFor(() => {
      expect(renderedPanes(TARGET)).toEqual(['s-moved', 's-target']);
    });
    expect(renderedPanes(SOURCE)).toEqual(['s-stay']);
    expect(renderedDesktopIds()).toEqual([SOURCE, TARGET]);
    expect(paneSessionIds(document)).toEqual(['s-moved', 's-stay', 's-target']);

    unmount();
  });

  it('keeps a moved agent on its new desktop when a stale session broadcast arrives', async () => {
    const { unmount } = render(<App />);
    const ws = await connect(
      [session(stay.sessionId, 'staying'), session(moved.sessionId, 'moved'), session(target.sessionId, 'target')],
      [desktop(SOURCE, 1, [stay, moved]), desktop(TARGET, 2, [target])],
    );
    await waitFor(() => {
      expect(renderedPanes(SOURCE)).toEqual(['s-moved', 's-stay']);
    });

    act(() => {
      ws.emit({
        event: 'profile_arrangement_changed',
        profile: { ...PROFILE, current_desktop_id: TARGET, revision: 2 },
        desktops: [desktop(SOURCE, 1, [stay], 2), desktop(TARGET, 2, [target, moved], 2)],
      });
      ws.emit({ event: 'session_state_changed', session: session(moved.sessionId, 'moved') });
    });

    await waitFor(() => {
      expect(renderedPanes(TARGET)).toEqual(['s-moved', 's-target']);
    });
    expect(renderedPanes(SOURCE)).toEqual(['s-stay']);

    unmount();
  });
});
