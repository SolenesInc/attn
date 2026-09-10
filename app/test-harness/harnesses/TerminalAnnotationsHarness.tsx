import { useEffect, useMemo, useRef, useState } from 'react';
import { SessionTerminalWorkspace } from '../../src/components/SessionTerminalWorkspace';
import { createPaneRuntimeEventRouterController } from '../../src/components/SessionTerminalWorkspace/paneRuntimeEventRouter';
import type { SessionAnnotationApi, SessionMessagesResult } from '../../src/components/TerminalAnnotations/AnnotatedTerminal';
import type { TerminalAnnotation } from '../../src/utils/terminalAnnotations';
import type { TerminalWorkspaceState } from '../../src/types/workspace';
import type { HarnessProps } from '../types';
import { settleUi } from '../../src/hooks/uiAutomationSettle';
import { listenPtyEvents } from '../../src/pty/bridge';
import { armNativePointerWitness, disarmNativePointerWitness, waitForNativePointerWitness } from '../../src/hooks/nativePointerWitness';

const quote = 'The parser already handles CRLF, so the retry wrapper is safe to land as is.';
const mark: TerminalAnnotation = {
  id: 'saved-mark', messageKey: 'turn-1', start: 0, end: quote.length,
  quote, quickLabelId: 'show-the-receipt', comment: '',
};
const messages: SessionMessagesResult = {
  status: 'ready', truncated: false, messages: [{ key: 'turn-1', markdown: quote }],
};
const noop = () => {};

interface AnnotationHarnessControl {
  refreshWorkspace: () => Promise<void>;
  switchWorkspace: () => Promise<void>;
  requestMessages: () => void;
  deliverMessages: () => Promise<void>;
  armPointer: typeof armNativePointerWitness;
  waitPointer: typeof waitForNativePointerWitness;
}

declare global {
  interface Window { __ANNOTATIONS__: AnnotationHarnessControl }
}

// The fixture controls API replies; workspace and terminal focus use production components.
export function TerminalAnnotationsHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [revision, setRevision] = useState(0);
  const [active, setActive] = useState('first');
  const listeners = useRef(new Set<() => void>());
  const pending = useRef<Array<(value: SessionMessagesResult) => void>>([]);
  const holdMessages = useRef(false);
  const deliveredMessages = useRef(messages);
  const router = useMemo(() => createPaneRuntimeEventRouterController(), []);
  const api = useMemo<SessionAnnotationApi>(() => ({
    fetchMessages: async () => {
      window.__HARNESS__.recordCall('fetchMessages', []);
      if (!holdMessages.current) return deliveredMessages.current;
      return new Promise(resolve => pending.current.push(resolve));
    },
    subscribeMessagesChanged: (_sessionId, listener) => {
      listeners.current.add(listener);
      return () => { listeners.current.delete(listener); };
    },
    fetchAnnotations: async sessionId => ({
      annotations: sessionId === 'first' ? [mark] : [], note: '', generation: 1,
    }),
    saveAnnotations: async (...args) => {
      window.__HARNESS__.recordCall('saveAnnotations', args);
      return { stale: false };
    },
    clearAnnotations: async (_sessionId, generation) => ({ generation }),
    submitAnnotations: async (...args) => {
      window.__HARNESS__.recordCall('submitAnnotations', args);
      return { status: 'delivered' };
    },
  }), []);

  useEffect(() => {
    const refreshWorkspace = async () => {
      setRevision(value => value + 1);
      await settleUi();
    };
    window.__ANNOTATIONS__ = {
      armPointer: armNativePointerWitness,
      waitPointer: waitForNativePointerWitness,
      refreshWorkspace,
      switchWorkspace: async () => {
        setActive(value => value === 'first' ? 'second' : 'first');
        await settleUi();
      },
      requestMessages: () => {
        holdMessages.current = true;
        for (const listener of listeners.current) listener();
      },
      deliverMessages: async () => {
        holdMessages.current = false;
        deliveredMessages.current = {
          ...messages,
          messages: [...messages.messages, { key: 'turn-2', markdown: 'The second response arrives while the draft is open.' }],
        };
        for (const resolve of pending.current.splice(0)) resolve(deliveredMessages.current);
        await settleUi();
      },
    };
    setTriggerRerender(refreshWorkspace);
    onReady();
    return disarmNativePointerWitness;
  }, [onReady, setTriggerRerender]);

  useEffect(() => {
    const subscription = listenPtyEvents(event => router.handleEvent(event.payload));
    return () => { void subscription.then(unsubscribe => unsubscribe()); };
  }, [router]);

  return <div style={{ position: 'fixed', inset: 0, background: '#181818', color: '#eee' }}>
    {['first', 'second'].map(id => {
      const workspace: TerminalWorkspaceState = {
        agents: [{ id, runtimeId: id, sessionId: id, title: `${id} ${revision}` }],
        layoutTree: { type: 'pane', paneId: id },
      };
      return <div key={id} data-testid={`workspace-${id}`}
        style={{ position: 'absolute', inset: 0, visibility: active === id ? 'visible' : 'hidden' }}>
        <SessionTerminalWorkspace workspaceId={id} workspace={workspace} activePaneId={id}
          fontSize={14} enabled isActiveSession={active === id} isSessionViewVisible={active === id}
          eventRouter={router} annotationApi={api} focusRequestToken={revision}
          onSplitPane={noop} onClosePane={noop} onFocusPane={noop} onNavigateOutOfSession={noop} />
      </div>;
    })}
  </div>;
}
