import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, render } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalWorkspaceState } from '../../types/workspace';
import { NotebookSurfaceProvider, type NotebookSurfaceContextValue } from '../../contexts/NotebookSurfaceContext';

const testSurfaceValue: NotebookSurfaceContextValue = {
  makeDaemon: () => ({
    listDir: vi.fn(),
    readFile: vi.fn(),
    writeFile: vi.fn(),
    existsFile: vi.fn(),
    readAsset: vi.fn(),
    backlinksNotebook: vi.fn(),
    sendToChief: vi.fn(),
    listFiles: vi.fn(),
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

const deferredReadyAnnouncements = vi.hoisted(() => [] as Array<() => void>);
vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function DeferredReadyTerminal(
      props: { onReady?: (handle: unknown) => void; runtimeLogMeta?: { paneId?: string } },
      ref,
    ) {
      const nodeRef = React.useRef<HTMLDivElement | null>(null);
      const paneId = props.runtimeLogMeta?.paneId ?? 'pane';
      const handle = React.useMemo(() => ({
        focus: () => {
          nodeRef.current?.focus();
          return document.activeElement === nodeRef.current;
        },
        getSize: () => null,
        fit: () => {},
        setSurfaceReleased: () => {},
      }), []);
      React.useImperativeHandle(ref, () => handle, [handle]);
      const onReadyRef = React.useRef(props.onReady);
      onReadyRef.current = props.onReady;
      React.useEffect(() => {
        deferredReadyAnnouncements.push(() => onReadyRef.current?.(handle));
      }, [handle]);
      return <div ref={nodeRef} tabIndex={-1} data-testid={`mock-terminal-${paneId}`} />;
    }),
  };
});

function paneWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-term', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-term' },
  };
}

function renderWorkspace() {
  return render(
    <>
      <button type="button" data-testid="sidebar-row">Open shell</button>
      <SessionTerminalWorkspace
        workspaceId="workspace-ready"
        workspaceSessions={[{ id: 'sess-1', label: 'shell', agent: 'shell', cwd: '/tmp/project' }]}
        workspace={paneWorkspace()}
        activePaneId="pane-term"
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={createPaneRuntimeEventRouterController()}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
        onUndockTile={vi.fn()}
        zoomActive={false}
        onSetZoomActive={vi.fn()}
        tileContents={{}}
        onRequestTileContent={vi.fn()}
      />
    </>,
    { wrapper: NotebookSurfaceTestWrapper },
  );
}

function announceReady() {
  act(() => {
    for (const announce of deferredReadyAnnouncements.splice(0)) announce();
  });
}

afterEach(() => {
  deferredReadyAnnouncements.length = 0;
});

describe('SessionTerminalWorkspace pane readiness focus', () => {
  it('focuses the terminal when it becomes ready and nothing else holds focus', () => {
    const { getByTestId } = renderWorkspace();
    (document.activeElement as HTMLElement | null)?.blur();
    expect(document.activeElement).toBe(document.body);

    announceReady();

    expect(document.activeElement).toBe(getByTestId('mock-terminal-pane-term'));
  });

  it('leaves focus on a control the user moved to before the terminal became ready', () => {
    const { getByTestId } = renderWorkspace();
    const row = getByTestId('sidebar-row');
    row.focus();
    expect(document.activeElement).toBe(row);

    announceReady();

    expect(document.activeElement).toBe(row);
  });
});
