import { useEffect, useLayoutEffect, useRef, useState, type MouseEvent, type ReactNode } from 'react';
import { DictationController, useDictationStore, type Dictation, type NativeDictation } from './controller';
import { speechSupported } from './speechHelper';
import type { DictationInteraction } from './settings';
import type { Refiner } from './refiner';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './DictationLayer.css';

const keepFocus = (event: MouseEvent) => event.preventDefault();

function Action({ onClick, primary, children, testId }: { onClick: () => void; primary?: boolean; children: ReactNode; testId?: string }) {
  return <button type="button" tabIndex={-1} data-primary={primary} data-testid={testId} onMouseDown={keepFocus} onClick={onClick}>{children}</button>;
}

function words(text: string) {
  return text.split(/(\s+)/).filter(Boolean);
}

export function wordDiff(before: string, after: string): { text: string; kind: 'same' | 'removed' | 'added' }[] {
  const a = words(before);
  const b = words(after);
  const table = Array.from({ length: a.length + 1 }, () => Array.from({ length: b.length + 1 }, () => 0));
  for (let i = a.length - 1; i >= 0; i--) for (let j = b.length - 1; j >= 0; j--) {
    table[i][j] = a[i] === b[j] ? table[i + 1][j + 1] + 1 : Math.max(table[i + 1][j], table[i][j + 1]);
  }
  const parts: { text: string; kind: 'same' | 'removed' | 'added' }[] = [];
  let i = 0; let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) { parts.push({ text: a[i], kind: 'same' }); i++; j++; }
    else if (j < b.length && (i === a.length || table[i][j + 1] > table[i + 1][j])) { parts.push({ text: b[j], kind: 'added' }); j++; }
    else { parts.push({ text: a[i], kind: 'removed' }); i++; }
  }
  return parts.reduce<typeof parts>((merged, part) => {
    const last = merged[merged.length - 1];
    if (last?.kind === part.kind) last.text += part.text;
    else merged.push({ ...part });
    return merged;
  }, []);
}

function Diff({ before, after }: { before: string; after: string }) {
  return <>{wordDiff(before, after).map((part, index) => part.kind === 'same' ? part.text : part.kind === 'added' ? <ins key={index}>{part.text}</ins> : <del key={index}>{part.text}</del>)}</>;
}

function elapsed(since: number | undefined, now: number) {
  const seconds = since ? Math.max(0, Math.floor((now - since) / 1000)) : 0;
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

function Clock({ since }: { since?: number }) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  return <span>{elapsed(since, now)}</span>;
}

function Pill({ dictation, controller }: { dictation: Dictation; controller: DictationController }) {
  const { key, phase } = dictation;
  let text: ReactNode;
  let actions: ReactNode = null;
  let tone: 'error' | undefined;
  if (phase === 'starting') {
    text = 'Starting the microphone…';
    actions = <Action onClick={() => controller.cancel(key)}>Cancel</Action>;
  } else if (phase === 'recording') {
    text = <>Listening <Clock since={dictation.listeningAt} /></>;
    actions = <><Action primary testId="dictation-stop" onClick={() => controller.stop(key)}>Stop</Action><Action testId="dictation-cancel" onClick={() => controller.cancel(key)}>Cancel</Action></>;
  } else if (phase === 'transcribing') {
    text = dictation.notice ?? 'Transcribing…';
  } else if (phase === 'refining') {
    text = 'Refining…';
    actions = <Action testId="dictation-keep-original" onClick={() => controller.discard(key)}>Keep original</Action>;
  } else if (dictation.refineError) {
    text = dictation.refineError;
    tone = 'error';
    actions = <><Action testId="dictation-retry" onClick={() => controller.retry(key)}>Retry</Action><Action onClick={() => controller.discard(key)}>Dismiss</Action></>;
  } else {
    text = dictation.notice ?? '';
    tone = 'error';
    actions = <>
      {dictation.microphoneDenied && <Action primary onClick={() => controller.openMicrophoneSettings()}>Open Privacy Settings</Action>}
      <Action onClick={() => controller.discard(key)}>Dismiss</Action>
    </>;
  }
  return <div className="dictation-pill" role="status" data-testid="dictation-pill" data-phase={phase} data-tone={tone}>
    <span className="dictation-dot" aria-hidden="true" />
    <span className="dictation-pill-text">{text}</span>
    {actions && <span className="dictation-actions">{actions}</span>}
  </div>;
}

