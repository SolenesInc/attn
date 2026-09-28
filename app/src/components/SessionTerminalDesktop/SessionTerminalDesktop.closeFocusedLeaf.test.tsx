import { useState, type ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render } from '@testing-library/react';
import { SessionTerminalDesktop } from './index';
import { annotationSurfaceOwnsFocus } from './annotationFocus';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import { tileContentKey, type TerminalDesktopState } from '../../types/desktop';
import { NotebookSurfaceProvider, type NotebookSurfaceContextValue } from '../../contexts/NotebookSurfaceContext';
import type { DesktopSelectionStyle } from '../../utils/desktopSelectionStyle';

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
  changeSignalFor: () => 0,
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

function paneAndTileDesktop(): TerminalDesktopState {
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

function paneOnlyDesktop(): TerminalDesktopState {
  return {
    agents: [{ id: 'pane-term', runtimeId: 'rt-1', sessionId: 'sess-1', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-term' },
  };
}

function renderSplit(overrides: {
  onClosePane?: () => void;
  onUndockTile?: (tileId: string) => void;
  onFocusPane?: (paneId: string) => void;
  desktopSelectionStyle?: DesktopSelectionStyle;
  activePaneId?: string;
  daemonConfirms?: boolean;
} = {}) {
  const onClosePane = overrides.onClosePane ?? vi.fn();
  const onUndockTile = overrides.onUndockTile ?? vi.fn();
  const onFocusPane = overrides.onFocusPane ?? vi.fn();
  const eventRouter = createPaneRuntimeEventRouterController();
  function ZoomHost({ terminalState }: { terminalState: TerminalDesktopState }) {
    const [zoomActive, setZoomActive] = useState(false);
    const [activePaneId, setActivePaneId] = useState(overrides.activePaneId ?? 'pane-term');
    const focusPane = (paneId: string) => {
      onFocusPane(paneId);
      if (overrides.daemonConfirms !== false) setActivePaneId(paneId);
    };
    return (
      <SessionTerminalDesktop
        desktopId="desktop-split"
        desktopSessions={[{ id: 'sess-1', label: 'shell', agent: 'shell', cwd: '/tmp/project' }]}
        terminalState={terminalState}
        desktopSelectionStyle={overrides.desktopSelectionStyle}
        activePaneId={activePaneId}
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={eventRouter}
        onSplitPane={vi.fn()}
        onClosePane={onClosePane}
        onFocusPane={focusPane}
        onNavigateOutOfSession={vi.fn()}
        onUndockTile={onUndockTile}
        zoomActive={zoomActive}
        onSetZoomActive={setZoomActive}
        tileContents={{
          [tileContentKey('desktop-split', 'tile-notes')]: {
            path: '/tmp/project/NOTES.md',
            content: '# Project notes',
          },
        }}
        onRequestTileContent={vi.fn()}
      />
    );
  }
  const element = (terminalState: TerminalDesktopState) => <ZoomHost terminalState={terminalState} />;
  const utils = render(element(paneAndTileDesktop()), { wrapper: NotebookSurfaceTestWrapper });
  const setDesktop = (terminalState: TerminalDesktopState) => utils.rerender(element(terminalState));
  return { ...utils, setDesktop, onClosePane, onUndockTile, onFocusPane };
}

function tileEl(container: HTMLElement): HTMLElement {
  return container.querySelector('[data-pane-kind="tile"]') as HTMLElement;
}

function paneEl(container: HTMLElement): HTMLElement {
  return container.querySelector('[data-pane-kind="agent"]') as HTMLElement;
}

describe('SessionTerminalDesktop selection style', () => {
  it('marks the terminalState with the selected treatment', () => {
    const { container } = renderSplit({ desktopSelectionStyle: 'spotlight' });

    expect(container.querySelector('.session-terminal-desktop')).toHaveClass('desktop-selection--spotlight');
  });
});

describe('SessionTerminalDesktop leaf focus', () => {
  it('keeps annotation focus only for its owning terminalState', () => {
    const popup = document.createElement('dialog');
    popup.className = 'anno-popup';
    popup.dataset.desktopId = 'desktop-split';
    const textarea = document.createElement('textarea');
    popup.appendChild(textarea);
    document.body.appendChild(popup);
    textarea.focus();

    expect(annotationSurfaceOwnsFocus('desktop-split')).toBe(true);
    expect(annotationSurfaceOwnsFocus('desktop-next')).toBe(false);

    popup.remove();
  });

  it('sends a clicked tile to the daemon as the active leaf and gives its body DOM focus', () => {
    const { container, onFocusPane } = renderSplit();

    const tile = tileEl(container);
    expect(tile.getAttribute('data-pane-id')).toBe('tile-notes');
    expect(tile.className).not.toContain('active');

    fireEvent.mouseDown(tile);

    expect(onFocusPane).toHaveBeenCalledWith('tile-notes');
    expect(tile.className).toContain('active');
    expect(paneEl(container).className).not.toContain('active');
    expect(container.querySelector('.session-terminal-desktop')
      ?.getAttribute('data-active-leaf-id')).toBe('tile-notes');
    const tileBody = tile.querySelector('.desktop-dock-tile-body') as HTMLElement;
    expect(document.activeElement).toBe(tileBody);
  });

  it('keeps the daemon\'s active pane until the daemon shows the clicked tile', () => {
    const { container, onFocusPane } = renderSplit({ daemonConfirms: false });
    const surface = () => container.querySelector('.session-terminal-desktop');

    fireEvent.mouseDown(tileEl(container));

    expect(onFocusPane).toHaveBeenCalledWith('tile-notes');
    expect(surface()?.getAttribute('data-active-leaf-id')).toBe('pane-term');
    expect(tileEl(container).className).not.toContain('active');
  });

  it('focuses the tile the daemon names as the active leaf', () => {
    const { container, onFocusPane } = renderSplit({ activePaneId: 'tile-notes' });

    expect(tileEl(container).className).toContain('active');
    expect(document.activeElement).toBe(tileEl(container).querySelector('.desktop-dock-tile-body'));
    expect(onFocusPane).not.toHaveBeenCalled();
  });

  it('zooms and maximizes the focused tile', () => {
    const { container } = renderSplit();
    const surface = container.querySelector('.session-terminal-desktop') as HTMLElement;

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
    const { container, setDesktop } = renderSplit();
    const surface = () => container.querySelector('.session-terminal-desktop') as HTMLElement;

    fireEvent.mouseDown(tileEl(container));
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(surface().getAttribute('data-maximized-pane-id')).toBe('tile-notes');

    setDesktop(paneOnlyDesktop());
    setDesktop(paneAndTileDesktop());

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

describe('SessionTerminalDesktop Cmd+W closes the focused leaf', () => {
  it('undocks the focused tile instead of closing the active terminal pane', () => {
    const { container, onClosePane, onUndockTile } = renderSplit();

    const tile = tileEl(container);
    fireEvent.mouseDown(tile);

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'w', metaKey: true });

    expect(onUndockTile).toHaveBeenCalledTimes(1);
    expect(onUndockTile).toHaveBeenCalledWith('tile-notes');
    expect(onClosePane).not.toHaveBeenCalled();
  });

  it('leaves a focused tile alone when a terminal remounts and announces readiness', () => {
    const { container } = renderSplit();
    terminalFocusCalls.length = 0;

    fireEvent.mouseDown(tileEl(container));
    const tileBody = tileEl(container).querySelector('.desktop-dock-tile-body') as HTMLElement;
    expect(document.activeElement).toBe(tileBody);

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(container.querySelector('[data-pane-kind="agent"]')).toBeNull();
    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Enter', metaKey: true, shiftKey: true });
    expect(container.querySelector('[data-pane-kind="agent"]')).not.toBeNull();

    expect(terminalFocusCalls).toEqual([]);
    expect(tileEl(container).className).toContain('active');
    expect(document.activeElement).toBe(tileEl(container).querySelector('.desktop-dock-tile-body'));
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
