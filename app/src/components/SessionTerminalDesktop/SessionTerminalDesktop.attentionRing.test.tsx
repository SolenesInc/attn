import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { useState, type ComponentProps, type ReactNode } from 'react';
import { SessionTerminalDesktop } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import {
  tileContentKey,
  type TerminalDesktopState,
} from '../../types/desktop';
import { NotebookSurfaceProvider, type NotebookSurfaceContextValue } from '../../contexts/NotebookSurfaceContext';

vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

const notebookSurface: NotebookSurfaceContextValue = {
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

function Wrapper({ children }: { children: ReactNode }) {
  return <NotebookSurfaceProvider value={notebookSurface}>{children}</NotebookSurfaceProvider>;
}

function crowdedDesktop(): TerminalDesktopState {
  return {
    agents: [
      { id: 'agent-a', runtimeId: 'runtime-a', sessionId: 'session-a', title: 'Alpha' },
      { id: 'agent-b', runtimeId: 'runtime-b', sessionId: 'session-b', title: 'Beta' },
    ],
    layoutTree: {
      type: 'split',
      splitId: 'outer',
      direction: 'vertical',
      ratio: 0.5,
      children: [
        {
          type: 'split',
          splitId: 'document-split',
          direction: 'vertical',
          ratio: 0.68,
          children: [
            { type: 'pane', paneId: 'agent-a' },
            {
              type: 'tile',
              tileId: 'document',
              tileKind: 'markdown',
              tileParams: '/tmp/review.md',
            },
          ],
        },
        { type: 'pane', paneId: 'agent-b' },
      ],
    },
  };
}

class ImmediateResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}

  observe(target: Element): void {
    this.callback([{
      target,
      contentRect: { width: observedWidth, height: 700 } as DOMRectReadOnly,
    } as ResizeObserverEntry], this as ResizeObserver);
  }

  disconnect(): void {}
  unobserve(): void {}
}

let observedWidth = 1100;

beforeEach(() => {
  observedWidth = 1100;
  vi.stubGlobal('ResizeObserver', ImmediateResizeObserver);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function DaemonEcho(props: ComponentProps<typeof SessionTerminalDesktop>) {
  const [active, setActive] = useState(props.activePaneId);
  const [seen, setSeen] = useState(props.activePaneId);
  if (props.activePaneId !== seen) {
    setSeen(props.activePaneId);
    setActive(props.activePaneId);
  }
  const { onFocusPane } = props;
  return (
    <SessionTerminalDesktop
      {...props}
      activePaneId={active}
      onFocusPane={(paneId) => {
        onFocusPane(paneId);
        setActive(paneId);
      }}
    />
  );
}

describe('SessionTerminalDesktop attention ring', () => {

  it('measures the surface once leaves arrive on a desktop that mounted empty', async () => {
    const props = {
      desktopId: 'desktop-attention',
      desktopSessions: [
        { id: 'session-a', label: 'Alpha', agent: 'shell' as const, cwd: '/tmp' },
        { id: 'session-b', label: 'Beta', agent: 'shell' as const, cwd: '/tmp' },
      ],
      activePaneId: 'agent-a',
      fontSize: 13,
      enabled: true,
      isActiveSession: true,
      eventRouter: createPaneRuntimeEventRouterController(),
      onSplitPane: vi.fn(),
      onClosePane: vi.fn(),
      onFocusPane: vi.fn(),
      onNavigateOutOfSession: vi.fn(),
      onUndockTile: vi.fn(),
      tileContents: {
        [tileContentKey('desktop-attention', 'document')]: { path: '/tmp/review.md', content: '# Review me' },
      },
      onRequestTileContent: vi.fn(),
    };
    const { rerender } = render(
      <DaemonEcho {...props} activePaneId="" terminalState={{ agents: [], layoutTree: null }} />,
      { wrapper: Wrapper },
    );

    rerender(<DaemonEcho {...props} terminalState={crowdedDesktop()} />);

    expect(await screen.findByRole('button', { name: 'Expand Beta' })).toBeInTheDocument();
  });

});
