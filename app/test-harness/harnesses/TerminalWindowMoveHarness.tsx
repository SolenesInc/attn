import { useEffect, useMemo, useRef, useState } from 'react';
import { SessionTerminalDesktop } from '../../src/components/SessionTerminalDesktop';
import { createPaneRuntimeEventRouterController } from '../../src/components/SessionTerminalDesktop/paneRuntimeEventRouter';
import { settleUi } from '../../src/hooks/uiAutomationSettle';
import { listenPtyEvents } from '../../src/pty/bridge';
import { TileWindowProvider } from '../../src/tileWindow';
import type { TerminalDesktopState } from '../../src/types/desktop';
import type { HarnessProps } from '../types';
import appStyles from '../../src/App.css?inline';

const SESSION = 'moving';
const noop = () => {};
const desktop: TerminalDesktopState = {
  agents: [{ id: SESSION, runtimeId: SESSION, sessionId: SESSION, title: SESSION }],
  layoutTree: { type: 'pane', paneId: SESSION },
};

interface TerminalWindowMoveControl {
  moveTo(target: 'child' | 'main'): Promise<void>;
  tileWindowNow(): number;
}

declare global {
  interface Window { __TERMINAL_WINDOW_MOVE__: TerminalWindowMoveControl }
}

export function TerminalWindowMoveHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [tileWindow, setTileWindow] = useState<Window>(window);
  const tileWindowRef = useRef<Window>(window);
  const childRef = useRef<Window | null>(null);
  const router = useMemo(() => createPaneRuntimeEventRouterController(), []);

  useEffect(() => {
    window.__TERMINAL_WINDOW_MOVE__ = {
      async moveTo(target) {
        if (target === 'child') {
          childRef.current ??= window.open('about:blank', 'terminal-window-move', 'width=300,height=200');
          if (!childRef.current) throw new Error('window.open returned null');
        }
        const next = target === 'child' ? childRef.current! : window;
        tileWindowRef.current = next;
        setTileWindow(next);
        await settleUi();
      },
      tileWindowNow: () => tileWindowRef.current.performance.now(),
    };
    setTriggerRerender(() => () => {});
    onReady();
    return () => { childRef.current?.close(); };
  }, [onReady, setTriggerRerender]);

  useEffect(() => {
    const subscription = listenPtyEvents(event => router.handleEvent(event.payload));
    return () => { void subscription.then(unsubscribe => unsubscribe()); };
  }, [router]);

  return <div style={{ position: 'fixed', inset: 0, background: '#181818', color: '#eee' }}>
    <style>{appStyles}</style>
    <TileWindowProvider value={tileWindow}>
      <SessionTerminalDesktop shelfNarrowestCols={66} desktopId={SESSION} terminalState={desktop} activePaneId={SESSION}
        desktopSessions={[{ id: SESSION, label: SESSION, agent: 'claude', cwd: '/tmp/window-move', state: 'idle' }]}
        fontSize={14} enabled isActiveSession isSessionViewVisible eventRouter={router}
        onSplitPane={noop} onClosePane={noop} onFocusPane={noop} onNavigateOutOfSession={noop} />
    </TileWindowProvider>
  </div>;
}