function Card({ dictation, controller }: { dictation: Dictation; controller: DictationController }) {
  const { key, raw = '', refined, phase, refineError } = dictation;
  const refinedOffer = dictation.rawInField;
  return <section className="dictation-card" aria-label="Dictation" data-testid="dictation-card" data-card={dictation.card}>
    {dictation.notice && <p className="dictation-card-note">{dictation.notice}</p>}
    {!refinedOffer && <>
      <span className="dictation-card-label">Original</span>
      <div className="dictation-card-text" data-testid="dictation-original">{raw}</div>
    </>}
    <span className="dictation-card-label">Refined</span>
    <div className="dictation-card-text" data-testid="dictation-refined">
      {refined !== undefined ? <Diff before={raw} after={refined} /> : phase === 'refining' ? 'Refining…' : refineError}
    </div>
    <div className="dictation-actions">
      {!refinedOffer && <Action primary={refined === undefined} testId="dictation-use-original" onClick={() => controller.choose(key, raw)}>Use original</Action>}
      {refined !== undefined && <Action primary testId="dictation-use-refined" onClick={() => controller.choose(key, refined)}>{refinedOffer ? 'Insert at cursor' : 'Use refined'}</Action>}
      {refineError && <Action testId="dictation-retry" onClick={() => controller.retry(key)}>Retry</Action>}
      <Action onClick={() => controller.copy(key, refined ?? raw)}>Copy</Action>
      <Action testId="dictation-discard" onClick={() => controller.discard(key)}>Discard</Action>
    </div>
  </section>;
}

function useEscapeCancelsRecording(controller: DictationController, dictations: Dictation[]) {
  const recording = dictations.find(dictation => dictation.phase === 'starting' || dictation.phase === 'recording')?.key;
  useEscapeStack(() => { if (recording) controller.cancel(recording); }, recording !== undefined);
}

export function DictationLayer({ controller }: { controller: DictationController }) {
  const dictations = useDictationStore(state => state.dictations);
  useEscapeCancelsRecording(controller, dictations);
  if (dictations.length === 0) return null;
  return <div className="dictation-layer" data-testid="dictation-layer">
    {dictations.map(dictation => dictation.card
      ? <Card key={dictation.key} dictation={dictation} controller={controller} />
      : <Pill key={dictation.key} dictation={dictation} controller={controller} />)}
  </div>;
}

interface WindowDictationProps {
  enabled: boolean;
  profileId: string;
  interaction: DictationInteraction;
  refiner: Refiner;
  native?: NativeDictation;
}

export function WindowDictation({ enabled, profileId, interaction, refiner, native }: WindowDictationProps) {
  const latest = useRef({ profileId, interaction, refiner });
  useLayoutEffect(() => { latest.current = { profileId, interaction, refiner }; });
  const [controller, setController] = useState<DictationController | null>(null);
  const active = enabled && (native !== undefined || speechSupported());
  useEffect(() => {
    if (!active) return;
    const next = new DictationController({
      profileId: () => latest.current.profileId,
      interaction: () => latest.current.interaction,
      refiner: () => latest.current.refiner,
    }, native);
    next.start();
    setController(next);
    return () => { next.dispose(); setController(null); };
  }, [active]);
  useEffect(() => { controller?.forgetOtherProfiles(profileId); }, [controller, profileId]);
  return controller ? <DictationLayer controller={controller} /> : null;
}
