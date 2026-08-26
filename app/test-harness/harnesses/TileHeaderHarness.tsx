import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { DaemonApiProvider, type DaemonApi } from '../../src/contexts/DaemonApiContext';
import {
  NotebookSurfaceProvider,
  type NotebookSurfaceContextValue,
} from '../../src/contexts/NotebookSurfaceContext';
import { WorkspaceDockTile } from '../../src/components/SessionTerminalWorkspace/WorkspaceDockTile';
import { setMarkdownAnnotationsTransport } from '../../src/components/MarkdownReader/annotations/transport';
import type { MarkdownAnnotationsTransport } from '../../src/components/MarkdownReader/annotations/transport';
import type { HarnessProps } from '../types';

const sessions = [
  { sessionId: 'session-alpha', label: 'Codex alpha', state: 'working' },
  { sessionId: 'session-beta', label: 'Claude beta', state: 'pending_approval' },
];

const transport: MarkdownAnnotationsTransport = {
  getMarkdownAnnotations: async () => ({
    annotations: [{
      id: 'overall-1',
      type: 'global',
      text: 'Check the conclusion.',
      created_at: 1,
    }],
    generation: 1,
  }),
  saveMarkdownAnnotations: async () => ({ stale: false }),
  clearMarkdownAnnotations: async (_source, generation) => ({ generation }),
  submitMarkdownAnnotations: async () => ({ status: 'delivered', generation: 2 }),
};
setMarkdownAnnotationsTransport(transport);

const notebookSurfaceValue: NotebookSurfaceContextValue = {
  makeDaemon: () => ({
    listDir: async () => [],
    readFile: async () => ({ content: '', hash: '' }),
    writeFile: async () => ({ hash: '' }),
    existsFile: async () => ({ exists: false }),
    readAsset: async () => ({ data: '', mime_type: 'application/octet-stream' }),
    backlinksNotebook: async () => [],
    sendToChief: async () => ({ delivered: false }),
    listFiles: async () => [],
    changeSignal: 0,
  }),
  effectiveNotebookRoot: '/tmp/notebook',
  sendFsWatch: async () => ({ root: '' }),
  sendFsUnwatch: async () => ({ root: '' }),
  connectionGeneration: 0,
};

const daemonApi = {} as DaemonApi;

function Providers({ children }: { children: ReactNode }) {
  return (
    <DaemonApiProvider api={daemonApi}>
      <NotebookSurfaceProvider value={notebookSurfaceValue}>{children}</NotebookSurfaceProvider>
    </DaemonApiProvider>
  );
}

export function TileHeaderHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [targets, setTargets] = useState({ first: 'session-alpha', second: 'session-beta' });
  const requestedWidth = Number(new URLSearchParams(window.location.search).get('width'));
  const workspaceWidth = Number.isFinite(requestedWidth) && requestedWidth > 0 ? requestedWidth : 1816;

  useEffect(() => {
    setTriggerRerender(() => () => {});
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <Providers>
      <div
        data-testid="three-leaf-workspace"
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
          width: workspaceWidth,
          height: 420,
          background: '#111318',
        }}
      >
        <WorkspaceDockTile
          tile={{
            type: 'tile',
            tileId: 'first-document',
            tileKind: 'markdown',
            tileParams: '/tmp/a-long-review-document-title.md',
            tileSessionId: targets.first,
          }}
          workspaceId="header-harness"
          content={{
            path: '/tmp/a-long-review-document-title.md',
            content: '# First document\n\nReview this document.',
          }}
          dragging={false}
          workspaceSessions={sessions}
          onClose={() => {}}
          onFocusDocument={() => {}}
          onRetargetTile={(sessionId) => setTargets((current) => ({ ...current, first: sessionId }))}
          onHeaderPointerDown={() => {}}
          onRequestContent={() => {}}
        />
        <WorkspaceDockTile
          tile={{
            type: 'tile',
            tileId: 'second-document',
            tileKind: 'markdown',
            tileParams: '/tmp/another-long-review-document-title.md',
            tileSessionId: targets.second,
          }}
          workspaceId="header-harness"
          content={{
            path: '/tmp/another-long-review-document-title.md',
            content: '# Second document\n\nReview this one too.',
          }}
          dragging={false}
          workspaceSessions={sessions}
          onClose={() => {}}
          onFocusDocument={() => {}}
          onRetargetTile={(sessionId) => setTargets((current) => ({ ...current, second: sessionId }))}
          onHeaderPointerDown={() => {}}
          onRequestContent={() => {}}
        />
        <div
          data-testid="terminal-leaf"
          style={{
            minWidth: 0,
            borderLeft: '1px solid #353944',
            color: '#c8cad0',
            font: '12px SFMono-Regular, monospace',
          }}
        >
          <div style={{ height: 36, padding: '9px 12px', boxSizing: 'border-box' }}>Codex terminal</div>
        </div>
      </div>
    </Providers>
  );
}
