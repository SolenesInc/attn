import { useEffect, useMemo, useState } from 'react';
import { WindowDictation } from '../../src/dictation/DictationLayer';
import type { DictationInteraction } from '../../src/dictation/settings';
import { LiveMarkdownEditor } from '../../src/components/notebook/LiveMarkdownEditor';
import { SessionTerminalDesktop } from '../../src/components/SessionTerminalDesktop';
import { createPaneRuntimeEventRouterController } from '../../src/components/SessionTerminalDesktop/paneRuntimeEventRouter';
import type { TerminalReach } from '../../src/components/SessionTerminalDesktop/useGhosttyPaneRuntime';
import type { TerminalDesktopState } from '../../src/types/desktop';
import { listenPtyEvents } from '../../src/pty/bridge';
import appStyles from '../../src/App.css?inline';
import { createProbe } from './DictationFieldsHarness';
import type { HarnessProps } from '../types';

interface TargetsControl {
  ptyWrites(): { runtime: string; data: string }[];
  output(data: string): void;
  replaceRuntime(): void;
  succeedSession(): void;
  setReach(reach: TerminalReach): void;
  runtime(): string;
}

declare global {
  interface Window { __targets: TargetsControl }
}

const noop = () => {};

function decode(base64: string) {
  return new TextDecoder().decode(Uint8Array.from(atob(base64), char => char.charCodeAt(0)));
}

export function DictationTargetsHarness({ onReady }: HarnessProps) {
  const [{ probe, native, refiner }] = useState(createProbe);
  const [interaction, setInteraction] = useState<DictationInteraction>('insertAtCursor');
  const [profileId, setProfileId] = useState('profile-a');
  const [doc, setDoc] = useState('# Notes\n\nFirst line.\n');
  const [notes, setNotes] = useState('');
  const [runtime, setRuntime] = useState('term-1');
  const [session, setSession] = useState('term');
  const [reach, setReach] = useState<TerminalReach>('reachable');
  const router = useMemo(() => createPaneRuntimeEventRouterController(), []);

  useEffect(() => {
    const writes: { runtime: string; data: string }[] = [];
    let expectedOutput: string[] = [];
    let current = 'term-1';
    const subscription = listenPtyEvents(event => {
      router.handleEvent(event.payload);
      const payload = event.payload;
      if (payload.event !== 'data') return;
      const data = decode(payload.data);
      if (data.startsWith('attn mock pty:')) return;
      const echo = expectedOutput.indexOf(data);
      if (echo >= 0) { expectedOutput.splice(echo, 1); return; }
      writes.push({ runtime: payload.id, data });
    });
    probe.setInteraction = setInteraction;
    probe.setProfile = setProfileId;
    window.__dictation = probe;
    window.__targets = {
      ptyWrites: () => writes,
      output: data => { expectedOutput = [...expectedOutput, data]; window.__TEST_EMIT_PTY_DATA?.(current, data); },
      replaceRuntime: () => { current = current === 'term-1' ? 'term-2' : 'term-1'; setRuntime(current); },
      succeedSession: () => setSession(id => `${id}-next`),
      setReach,
      runtime: () => current,
    };
    onReady();
    return () => { void subscription.then(unsubscribe => unsubscribe()); };
  }, [onReady, probe, router]);

  const desktop: TerminalDesktopState = {
    agents: [{ id: 'term', runtimeId: runtime, sessionId: session, title: 'Shell' }],
    layoutTree: { type: 'pane', paneId: 'term' },
  };

  return <div style={{ position: 'fixed', inset: 0, display: 'grid', gridTemplateColumns: '1fr 1fr', background: '#181818', color: '#eee' }}>
    <style>{appStyles}</style>
    <div style={{ position: 'relative' }}>
      <SessionTerminalDesktop desktopId="d" terminalState={desktop} activePaneId="term"
        desktopSessions={[{ id: session, label: 'Shell', agent: 'claude', cwd: '/tmp/dictation', state: 'idle', reach }]}
        fontSize={14} enabled isActiveSession isSessionViewVisible eventRouter={router}
        onSplitPane={noop} onClosePane={noop} onFocusPane={noop} onNavigateOutOfSession={noop} />
    </div>
    <div style={{ display: 'grid', gridTemplateRows: 'auto 1fr auto auto', gap: 8, padding: 8 }}>
      <textarea aria-label="Notes" rows={3} value={notes} onChange={event => setNotes(event.target.value)} />
      <div className="notebook-browser-live-editor" style={{ border: '1px solid #444', minHeight: 160 }}>
        <LiveMarkdownEditor value={doc} onChange={setDoc} ariaLabel="Document" />
      </div>
      <output data-testid="doc-state" style={{ whiteSpace: 'pre' }}>{doc}</output>
      <WindowDictation enabled profileId={profileId} interaction={interaction} refiner={refiner} native={native} />
    </div>
  </div>;
}
