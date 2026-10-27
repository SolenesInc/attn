import { useEffect, useMemo } from 'react';
import { SessionTerminalDesktop } from '../../src/components/SessionTerminalDesktop';
import { createPaneRuntimeEventRouterController } from '../../src/components/SessionTerminalDesktop/paneRuntimeEventRouter';
import type { TerminalDesktopState } from '../../src/types/desktop';
import type { HarnessProps } from '../types';
import { listenPtyEvents } from '../../src/pty/bridge';
import appStyles from '../../src/App.css?inline';

const noop = () => {};

export function OutpostTerminalLinksHarness({ onReady }: HarnessProps) {
  const router = useMemo(() => createPaneRuntimeEventRouterController(), []);
  const host = new URLSearchParams(window.location.search).get('host') ?? undefined;
  const id = host ? 'remote' : 'local';

  useEffect(() => {
    const subscription = listenPtyEvents(event => router.handleEvent(event.payload));
    onReady();
    return () => { void subscription.then(unsubscribe => unsubscribe()); };
  }, [onReady, router]);

  const desktop: TerminalDesktopState = {
    agents: [{ id, runtimeId: id, sessionId: id, title: id }],
    layoutTree: { type: 'pane', paneId: id },
  };
  return <div style={{ position: 'fixed', inset: 0, background: '#181818', color: '#eee' }}>
    <style>{appStyles}</style>
    <SessionTerminalDesktop desktopId="links" terminalState={desktop} activePaneId={id}
      desktopSessions={[{ id, label: id, agent: 'shell', cwd: '/tmp/test/terminal-links', state: 'idle', host: host && 'studio', hostName: host, reach: 'reachable' }]}
      fontSize={14} enabled isActiveSession isSessionViewVisible eventRouter={router}
      onOpenMarkdown={(...args) => window.__HARNESS__.recordCall('openMarkdown', args)}
      onTerminalPathRefused={(...args) => window.__HARNESS__.recordCall('pathRefused', args)}
      onSplitPane={noop} onClosePane={noop} onFocusPane={noop} onNavigateOutOfSession={noop} />
  </div>;
}
