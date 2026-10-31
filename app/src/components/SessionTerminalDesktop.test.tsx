import { fireEvent, render, screen } from '@testing-library/react';
import { forwardRef, useEffect, useImperativeHandle, type ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SessionTerminalDesktop } from './SessionTerminalDesktop';
import type { PaneRuntimeEventRouter } from './SessionTerminalDesktop/paneRuntimeEventRouter';
import { tileContentKey, type TerminalDesktopState } from '../types/desktop';
import { NotebookSurfaceProvider, type NotebookSurfaceContextValue } from '../contexts/NotebookSurfaceContext';

const testSurfaceValue: NotebookSurfaceContextValue = {
  makeDaemon: () => ({
    listDir: vi.fn(),
    readFile: vi.fn(),
    writeFile: vi.fn(),
    existsFile: vi.fn(),
    readAsset: vi.fn(),
    backlinksNotebook: vi.fn(),
    listFiles: vi.fn(),
    changeSignal: 0,
  }),
  changeSignalFor: () => 0,
  effectiveNotebookRoot: '',
  sendFsWatch: vi.fn(),
  sendFsUnwatch: vi.fn(),
  connectionGeneration: 0,
};
function NotebookSurfaceTestWrapper({ children }: { children: ReactNode }) {
  return <NotebookSurfaceProvider value={testSurfaceValue}>{children}</NotebookSurfaceProvider>;
}
const SESSION_PANE_ID = 'pane-session';
function createSingleAgentDesktop(): TerminalDesktopState {
  return {
    agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }],
    layoutTree: { type: 'pane', paneId: SESSION_PANE_ID },
  };
}

function createSplitDesktop(ratio = 0.5): TerminalDesktopState {
  return {
    agents: [
      { id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' },
      { id: 'pane-session-2', runtimeId: 'session-2', sessionId: 'session-2', title: 'Session 2' },
    ],
    layoutTree: {
      type: 'split',
      splitId: 'root',
      direction: 'vertical',
      ratio,
      children: [
        { type: 'pane', paneId: SESSION_PANE_ID },
        { type: 'pane', paneId: 'pane-session-2' },
      ],
    },
  };
}

function createDocumentSplitDesktop(
  ratio: number,
  ratioMode: 'automatic' | 'preferred',
): TerminalDesktopState {
  return {
    agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }],
    layoutTree: {
      type: 'split',
      splitId: 'root',
      direction: 'vertical',
      ratio,
      ratioMode,
      children: [
        { type: 'pane', paneId: SESSION_PANE_ID },
        {
          type: 'tile',
          tileId: 'doc',
          tileKind: 'markdown',
          tileParams: '/tmp/doc.md',
        },
      ],
    },
  };
}

const { registeredShortcuts } = vi.hoisted(() => ({
  registeredShortcuts: new Map<string, () => void>(),
}));

const mockEventRouter: PaneRuntimeEventRouter = {
  registerBinding: vi.fn(() => () => {}),
};

const { mockPtyAttach, mockPtyDetach, mockPtyResize, mockPtyWrite } = vi.hoisted(() => ({
  mockPtyAttach: vi.fn(() => Promise.resolve()),
  mockPtyDetach: vi.fn(() => Promise.resolve()),
  mockPtyResize: vi.fn(() => Promise.resolve()),
  mockPtyWrite: vi.fn(() => Promise.resolve()),
}));

vi.mock('../pty/bridge', () => ({
  listenPtyEvents: vi.fn(() => Promise.resolve(() => {})),
  ptyAttach: mockPtyAttach,
  ptyDetach: mockPtyDetach,
  ptyResize: mockPtyResize,
  ptyWrite: mockPtyWrite,
}));

const { mockTerminalFocus } = vi.hoisted(() => ({
  mockTerminalFocus: vi.fn(() => true),
}));

const { mockTerminalFit } = vi.hoisted(() => ({
  mockTerminalFit: vi.fn(),
}));

const { mockTerminalSetSurfaceReleased } = vi.hoisted(() => ({
  mockTerminalSetSurfaceReleased: vi.fn(),
}));

const { mockTerminalOverflowsContainer } = vi.hoisted(() => ({
  mockTerminalOverflowsContainer: vi.fn(() => false),
}));

const { renderedTerminalProps } = vi.hoisted(() => ({
  renderedTerminalProps: new Map<string, any>(),
}));

const { terminalLifecycleCounts } = vi.hoisted(() => ({
  terminalLifecycleCounts: new Map<string, { mounts: number; unmounts: number }>(),
}));

