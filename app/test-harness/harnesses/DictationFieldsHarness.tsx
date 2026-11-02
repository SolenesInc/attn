import { useEffect, useState } from 'react';
import { WindowDictation } from '../../src/dictation/DictationLayer';
import { useDictationStore, type NativeDictation, type NativeUpdate } from '../../src/dictation/controller';
import type { RefineOutcome, RefineRequest, Refiner } from '../../src/dictation/refiner';
import type { DictationInteraction } from '../../src/dictation/settings';
import { useEscapeStack } from '../../src/hooks/useEscapeStack';
import type { HarnessProps } from '../types';
import '../../src/browser/runtime';

export interface DictationProbe {
  press(browser?: string): void;
  browserCalls: { label: string; action: string }[];
  browserGone: boolean;
  update(update: NativeUpdate): void;
  started: number[];
  stopped: number[];
  canceled: (number | null)[];
  refines: RefineRequest[];
  refineCancels: string[];
  answer(requestId: string, outcome: RefineOutcome): void;
  setInteraction(mode: DictationInteraction): void;
  setProfile(profileId: string): void;
  receipts(): unknown[];
}

declare global {
  interface Window { __dictation: DictationProbe }
}

export function createProbe() {
  let onRequest: (request: { browser: string | null }) => void = () => {};
  let browserIds = 0;
  let onUpdate: (update: NativeUpdate) => void = () => {};
  let next = 0;
  const answers = new Map<string, (outcome: RefineOutcome) => void>();
  const probe: DictationProbe = {
    press: browser => onRequest({ browser: browser ?? null }),
    browserCalls: [], browserGone: false,
    update: update => onUpdate(update),
    started: [], stopped: [], canceled: [], refines: [], refineCancels: [],
    answer: (requestId, outcome) => { answers.get(requestId)?.(outcome); answers.delete(requestId); },
    setInteraction: () => {},
    setProfile: () => {},
    receipts: () => useDictationStore.getState().receipts,
  };
  const native: NativeDictation = {
    start: async () => { const recording = ++next; probe.started.push(recording); return { recording }; },
    stop: async recording => { probe.stopped.push(recording); },
    cancel: async recording => { probe.canceled.push(recording); },
    subscribe(request, update) { onRequest = request; onUpdate = update; return () => {}; },
    openMicrophoneSettings: () => {},
    browser: async (label, action, id, text) => {
      probe.browserCalls.push({ label, action });
      if (probe.browserGone) throw new Error('frame not found');
      const value = await window.__attnBrowser!.execute(`dictation_${action}`, { id: id ?? `page-${++browserIds}`, text });
      return JSON.stringify(value);
    },
  };
  const refiner: Refiner = {
    refine: request => new Promise(resolve => { probe.refines.push(request); answers.set(request.requestId, resolve); }),
    cancel: requestId => { probe.refineCancels.push(requestId); answers.delete(requestId); },
  };
  return { probe, native, refiner };
}

function Panel({ onClose }: { onClose: () => void }) {
  useEscapeStack(onClose, true);
  return <section aria-label="Panel">Panel</section>;
}

export function DictationFieldsHarness({ onReady }: HarnessProps) {
  const [{ probe, native, refiner }] = useState(createProbe);
  const [interaction, setInteraction] = useState<DictationInteraction>('insertAtCursor');
  const [profileId, setProfileId] = useState('profile-a');
  const [message, setMessage] = useState('');
  const [search, setSearch] = useState('');
  const [messageChanges, setMessageChanges] = useState(0);
  const [generation, setGeneration] = useState(0);
  const [sent, setSent] = useState<string[]>([]);
  const [panel, setPanel] = useState(false);
  const [rich, setRich] = useState(true);
  const [richInputs, setRichInputs] = useState<string[]>([]);
  useEffect(() => {
    probe.setInteraction = setInteraction;
    probe.setProfile = setProfileId;
    window.__dictation = probe;
    onReady();
  }, [onReady, probe]);
  return <main style={{ padding: 16, display: 'grid', gap: 8, width: 480 }}>
    <textarea key={generation} aria-label="Message" rows={4} value={message}
      onChange={event => { setMessage(event.target.value); setMessageChanges(count => count + 1); }} />
    <input aria-label="Search" value={search} onChange={event => setSearch(event.target.value)} />
    <input aria-label="Read-only" readOnly value="fixed" />
    {rich && <div aria-label="Rich" role="textbox" contentEditable suppressContentEditableWarning
      onInput={event => { const inputType = (event.nativeEvent as InputEvent).inputType; setRichInputs(previous => [...previous, inputType]); }}>Hello world</div>}
    <button type="button" onClick={() => setRich(false)}>Remove rich editor</button>
    <button type="button" onClick={() => { setSent(previous => [...previous, message]); setMessage(''); }}>Send</button>
    <button type="button" onClick={() => setGeneration(value => value + 1)}>Replace message field</button>
    <button type="button" onMouseDown={event => event.preventDefault()} onClick={() => setPanel(true)}>Open panel</button>
    {panel && <Panel onClose={() => setPanel(false)} />}
    <output data-testid="message-state">{message}</output>
    <output data-testid="search-state">{search}</output>
    <output data-testid="message-changes">{messageChanges}</output>
    <output data-testid="sent">{JSON.stringify(sent)}</output>
    <output data-testid="rich-inputs">{JSON.stringify(richInputs)}</output>
    <WindowDictation enabled profileId={profileId} interaction={interaction} refiner={refiner} native={native} />
  </main>;
}
