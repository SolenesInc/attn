import { useState, type ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import { tileContentKey, type TerminalWorkspaceState } from '../../types/workspace';
import { NotebookSurfaceProvider, type NotebookSurfaceContextValue } from '../../contexts/NotebookSurfaceContext';
import type { WorkspaceSelectionStyle } from '../../utils/workspaceSelectionStyle';

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
    changeSignal: 0,
  }),
  effectiveNotebookRoot: '',
  sendFsWatch: vi.fn(),
  sendFsUnwatch: vi.fn(),
  connectionGeneration: 0,
};
function NotebookSurfaceTestWrapper({ children }: { children: ReactNode }) {
  return <NotebookSurfaceProvider value={testSurfaceValue}>{children}</NotebookSurfaceProvider>;
}

// The Ghostty stub still announces readiness and takes real DOM focus: when a mounting terminal grabs focus is what this spec is about.
const terminalFocusCalls = vi.hoisted(() => [] as string[]);
vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal(
      props: { onReady?: (handle: unknown) => void; runtimeLogMeta?: { paneId?: string } },
      ref,
    ) {
      const nodeRef = React.useRef<HTMLDivElement | null>(null);
      const paneId = props.runtimeLogMeta?.paneId ?? 'pane';
      const handle = React.useMemo(() => ({
        focus: () => {
          terminalFocusCalls.push(paneId);
          nodeRef.current?.focus();
          return true;
        },
        getSize: () => null,
        fit: () => {},
        setSurfaceReleased: () => {},
      }), [paneId]);
      React.useImperativeHandle(ref, () => handle, [handle]);
      // Once per mount: onReady's identity changes every parent render, so firing on it would announce forever.
      const onReadyRef = React.useRef(props.onReady);
      onReadyRef.current = props.onReady;
      React.useEffect(() => {
        onReadyRef.current?.(handle);
      }, [handle]);
      return <div ref={nodeRef} tabIndex={-1} data-testid={`mock-terminal-${paneId}`} />;
    }),
  };
});

function paneAndTileWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-term', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: {
      type: 'split',
      splitId: 'split-1',
      direction: 'vertical',
      ratio: 0.6,
      children: [
        { type: 'pane', paneId: 'pane-term' },
        { type: 'tile', tileId: 'tile-notes', tileKind: 'markdown', tileParams: '/tmp/project/NOTES.md' },
      ],
    },
  };
}

function paneOnlyWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-term', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-term' },
  };
}

function renderSplit(overrides: {
  onClosePane?: () => void;
  onUndockTile?: (tileId: string) => void;
  onFocusPane?: (paneId: string) => void;
  workspaceSelectionStyle?: WorkspaceSelectionStyle;
} = {}) {
  const onClosePane = overrides.onClosePane ?? vi.fn();
  const onUndockTile = overrides.onUndockTile ?? vi.fn();
  const onFocusPane = overrides.onFocusPane ?? vi.fn();
  const eventRouter = createPaneRuntimeEventRouterController();
  function ZoomHost({ workspace }: { workspace: TerminalWorkspaceState }) {
    const [zoomActive, setZoomActive] = useState(false);
    return (
      <SessionTerminalWorkspace
        workspaceId="workspace-split"
        workspaceSessions={[{ id: 'sess-1', label: 'shell', agent: 'shell', cwd: '/tmp/project' }]}
        workspace={workspace}
        workspaceSelectionStyle={overrides.workspaceSelectionStyle}
        activePaneId="pane-term"
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={eventRouter}
        onSplitPane={vi.fn()}
        onClosePane={onClosePane}
        onFocusPane={onFocusPane}
        onNavigateOutOfSession={vi.fn()}
        onUndockTile={onUndockTile}
        zoomActive={zoomActive}
        onSetZoomActive={setZoomActive}
        tileContents={{
          [tileContentKey('workspace-split', 'tile-notes')]: {
            path: '/tmp/project/NOTES.md',
            content: '# Project notes',
          },
        }}
        onRequestTileContent={vi.fn()}
      />
    );
  }
  const element = (workspace: TerminalWorkspaceState) => <ZoomHost workspace={workspace} />;
  const utils = render(element(paneAndTileWorkspace()), { wrapper: NotebookSurfaceTestWrapper });
  const setWorkspace = (workspace: TerminalWorkspaceState) => utils.rerender(element(workspace));
  return { ...utils, setWorkspace, onClosePane, onUndockTile, onFocusPane };
}

function tileEl(container: HTMLElement): HTMLElement {
  return container.querySelector('[data-pane-kind="tile"]') as HTMLElement;
}

function paneEl(container: HTMLElement): HTMLElement {
  return container.querySelector('[data-pane-kind="agent"]') as HTMLElement;
}

describe('SessionTerminalWorkspace selection style', () => {
  it('marks the workspace with the selected treatment', () => {
    const { container } = renderSplit({ workspaceSelectionStyle: 'spotlight' });

    expect(container.querySelector('.session-terminal-workspace')).toHaveClass('workspace-selection--spotlight');
  });
});

