import { useEffect, useMemo, useRef, useState } from 'react';
import { SessionTerminalDesktop, type SessionTerminalDesktopHandle } from '../../src/components/SessionTerminalDesktop';
import { createPaneRuntimeEventRouterController } from '../../src/components/SessionTerminalDesktop/paneRuntimeEventRouter';
import { settleUi } from '../../src/hooks/uiAutomationSettle';
import { listenPtyEvents } from '../../src/pty/bridge';
import { tileWindowKey, useTileWindowStore } from '../../src/tileWindow/pool';
import type { TerminalDesktopState } from '../../src/types/desktop';
import type { HarnessProps } from '../types';
import appStyles from '../../src/App.css?inline';

const DESKTOP = 'popped-desktop';
const SESSION = 'popped';
const noop = () => {};
const desktop: TerminalDesktopState = {
  agents: [{ id: SESSION, runtimeId: SESSION, sessionId: SESSION, title: SESSION }],
  layoutTree: { type: 'pane', paneId: SESSION, window: { x: 0, y: 0, width: 700, height: 400 } },
};

interface PoppedTerminalControl {
  popOut(): Promise<void>;
  showMainElsewhere(): Promise<void>;
  drain(): Promise<void>;
  size(): { cols: number; rows: number } | null;
  ptyResizes(): Array<{ cols: number; rows: number }>;
}

declare global {
  interface Window { __POPPED_TERMINAL__: PoppedTerminalControl }
}

function openTileWindow(): { window: Window; mount: HTMLElement } {
  const child = window.open('about:blank', 'popped-terminal', 'popup=yes,width=700,height=400');
  if (!child) throw new Error('window.open returned null');
  for (const style of Array.from(document.head.querySelectorAll('style, link[rel="stylesheet"]'))) {
    child.document.head.append(style.cloneNode(true));
  }
  child.document.body.style.cssText = 'margin:0;height:100vh;overflow:hidden;background:#181818';
  const frame = child.document.createElement('div');
  frame.className = 'session-terminal-desktop';
  frame.style.cssText = 'position:absolute;inset:0';
  const mount = child.document.createElement('div');
  mount.className = 'session-terminal-panes';
  mount.style.cssText = 'position:absolute;inset:0';
  frame.append(mount);
  child.document.body.append(frame);
  return { window: child, mount };
}

export function PoppedTerminalHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [mainOnThisDesktop, setMainOnThisDesktop] = useState(true);
  const childRef = useRef<Window | null>(null);
  const desktopRef = useRef<SessionTerminalDesktopHandle | null>(null);
  const ptyResizesRef = useRef<Array<{ cols: number; rows: number }>>([]);
  const router = useMemo(() => createPaneRuntimeEventRouterController(), []);

  useEffect(() => {
    window.__POPPED_TERMINAL__ = {
      async popOut() {
        const opened = openTileWindow();
        childRef.current = opened.window;
        useTileWindowStore.setState({
          byLeaf: new Map([[tileWindowKey(DESKTOP, SESSION), { label: 'tile-window-1', ...opened }]]),
        });
        await settleUi();
      },
      async showMainElsewhere() {
        setMainOnThisDesktop(false);
        await settleUi();
      },
      async drain() {
        await desktopRef.current?.drainPaneTerminal(SESSION);
        await settleUi();
      },
      size: () => desktopRef.current?.getPaneSize(SESSION) ?? null,
      ptyResizes: () => ptyResizesRef.current,
    };
    setTriggerRerender(() => () => {});
    onReady();
    return () => {
      useTileWindowStore.setState({ byLeaf: new Map() });
      childRef.current?.close();
    };
  }, [onReady, setTriggerRerender]);

  useEffect(() => {
    const subscription = listenPtyEvents((event) => {
      if (event.payload.event === 'local_resize') {
        ptyResizesRef.current.push({ cols: event.payload.cols, rows: event.payload.rows });
      }
      router.handleEvent(event.payload);
    });
    return () => { void subscription.then((unsubscribe) => unsubscribe()); };
  }, [router]);

  return <div style={{ position: 'fixed', inset: 0, background: '#181818', color: '#eee' }}>
    <style>{appStyles}</style>
    <SessionTerminalDesktop ref={desktopRef} shelfNarrowestCols={66} desktopId={DESKTOP} terminalState={desktop} activePaneId={SESSION}
      desktopSessions={[{ id: SESSION, label: SESSION, agent: 'claude', cwd: '/tmp/popped-terminal', state: 'idle' }]}
      fontSize={14} enabled isActiveSession={mainOnThisDesktop} isSessionViewVisible={mainOnThisDesktop}
      tileWindowsEnabled attachOnlyDrawn eventRouter={router}
      onSplitPane={noop} onClosePane={noop} onFocusPane={noop} onNavigateOutOfSession={noop} />
  </div>;
}
