import type { ReactNode } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import { tileContentKey, type TerminalWorkspaceState } from '../../types/workspace';
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

const browserTileProps = vi.hoisted(() => ({
  current: null as null | { visible: boolean },
}));

vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

vi.mock('./BrowserTileBody', () => ({
  BrowserTileBody: (props: { visible: boolean }) => {
    browserTileProps.current = props;
    return <div data-testid="mock-browser-tile" />;
  },
}));

function tileOnlyWorkspace(): TerminalWorkspaceState {
  return {
    agents: [],
    layoutTree: {
      type: 'tile',
      tileId: 'tile-readme',
      tileKind: 'markdown',
      tileParams: '/tmp/project/README.md',
    },
  };
}

function renderTileOnly() {
  return render(
    <SessionTerminalWorkspace
      workspaceId="workspace-tiles"
      workspace={tileOnlyWorkspace()}
      activePaneId=""
      fontSize={13}
      enabled
      isActiveSession
      eventRouter={createPaneRuntimeEventRouterController()}
      onSplitPane={vi.fn()}
      onClosePane={vi.fn()}
      onFocusPane={vi.fn()}
      onNavigateOutOfSession={vi.fn()}
      tileContents={{
        [tileContentKey('workspace-tiles', 'tile-readme')]: {
          path: '/tmp/project/README.md',
          content: '# Project notes',
        },
      }}
      onRequestTileContent={vi.fn()}
    />,
    { wrapper: NotebookSurfaceTestWrapper },
  );
}

describe('SessionTerminalWorkspace tile-only (sessionless) rendering', () => {
  it('renders the docked tile when the workspace has no agent panes', () => {
    const { container } = renderTileOnly();

    const tileSurface = container.querySelector('[data-pane-kind="tile"]');
    expect(tileSurface).not.toBeNull();
    expect(tileSurface?.getAttribute('data-pane-id')).toBe('tile-readme');
    expect(tileSurface?.getAttribute('data-tile-kind')).toBe('markdown');

    expect(screen.getByRole('heading', { name: 'Project notes' })).toBeInTheDocument();
  });

  it('does not fall back to the empty workspace placeholder', () => {
    const { container } = renderTileOnly();

    const workspaceRoot = container.querySelector('[data-session-terminal-workspace="workspace-tiles"]');
    expect(workspaceRoot).not.toBeNull();
    expect(workspaceRoot?.querySelector('.session-terminal-panes')).not.toBeNull();
  });

  it('focuses the tile body so the tile-only workspace is keyboard-scrollable', () => {
    const { container } = renderTileOnly();

    const body = container.querySelector('.workspace-dock-tile-body');
    expect(body).not.toBeNull();
    expect(body).toHaveAttribute('tabindex', '-1');
    expect(document.activeElement).toBe(body);
  });

  it('hides a native browser tile while workspace interaction is disabled', () => {
    const browserWorkspace: TerminalWorkspaceState = {
      agents: [],
      layoutTree: {
        type: 'tile',
        tileId: 'tile-browser',
        tileKind: 'browser',
        tileParams: 'https://backstage.spotify.net',
      },
    };
    const commonProps = {
      workspaceId: 'workspace-browser',
      workspace: browserWorkspace,
      activePaneId: '',
      fontSize: 13,
      isActiveSession: true,
      eventRouter: createPaneRuntimeEventRouterController(),
      onSplitPane: vi.fn(),
      onClosePane: vi.fn(),
      onFocusPane: vi.fn(),
      onNavigateOutOfSession: vi.fn(),
    };
    const { rerender } = render(
      <SessionTerminalWorkspace {...commonProps} enabled />,
      { wrapper: NotebookSurfaceTestWrapper },
    );

    expect(browserTileProps.current?.visible).toBe(true);

    rerender(<SessionTerminalWorkspace {...commonProps} enabled={false} />);

    expect(browserTileProps.current?.visible).toBe(false);
  });
});