describe('SessionTerminalWorkspace leaf focus', () => {
  it('makes a clicked tile the active leaf and gives its body DOM focus', () => {
    const { container } = renderSplit();

    const tile = tileEl(container);
    expect(tile.getAttribute('data-pane-id')).toBe('tile-notes');
    expect(tile.className).not.toContain('active');

    fireEvent.mouseDown(tile);

    expect(tile.className).toContain('active');
    expect(paneEl(container).className).not.toContain('active');
    expect(container.querySelector('.session-terminal-workspace')
      ?.getAttribute('data-active-leaf-id')).toBe('tile-notes');
    const tileBody = tile.querySelector('.workspace-dock-tile-body') as HTMLElement;
    expect(document.activeElement).toBe(tileBody);
  });

  it('zooms and maximizes the focused tile', () => {
    const { container } = renderSplit();
    const surface = container.querySelector('.session-terminal-workspace') as HTMLElement;

    fireEvent.mouseDown(tileEl(container));
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'z', metaKey: true, shiftKey: true });
    expect(surface.getAttribute('data-zoomed-pane-id')).toBe('tile-notes');

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(surface.getAttribute('data-maximized-pane-id')).toBe('tile-notes');
    expect(surface.getAttribute('data-zoomed-pane-id')).toBe('');
    expect(container.querySelector('[data-pane-kind="agent"]')).toBeNull();
    expect(tileEl(container).getAttribute('data-pane-id')).toBe('tile-notes');
  });

  // A markdown tile's id is derived from its file path, so a maximized leaf that leaves the layout must be forgotten.
  it('forgets a maximized tile once it leaves the layout', () => {
    const { container, setWorkspace } = renderSplit();
    const surface = () => container.querySelector('.session-terminal-workspace') as HTMLElement;

    fireEvent.mouseDown(tileEl(container));
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(surface().getAttribute('data-maximized-pane-id')).toBe('tile-notes');

    setWorkspace(paneOnlyWorkspace());
    setWorkspace(paneAndTileWorkspace());

    expect(surface().getAttribute('data-maximized-pane-id')).toBe('');
    expect(container.querySelector('[data-pane-kind="agent"]')).not.toBeNull();
  });

  it('returns the active leaf to the terminal pane when it is clicked', () => {
    const { container, onFocusPane } = renderSplit();

    fireEvent.mouseDown(tileEl(container));
    expect(tileEl(container).className).toContain('active');

    fireEvent.mouseDown(paneEl(container));

    expect(onFocusPane).toHaveBeenCalledWith('pane-term');
    expect(tileEl(container).className).not.toContain('active');
    expect(paneEl(container).className).toContain('active');
  });
});

describe('SessionTerminalWorkspace Cmd+W closes the focused leaf', () => {
  it('undocks the focused tile instead of closing the active terminal pane', () => {
    const { container, onClosePane, onUndockTile } = renderSplit();

    const tile = tileEl(container);
    fireEvent.mouseDown(tile);

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'w', metaKey: true });

    expect(onUndockTile).toHaveBeenCalledTimes(1);
    expect(onUndockTile).toHaveBeenCalledWith('tile-notes');
    expect(onClosePane).not.toHaveBeenCalled();
  });

  it('closes the terminal pane after Focus utility terminal takes focus back from a tile', () => {
    const { container, onClosePane, onUndockTile } = renderSplit();

    fireEvent.mouseDown(tileEl(container));
    expect(tileEl(container).className).toContain('active');

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: '`', metaKey: true });

    expect(tileEl(container).className).not.toContain('active');
    expect(paneEl(container).className).toContain('active');

    fireEvent.keyDown(paneEl(container), { key: 'w', metaKey: true });

    expect(onClosePane).toHaveBeenCalledTimes(1);
    expect(onClosePane).toHaveBeenCalledWith('pane-term');
    expect(onUndockTile).not.toHaveBeenCalled();
  });

  it('leaves a focused tile alone when a terminal remounts and announces readiness', () => {
    const { container } = renderSplit();
    terminalFocusCalls.length = 0;

    fireEvent.mouseDown(tileEl(container));
    const tileBody = tileEl(container).querySelector('.workspace-dock-tile-body') as HTMLElement;
    expect(document.activeElement).toBe(tileBody);

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(container.querySelector('[data-pane-kind="agent"]')).toBeNull();
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(container.querySelector('[data-pane-kind="agent"]')).not.toBeNull();

    expect(terminalFocusCalls).toEqual([]);
    expect(tileEl(container).className).toContain('active');
    expect(document.activeElement).toBe(tileEl(container).querySelector('.workspace-dock-tile-body'));
  });

  it('closes the active terminal pane when the pane is the focused leaf', () => {
    const { container, onClosePane, onUndockTile } = renderSplit();

    const pane = paneEl(container);
    expect(pane.getAttribute('data-pane-id')).toBe('pane-term');
    fireEvent.mouseDown(tileEl(container));
    fireEvent.mouseDown(pane);

    fireEvent.keyDown(pane, { key: 'w', metaKey: true });

    expect(onClosePane).toHaveBeenCalledTimes(1);
    expect(onClosePane).toHaveBeenCalledWith('pane-term');
    expect(onUndockTile).not.toHaveBeenCalled();
  });
});
