import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createRef, startTransition, Suspense, useState, type ReactNode } from 'react';
import { SessionTerminalWorkspace, type SessionTerminalWorkspaceHandle } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import {
  tileContentKey,
  type TerminalLayoutNode,
  type TerminalWorkspaceState,
} from '../../types/workspace';
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
    sendToChief: vi.fn(),
    listFiles: vi.fn(),
    changeSignal: 0,
  }),
  effectiveNotebookRoot: '',
  sendFsWatch: vi.fn(),
  sendFsUnwatch: vi.fn(),
  connectionGeneration: 0,
};

function Wrapper({ children }: { children: ReactNode }) {
  return <NotebookSurfaceProvider value={notebookSurface}>{children}</NotebookSurfaceProvider>;
}

function crowdedWorkspace(): TerminalWorkspaceState {
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

function workspaceWithOpenedDocuments(documentIds: readonly string[]): TerminalWorkspaceState {
  let layoutTree: TerminalLayoutNode = { type: 'pane', paneId: 'agent-a' };
  for (const documentId of documentIds) {
    const dockBesideAgent = (node: TerminalLayoutNode): TerminalLayoutNode => {
      if (node.type === 'pane' && node.paneId === 'agent-a') {
        return {
          type: 'split',
          splitId: `split-${documentId}`,
          direction: 'vertical',
          ratio: 0.68,
          children: [
            node,
            {
              type: 'tile',
              tileId: documentId,
              tileKind: 'markdown',
              tileParams: `/tmp/${documentId}.md`,
            },
          ],
        };
      }
      if (node.type !== 'split') {
        return node;
      }
      return {
        ...node,
        children: [dockBesideAgent(node.children[0]), node.children[1]],
      };
    };
    layoutTree = dockBesideAgent(layoutTree);
  }
  return {
    agents: [
      { id: 'agent-a', runtimeId: 'runtime-a', sessionId: 'session-a', title: 'Alpha' },
    ],
    layoutTree,
  };
}

class ImmediateResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}

  observe(target: Element): void {
    this.callback([{
      target,
      contentRect: { width: observedWidth, height: 700 } as DOMRectReadOnly,
    } as ResizeObserverEntry], this as unknown as ResizeObserver);
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

describe('SessionTerminalWorkspace attention ring', () => {
  it('keeps imperative focus bound to the last committed workspace', async () => {
    const committedWorkspace = workspaceWithOpenedDocuments(['document']);
    const discardedWorkspace: TerminalWorkspaceState = {
      agents: [
        {
          id: 'document',
          runtimeId: 'runtime-document',
          sessionId: 'session-document',
          title: 'Document pane',
        },
      ],
      layoutTree: { type: 'pane', paneId: 'document' },
    };
    const workspaceRef = createRef<SessionTerminalWorkspaceHandle>();
    const onFocusPane = vi.fn();
    const discardedRenderReached = vi.fn();
    const neverCommits = new Promise<void>(() => {});
    let beginDiscardedRender = () => {};

    function SuspendDiscardedRender({ active }: { active: boolean }) {
      if (active) {
        discardedRenderReached();
        throw neverCommits;
      }
      return null;
    }

    function Host() {
      const [showDiscardedWorkspace, setShowDiscardedWorkspace] = useState(false);
      beginDiscardedRender = () => {
        startTransition(() => setShowDiscardedWorkspace(true));
      };
      return (
        <Suspense fallback={null}>
          <SessionTerminalWorkspace
            ref={workspaceRef}
            workspaceId="workspace-committed-focus"
            workspaceSessions={[
              { id: 'session-a', label: 'Alpha', agent: 'shell', cwd: '/tmp' },
              { id: 'session-document', label: 'Document pane', agent: 'shell', cwd: '/tmp' },
            ]}
            workspace={showDiscardedWorkspace ? discardedWorkspace : committedWorkspace}
            activePaneId={showDiscardedWorkspace ? 'document' : 'agent-a'}
            fontSize={13}
            enabled
            isActiveSession
            eventRouter={createPaneRuntimeEventRouterController()}
            onSplitPane={vi.fn()}
            onClosePane={vi.fn()}
            onFocusPane={onFocusPane}
            onNavigateOutOfSession={vi.fn()}
            onUndockTile={vi.fn()}
            onRequestTileContent={vi.fn()}
            tileContents={{
              [tileContentKey('workspace-committed-focus', 'document')]: {
                path: '/tmp/document.md',
                content: '# Document',
              },
            }}
          />
          <SuspendDiscardedRender active={showDiscardedWorkspace} />
        </Suspense>
      );
    }

    const { container } = render(<Host />, { wrapper: Wrapper });
    await waitFor(() => expect(workspaceRef.current).not.toBeNull());
    onFocusPane.mockClear();

    await act(async () => {
      beginDiscardedRender();
      await Promise.resolve();
    });
    expect(discardedRenderReached).toHaveBeenCalled();
    expect(container.querySelector('[data-pane-id="document"]')).toHaveAttribute('data-pane-kind', 'tile');

    act(() => workspaceRef.current?.focusLeaf('document'));

    expect(onFocusPane).not.toHaveBeenCalled();
  });

  it('suspends the oldest rightmost documents before newer work', async () => {
    observedWidth = 1816;
    const commonProps = {
      workspaceId: 'workspace-focus-order',
      workspaceSessions: [
        { id: 'session-a', label: 'Alpha', agent: 'shell' as const, cwd: '/tmp' },
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
      onRequestTileContent: vi.fn(),
    };
    const openedDocuments = ['oldest', 'older', 'newer', 'newest'];
    const tileContents = Object.fromEntries(openedDocuments.map((documentId) => [
      tileContentKey('workspace-focus-order', documentId),
      { path: `/tmp/${documentId}.md`, content: `# ${documentId}` },
    ]));
    const { container, rerender } = render(
      <SessionTerminalWorkspace
        {...commonProps}
        workspace={workspaceWithOpenedDocuments([])}
        tileContents={tileContents}
      />,
      { wrapper: Wrapper },
    );

    for (const [index, documentId] of openedDocuments.entries()) {
      rerender(
        <SessionTerminalWorkspace
          {...commonProps}
          workspace={workspaceWithOpenedDocuments(openedDocuments.slice(0, index + 1))}
          tileContents={tileContents}
        />,
      );
      await waitFor(() => {
        expect(container.querySelector('.session-terminal-workspace')).toHaveAttribute(
          'data-active-leaf-id',
          documentId,
        );
      });
      await waitFor(() => {
        expect(container.querySelectorAll('[data-pane-suspended="true"]')).toHaveLength(
          Math.max(0, index - 1),
        );
      });
      if (documentId !== 'newest') {
        fireEvent.mouseDown(container.querySelector('[data-pane-id="agent-a"]')!);
        await waitFor(() => {
          expect(container.querySelector('.session-terminal-workspace')).toHaveAttribute(
            'data-active-leaf-id',
            'agent-a',
          );
        });
      }
    }

    expect(await screen.findByRole('button', { name: 'Expand oldest.md' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Expand older.md' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Expand Alpha' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Expand newer.md' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Expand newest.md' })).toBeNull();
  });

  it('brings a newly opened document into the attention ring', async () => {
    observedWidth = 560;
    const initialWorkspace: TerminalWorkspaceState = {
      agents: [
        { id: 'agent-a', runtimeId: 'runtime-a', sessionId: 'session-a', title: 'Alpha' },
      ],
      layoutTree: { type: 'pane', paneId: 'agent-a' },
    };
    const commonProps = {
      workspaceId: 'workspace-new-document',
      workspaceSessions: [
        { id: 'session-a', label: 'Alpha', agent: 'shell' as const, cwd: '/tmp' },
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
      onRequestTileContent: vi.fn(),
    };
    const { container, rerender } = render(
      <SessionTerminalWorkspace workspace={initialWorkspace} {...commonProps} />,
      { wrapper: Wrapper },
    );

    rerender(
      <SessionTerminalWorkspace
        {...commonProps}
        workspace={{
          ...initialWorkspace,
          layoutTree: {
            type: 'split',
            splitId: 'opened-document',
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
        }}
        tileContents={{
          [tileContentKey('workspace-new-document', 'document')]: {
            path: '/tmp/review.md', content: '# Review me',
          },
        }}
      />,
    );

    expect(await screen.findByRole('button', { name: 'Expand Alpha' })).toBeInTheDocument();
    await waitFor(() => {
      expect(container.querySelector('.session-terminal-workspace')).toHaveAttribute(
        'data-active-leaf-id',
        'document',
      );
    });
    expect(container.querySelector('[data-pane-id="document"]')).not.toHaveAttribute('data-pane-suspended');
  });

  it('expands a clicked sliver and folds the least-recently-focused leaf, not the previous one', async () => {
    const onFocusPane = vi.fn();
    const { container } = render(
      <SessionTerminalWorkspace
        workspaceId="workspace-attention"
        workspaceSessions={[
          { id: 'session-a', label: 'Alpha', agent: 'shell', cwd: '/tmp' },
          { id: 'session-b', label: 'Beta', agent: 'shell', cwd: '/tmp' },
        ]}
        workspace={crowdedWorkspace()}
        activePaneId="agent-a"
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={createPaneRuntimeEventRouterController()}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={onFocusPane}
        onNavigateOutOfSession={vi.fn()}
        onUndockTile={vi.fn()}
        tileContents={{
          [tileContentKey('workspace-attention', 'document')]: {
            path: '/tmp/review.md',
            content: '# Review me',
          },
        }}
        onRequestTileContent={vi.fn()}
      />,
      { wrapper: Wrapper },
    );

    const beta = await screen.findByRole('button', { name: 'Expand Beta' });
    expect(beta.closest('[data-pane-suspended="true"]')).toHaveClass('workspace-pane--suspended-column');
    expect(container.querySelector('[data-pane-id="document"]')).not.toHaveAttribute('data-pane-suspended');

    fireEvent.click(beta);
    expect(onFocusPane).toHaveBeenCalledWith('agent-b');
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Expand review.md' })).toBeInTheDocument();
    });
    expect(screen.queryByRole('button', { name: 'Expand Beta' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Expand Alpha' })).toBeNull();
  });

  it('resizes the visible neighbors when a sliver-side divider is dragged', async () => {
    const onFocusPane = vi.fn();
    const boundingRect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      x: 0, y: 0, left: 0, top: 0, right: 1100, bottom: 700, width: 1100, height: 700,
      toJSON: () => ({}),
    } as DOMRect);
    try {
      const { container } = render(
        <SessionTerminalWorkspace
          workspaceId="workspace-drag"
          workspaceSessions={[
            { id: 'session-a', label: 'Alpha', agent: 'shell', cwd: '/tmp' },
            { id: 'session-b', label: 'Beta', agent: 'shell', cwd: '/tmp' },
          ]}
          workspace={crowdedWorkspace()}
          activePaneId="agent-a"
          fontSize={13}
          enabled
          isActiveSession
          eventRouter={createPaneRuntimeEventRouterController()}
          onSplitPane={vi.fn()}
          onClosePane={vi.fn()}
          onFocusPane={onFocusPane}
          onNavigateOutOfSession={vi.fn()}
          onUndockTile={vi.fn()}
          tileContents={{
            [tileContentKey('workspace-drag', 'document')]: {
              path: '/tmp/review.md',
              content: '# Review me',
            },
          }}
          onRequestTileContent={vi.fn()}
        />,
        { wrapper: Wrapper },
      );

      // Fold the middle document so the layout is [Alpha | sliver | Beta].
      const beta = await screen.findByRole('button', { name: 'Expand Beta' });
      fireEvent.click(beta);
      await waitFor(() => {
        expect(screen.getByRole('button', { name: 'Expand review.md' })).toBeInTheDocument();
      });

      // The boundary beside the sliver re-aims at the outer split, so both
      // of the sliver's edges resize Alpha against Beta.
      const grab = container.querySelector('.workspace-split-divider[data-split-grab]') as HTMLElement;
      expect(grab).not.toBeNull();
      expect(grab.dataset.splitId).toBe('outer');
      const ratioBefore = Number.parseFloat(
        container.querySelector('.workspace-layout-metadata [data-split-id="outer"]')!
          .getAttribute('data-split-ratio')!,
      );

      fireEvent.pointerDown(grab, { button: 0, pointerId: 1, clientX: 500, clientY: 350 });
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 300, clientY: 350 });
      await new Promise((resolve) => requestAnimationFrame(resolve));
      fireEvent.pointerUp(window, { pointerId: 1, clientX: 300, clientY: 350 });

      await waitFor(() => {
        const ratioAfter = Number.parseFloat(
          container.querySelector('.workspace-layout-metadata [data-split-id="outer"]')!
            .getAttribute('data-split-ratio')!,
        );
        expect(ratioAfter).toBeLessThan(ratioBefore - 0.02);
        // Alpha bottoms out at its 480px minimum plus the 34px sliver.
        expect(ratioAfter).toBeCloseTo(514 / 1100, 2);
      });
      // The sliver itself never expands from a drag.
      expect(screen.getByRole('button', { name: 'Expand review.md' })).toBeInTheDocument();
    } finally {
      boundingRect.mockRestore();
    }
  });

  it('opens a tabbed review deck and unwinds inspector then Focus with Escape', async () => {
    observedWidth = 2000;
    const workspace = crowdedWorkspace();
    if (workspace.layoutTree?.type !== 'split' || workspace.layoutTree.children[1].type !== 'pane') {
      throw new Error('expected fixture split');
    }
    workspace.layoutTree = {
      ...workspace.layoutTree,
      children: [
        workspace.layoutTree.children[0],
        {
          type: 'split',
          splitId: 'second-document-split',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            workspace.layoutTree.children[1],
            {
              type: 'tile',
              tileId: 'second-document',
              tileKind: 'markdown',
              tileParams: '/tmp/second.md',
            },
          ],
        },
      ],
    };
    const { container } = render(
      <SessionTerminalWorkspace
        workspaceId="workspace-focus"
        workspaceSessions={[
          { id: 'session-a', label: 'Alpha', agent: 'shell', cwd: '/tmp' },
          { id: 'session-b', label: 'Beta', agent: 'shell', cwd: '/tmp' },
        ]}
        workspace={workspace}
        activePaneId="agent-a"
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={createPaneRuntimeEventRouterController()}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
        onUndockTile={vi.fn()}
        tileContents={{
          [tileContentKey('workspace-focus', 'document')]: {
            path: '/tmp/review.md', content: '# Review me',
          },
          [tileContentKey('workspace-focus', 'second-document')]: {
            path: '/tmp/second.md', content: '# Second review',
          },
        }}
        onRequestTileContent={vi.fn()}
      />,
      { wrapper: Wrapper },
    );

    const firstDocument = container.querySelector<HTMLElement>('[data-pane-id="document"]')!;
    fireEvent.click(within(firstDocument).getByRole('button', { name: 'Focus document' }));
    expect(container.querySelector('.session-terminal-workspace')).toHaveClass('focus-mode');
    expect(screen.getByRole('tab', { name: 'review.md' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: 'second.md' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: 'second.md' }));
    expect(screen.getByRole('tab', { name: 'second.md' })).toHaveAttribute('aria-selected', 'true');
    const focusedDocument = container.querySelector<HTMLElement>('[data-pane-id="second-document"]')!;
    fireEvent.click(within(focusedDocument).getByRole('button', { name: 'Notes 0' }));
    expect(screen.getByRole('dialog', { name: 'Review notes' })).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Review notes' })).toBeNull();
    expect(container.querySelector('.session-terminal-workspace')).toHaveClass('focus-mode');
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(container.querySelector('.session-terminal-workspace')).not.toHaveClass('focus-mode');
  });
});