vi.mock('./GhosttyTerminal', () => ({
  GhosttyTerminal: forwardRef((props: any, ref) => {
    renderedTerminalProps.set(props.debugName, props);
    useEffect(() => {
      const current = terminalLifecycleCounts.get(props.debugName) || { mounts: 0, unmounts: 0 };
      terminalLifecycleCounts.set(props.debugName, {
        mounts: current.mounts + 1,
        unmounts: current.unmounts,
      });
      return () => {
        const latest = terminalLifecycleCounts.get(props.debugName) || { mounts: 0, unmounts: 0 };
        terminalLifecycleCounts.set(props.debugName, {
          mounts: latest.mounts,
          unmounts: latest.unmounts + 1,
        });
      };
    }, [props.debugName]);
    useImperativeHandle(ref, () => ({
      fit: mockTerminalFit,
      focus: mockTerminalFocus,
      typeTextViaInput: vi.fn(() => true),
      isInputFocused: vi.fn(() => false),
      write: vi.fn(() => Promise.resolve()),
      resizeLocal: vi.fn(),
      reset: vi.fn(),
      setSurfaceReleased: mockTerminalSetSurfaceReleased,
      scrollToTop: vi.fn(() => true),
      getText: vi.fn(() => ''),
      getSize: vi.fn(() => ({ cols: 80, rows: 24 })),
      hasMeasuredSize: vi.fn(() => true),
      overflowsContainer: mockTerminalOverflowsContainer,
      getVisibleContent: vi.fn(),
      getVisibleStyleSummary: vi.fn(),
      drain: vi.fn(() => Promise.resolve()),
    }));
    const label = typeof props.debugName === 'string' && props.debugName.startsWith('utility:')
      ? props.debugName.split(':')[2]
      : 'session pane';
    return <div data-testid="mock-terminal">{label}</div>;
  }),
}));

// Shortcuts are recorded rather than dispatched, so a test can invoke one by id.
// The bodies are duplicated because vi.mock factories are hoisted above any shared helper.
vi.mock('../shortcuts', () => ({
  useShortcut: vi.fn((id: string, handler: () => void, enabled?: boolean) => {
    if (enabled) {
      registeredShortcuts.set(id, handler);
      return;
    }
    registeredShortcuts.delete(id);
  }),
}));

vi.mock('../shortcuts/useShortcut', () => ({
  useShortcut: vi.fn((id: string, handler: () => void, enabled?: boolean) => {
    if (enabled) {
      registeredShortcuts.set(id, handler);
      return;
    }
    registeredShortcuts.delete(id);
  }),
  triggerShortcut: vi.fn(() => false),
}));

describe('SessionTerminalDesktop', () => {
  afterEach(() => {
    registeredShortcuts.clear();
    renderedTerminalProps.clear();
    terminalLifecycleCounts.clear();
    vi.mocked(mockEventRouter.registerBinding).mockClear();
    mockTerminalFit.mockReset();
    mockTerminalSetSurfaceReleased.mockReset();
    mockTerminalOverflowsContainer.mockReset().mockReturnValue(false);
    mockPtyAttach.mockReset();
    mockPtyDetach.mockReset();
    mockPtyResize.mockReset();
    mockPtyWrite.mockReset();
    delete document.documentElement.dataset.attnDesktopResizing;
    delete document.documentElement.dataset.attnDesktopResizeToken;
    delete document.documentElement.dataset.attnDesktopMouseSuppressUntil;
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  const virtualizationProps = {
    desktopId: 'desktop-session-1',
    desktopSessions: [{ id: 'session-1', label: 'Session 1', agent: 'claude', cwd: '/tmp/repo' }],
    activePaneId: SESSION_PANE_ID,
    fontSize: 14,
    enabled: true,
    eventRouter: mockEventRouter,
    onSplitPane: vi.fn(),
    onClosePane: vi.fn(),
    onFocusPane: vi.fn(),
    onNavigateOutOfSession: vi.fn(),
  };

  // An inactive session's wrapper is display:none, so its panes' GPU drawing buffers can
  // go back. A pane behind a modal is still on screen, so `enabled` must not drive this.

  it('keeps the drawing buffer when a modal disables the session', () => {
    const { rerender } = render(
      <SessionTerminalDesktop
        {...virtualizationProps}
        terminalState={createSingleAgentDesktop()}
        isActiveSession
      />,
    );

    mockTerminalSetSurfaceReleased.mockClear();
    rerender(
      <SessionTerminalDesktop
        {...virtualizationProps}
        terminalState={createSingleAgentDesktop()}
        isActiveSession
        enabled={false}
      />,
    );
    expect(mockTerminalSetSurfaceReleased.mock.calls.map(([released]) => released)).not.toContain(true);
  });

  it('accepts an authoritative split ratio that supersedes an optimistic resize', () => {
    const onResizeSplit = vi.fn(() => new Promise<void>(() => {}));
    const terminalState = createSplitDesktop();
    const { container, rerender } = render(
      <SessionTerminalDesktop
        desktopId="desktop-session-1"
        desktopSessions={[
          { id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" },
          { id: "session-2", label: "Session 2", agent: "claude", cwd: "/tmp/repo" },
        ]}
        terminalState={terminalState}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
        onResizeSplit={onResizeSplit}
      />
    );
    const panes = container.querySelector('.session-terminal-panes') as HTMLElement;
    vi.spyOn(panes, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, right: 1000, bottom: 500, width: 1000, height: 500, x: 0, y: 0, toJSON: () => {},
    });

    fireEvent.pointerDown(screen.getByRole('separator'), { button: 0, clientX: 500, clientY: 250 });
    fireEvent.pointerUp(window, { clientX: 800, clientY: 250 });
    expect(container.querySelector('.desktop-layout-metadata [data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.800');

    rerender(
      <SessionTerminalDesktop
        desktopId="desktop-session-1"
        desktopSessions={[
          { id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" },
          { id: "session-2", label: "Session 2", agent: "claude", cwd: "/tmp/repo" },
        ]}
        terminalState={createSplitDesktop(0.6)}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
        onResizeSplit={onResizeSplit}
      />
    );

    expect(container.querySelector('.desktop-layout-metadata [data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.600');
  });

  it('settles an optimistic resize when the daemon echoes it as preferred', () => {
    const onResizeSplit = vi.fn(() => new Promise<void>(() => {}));
    const commonProps = {
      desktopId: 'desktop-session-1',
      desktopSessions: [{ id: 'session-1', label: 'Session 1', agent: 'claude' as const, cwd: '/tmp/repo' }],
      activePaneId: SESSION_PANE_ID,
      fontSize: 14,
      enabled: true,
      isActiveSession: true,
      eventRouter: mockEventRouter,
      onSplitPane: vi.fn(),
      onClosePane: vi.fn(),
      onFocusPane: vi.fn(),
      onNavigateOutOfSession: vi.fn(),
      onResizeSplit,
      onUndockTile: vi.fn(),
      tileContents: {
        [tileContentKey('desktop-session-1', 'doc')]: { path: '/tmp/doc.md', content: '# Document' },
      },
      onRequestTileContent: vi.fn(),
    };
    const { container, rerender } = render(
      <SessionTerminalDesktop
        {...commonProps}
        terminalState={createDocumentSplitDesktop(0.5, 'automatic')}
      />,
      { wrapper: NotebookSurfaceTestWrapper },
    );
    const panes = container.querySelector('.session-terminal-panes') as HTMLElement;
    vi.spyOn(panes, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, right: 1000, bottom: 500, width: 1000, height: 500, x: 0, y: 0, toJSON: () => {},
    });

    fireEvent.pointerDown(screen.getByRole('separator'), { button: 0, clientX: 500, clientY: 250 });
    fireEvent.pointerUp(window, { clientX: 520, clientY: 250 });
    expect(container.querySelector('.desktop-layout-metadata [data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.520');

    rerender(
      <SessionTerminalDesktop
        {...commonProps}
        terminalState={createDocumentSplitDesktop(0.52, 'preferred')}
      />,
    );
    rerender(
      <SessionTerminalDesktop
        {...commonProps}
        terminalState={createDocumentSplitDesktop(0.52, 'automatic')}
      />,
    );

    expect(container.querySelector('.desktop-layout-metadata [data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.400');
  });

  it('keeps the session pane mounted when the split topology changes around it', () => {
    const { rerender } = render(
      <SessionTerminalDesktop
        desktopId="desktop-session-1"
        desktopSessions={[{ id: "session-1", label: "Session 1", agent: "codex", cwd: "/tmp/repo" }]}
        terminalState={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    rerender(
      <SessionTerminalDesktop
        desktopId="desktop-session-1"
        desktopSessions={[{ id: "session-1", label: "Session 1", agent: "codex", cwd: "/tmp/repo" }]}
        terminalState={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' },
            { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' },
            { id: 'pane-session-2', runtimeId: 'runtime-session-2', title: "Session", sessionId: 'session-1' },
          ],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.7,
            children: [
              {
                type: 'split',
                splitId: 'main-branch',
                direction: 'vertical',
                ratio: 0.5,
                children: [
                  { type: 'pane', paneId: SESSION_PANE_ID },
                  { type: 'pane', paneId: 'pane-session-2' },
                ],
              },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    rerender(
      <SessionTerminalDesktop
        desktopId="desktop-session-1"
        desktopSessions={[{ id: "session-1", label: "Session 1", agent: "codex", cwd: "/tmp/repo" }]}
        terminalState={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(terminalLifecycleCounts.get('agent:Session 1:codex:session-1')?.mounts).toBeGreaterThan(0);
  });

});
