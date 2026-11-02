import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { create } from 'zustand';
import { afterComposition, missingTargetReason, type Insertion } from './field';
import { captureBrowserTarget, type BrowserDictationHost } from './browser';
import { captureTarget, placeOf, type EditTarget, type PasteTarget, type TargetPlace, type DictationTarget } from './target';
import type { DictationInteraction } from './settings';
import type { Refiner } from './refiner';
import { DictationTextFormat } from '../types/generated';

const PASTE_LOST: Record<PasteTarget['place'], string> = {
  terminal: 'The terminal closed, restarted or became unreachable, so nothing was pasted.',
  richEditor: 'The editor or its page changed, so nothing was inserted.',
};

export type DictationPhase = 'starting' | 'recording' | 'transcribing' | 'refining' | 'settled';

export interface Dictation {
  key: string;
  recording: number | null;
  profileId: string;
  mode: DictationInteraction;
  target: TargetPlace;
  phase: DictationPhase;
  listeningAt?: number;
  raw?: string;
  audioMs?: number;
  refined?: string;
  refineError?: string;
  rawInField: boolean;
  card: 'compare' | 'offer' | null;
  notice?: string;
  microphoneDenied?: boolean;
}

export interface DictationReceipt {
  recording: number;
  audioMs: number;
  reason: string;
  pressToListeningMs: number | null;
  stopToTranscriptMs: number | null;
  stopToRawInDomMs: number | null;
  stopToRawPaintMs: number | null;
  helper: { asrMs?: number; finishMs?: number; segments?: number };
  rawPaintedAt: number;
}

type Timeline = { pressed?: number; requested: number; engineStarted?: number; firstAudio?: number; stopped?: number; transcribed?: number };

export type NativeUpdate =
  | { kind: 'listening'; recording: number; at: number }
  | { kind: 'stopped'; recording: number; reason: string; at: number }
  | { kind: 'transcript'; recording: number; text: string; speech: boolean; audioMs: number; reason: string; timeline: Timeline; helper: DictationReceipt['helper'] }
  | { kind: 'failed'; recording: number; message: string }
  | { kind: 'canceled'; recording: number };

export interface DictationRequest {
  browser: string | null;
}

export const DICTATION_EVENT = 'dictation';
export const DICTATION_REQUEST_EVENT = 'dictation-request';
const NOTICE_MS = 4000;
const STOP_NOTICES: Record<string, string> = {
  deviceChanged: 'The microphone changed. Transcribing what was recorded.',
};
const RECEIPT_LIMIT = 200;

interface Store {
  dictations: Dictation[];
  receipts: DictationReceipt[];
}

export const useDictationStore = create<Store>(() => ({ dictations: [], receipts: [] }));

export interface NativeDictation {
  start(): Promise<{ recording: number }>;
  stop(recording: number): Promise<unknown>;
  cancel(recording: number | null): Promise<unknown>;
  subscribe(onRequest: (request: DictationRequest) => void, onUpdate: (update: NativeUpdate) => void): () => void;
  browser: BrowserDictationHost;
  openMicrophoneSettings(): void;
}

const MICROPHONE_SETTINGS = 'x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone';

export const tauriDictation: NativeDictation = {
  start: () => invoke<{ recording: number }>('dictation_start'),
  stop: recording => invoke('dictation_stop', { recording }),
  cancel: recording => invoke('dictation_cancel', { recording }),
  subscribe(onRequest, onUpdate) {
    const request = listen<DictationRequest>(DICTATION_REQUEST_EVENT, ({ payload }) => onRequest({ browser: payload?.browser ?? null }));
    const updates = listen<NativeUpdate>(DICTATION_EVENT, ({ payload }) => onUpdate(payload));
    return () => { void request.then(stop => stop()); void updates.then(stop => stop()); };
  },
  browser: (label, action, id, text) => invoke<string>('browser_host_dictation', { label, action, id, text }),
  openMicrophoneSettings: () => { void import('@tauri-apps/plugin-opener').then(({ openUrl }) => openUrl(MICROPHONE_SETTINGS)); },
};

export interface DictationContext {
  profileId: () => string;
  interaction: () => DictationInteraction;
  refiner: () => Refiner;
}

async function insertAtCursor(text: string): Promise<Insertion> {
  const target = captureTarget(document.activeElement);
  if (!target) return 'targetGone';
  try {
    return target.kind === 'paste' ? await target.paste(text) : await target.replace(text);
  } finally {
    target.release();
  }
}

export class DictationController {
  private targets = new Map<string, DictationTarget>();
  private pasting = new Set<string>();
  private requests = new Map<string, string>();
  private early = new Map<number, NativeUpdate[]>();
  private stops: (() => void)[] = [];
  private sequence = 0;

  constructor(private readonly context: DictationContext, private readonly native: NativeDictation = tauriDictation) {}

  start() {
    this.stops.push(this.native.subscribe(request => this.begin(request), update => this.receive(update)));
    void this.native.cancel(null).catch(() => undefined);
  }

  dispose() {
    for (const dictation of useDictationStore.getState().dictations) this.discard(dictation.key);
    for (const stop of this.stops.splice(0)) stop();
  }

  private patch(key: string, change: Partial<Dictation>) {
    useDictationStore.setState(state => ({ dictations: state.dictations.map(item => item.key === key ? { ...item, ...change } : item) }));
  }

  private find(key: string) {
    return useDictationStore.getState().dictations.find(item => item.key === key);
  }

  private byRecording(recording: number) {
    return useDictationStore.getState().dictations.find(item => item.recording === recording);
  }

  private add(dictation: Dictation) {
    useDictationStore.setState(state => ({ dictations: [...state.dictations, dictation] }));
  }

  private remove(key: string) {
    const target = this.targets.get(key);
    target?.release();
    this.targets.delete(key);
    const requestId = this.requests.get(key);
    if (requestId) this.context.refiner().cancel(requestId);
    this.requests.delete(key);
    useDictationStore.setState(state => ({ dictations: state.dictations.filter(item => item.key !== key) }));
  }

  private notice(message: string, extra: Partial<Dictation> = {}) {
    const key = `notice-${++this.sequence}`;
    this.add({ key, recording: null, profileId: this.context.profileId(), mode: this.context.interaction(), target: 'field', phase: 'settled', rawInField: false, card: null, notice: message, ...extra });
    if (!extra.microphoneDenied) setTimeout(() => this.remove(key), NOTICE_MS);
  }

  begin(request: DictationRequest = { browser: null }) {
    if (useDictationStore.getState().dictations.some(item => item.phase === 'starting' || item.phase === 'recording')) return;
    if (request.browser) { void this.beginInBrowser(request.browser); return; }
    const focused = document.activeElement;
    const target = captureTarget(focused);
    if (!target) { this.notice(missingTargetReason(focused)); return; }
    const key = this.starting(placeOf(target));
    this.targets.set(key, target);
    this.record(key);
  }

  private starting(place: TargetPlace) {
    const key = `dictation-${++this.sequence}`;
    this.add({ key, recording: null, profileId: this.context.profileId(), mode: this.context.interaction(), target: place, phase: 'starting', rawInField: false, card: null });
    return key;
  }

  private async beginInBrowser(label: string) {
    const key = this.starting('field');
    const target = await captureBrowserTarget(this.native.browser, label);
    if (!this.find(key)) { if (!('reason' in target)) target.release(); return; }
    if ('reason' in target) { this.remove(key); this.notice(target.reason); return; }
    this.targets.set(key, target);
    this.patch(key, { target: placeOf(target) });
    this.record(key);
  }

  private record(key: string) {
    this.native.start().then(({ recording }) => {
      const updates = this.early.get(recording) ?? [];
      this.early.delete(recording);
      if (!this.find(key)) { void this.native.cancel(recording); return; }
      this.patch(key, { recording });
      for (const update of updates) this.receive(update);
    }, error => {
      this.remove(key);
      const message = String(error);
      this.notice(message, message.includes('microphone access') ? { microphoneDenied: true } : {});
    });
  }

  stop(key: string) {
    const recording = this.find(key)?.recording;
    if (recording != null) void this.native.stop(recording);
  }

  cancel(key: string) {
    const dictation = this.find(key);
    if (dictation?.recording != null && (dictation.phase === 'starting' || dictation.phase === 'recording')) void this.native.cancel(dictation.recording);
    this.remove(key);
  }

  openMicrophoneSettings() {
    this.native.openMicrophoneSettings();
  }

  private receive(update: NativeUpdate) {
    const dictation = this.byRecording(update.recording);
    if (!dictation) {
      if (useDictationStore.getState().dictations.some(item => item.phase === 'starting' && item.recording === null)) {
        this.early.set(update.recording, [...(this.early.get(update.recording) ?? []), update]);
      }
      return;
    }
    switch (update.kind) {
      case 'listening': this.patch(dictation.key, { phase: 'recording', listeningAt: update.at }); break;
      case 'stopped': this.patch(dictation.key, { phase: 'transcribing', notice: STOP_NOTICES[update.reason] }); break;
      case 'failed': this.remove(dictation.key); this.notice(update.message); break;
      case 'canceled': this.remove(dictation.key); break;
      case 'transcript': void this.transcribed(dictation, update); break;
    }
  }

  private async transcribed(dictation: Dictation, update: Extract<NativeUpdate, { kind: 'transcript' }>) {
    const raw = update.text.trim();
    if (!update.speech || !raw) {
      this.remove(dictation.key);
      this.notice('No speech was heard.');
      return;
    }
    const target = this.targets.get(dictation.key);
    if (dictation.mode === 'compareFirst') {
      this.patch(dictation.key, { raw, audioMs: update.audioMs, card: 'compare', notice: undefined });
    } else if (target?.kind === 'paste') {
      this.patch(dictation.key, { raw, audioMs: update.audioMs, notice: undefined });
      if (target.place === 'terminal') target.claimEnter(() => this.choose(dictation.key, raw));
    } else {
      await afterComposition();
      const current = this.targets.get(dictation.key);
      const inserted = current?.kind === 'edit' ? await current.replace(raw) === 'inserted' : false;
      this.patch(dictation.key, inserted ? { raw, audioMs: update.audioMs, rawInField: true, notice: undefined } : { raw, audioMs: update.audioMs, card: 'offer', notice: 'The field changed while you spoke, so nothing was inserted.' });
    }
    this.paintReceipt(update);
    this.refine(dictation.key, raw, update.audioMs);
  }

  private paintReceipt(update: Extract<NativeUpdate, { kind: 'transcript' }>) {
    const { timeline } = update;
    const inDom = Date.now();
    requestAnimationFrame(() => setTimeout(() => {
      const painted = Date.now();
      const receipt: DictationReceipt = {
        recording: update.recording,
        audioMs: update.audioMs,
        reason: update.reason,
        pressToListeningMs: timeline.pressed != null && timeline.firstAudio != null ? timeline.firstAudio - timeline.pressed : null,
        stopToTranscriptMs: timeline.stopped != null && timeline.transcribed != null ? timeline.transcribed - timeline.stopped : null,
        stopToRawInDomMs: timeline.stopped != null ? inDom - timeline.stopped : null,
        stopToRawPaintMs: timeline.stopped != null ? painted - timeline.stopped : null,
        helper: update.helper,
        rawPaintedAt: painted,
      };
      useDictationStore.setState(state => ({ receipts: [...state.receipts, receipt].slice(-RECEIPT_LIMIT) }));
    }, 0));
  }

  refine(key: string, raw: string, audioMs: number) {
    const requestId = crypto.randomUUID();
    this.requests.set(key, requestId);
    this.patch(key, { phase: 'refining', refineError: undefined });
    const format = this.targets.get(key)?.format ?? DictationTextFormat.Plain;
    void this.context.refiner().refine({ requestId, profileId: this.find(key)?.profileId ?? '', rawText: raw, recordingDurationMs: audioMs, format }).then(outcome => {
      if (this.requests.get(key) !== requestId) return;
      this.requests.delete(key);
      const dictation = this.find(key);
      if (!dictation) return;
      if (!outcome.ok) { this.patch(key, { phase: 'settled', refineError: outcome.error }); return; }
      this.refined(dictation, outcome.text);
    });
  }

  retry(key: string) {
    const dictation = this.find(key);
    if (dictation?.raw) this.refine(key, dictation.raw, dictation.audioMs ?? 0);
  }

  private async refined(dictation: Dictation, refined: string) {
    const target = this.targets.get(dictation.key);
    if (!dictation.card && target?.kind === 'paste') { void this.pasteOnce(dictation.key, target, refined, refined); return; }
    if (dictation.card || !dictation.rawInField || target?.kind !== 'edit') { this.patch(dictation.key, { phase: 'settled', refined }); return; }
    if (refined === dictation.raw) { this.remove(dictation.key); return; }
    await afterComposition();
    if (this.targets.get(dictation.key) !== target) return;
    const outcome = await target.replace(refined);
    if (outcome === 'inserted') { this.remove(dictation.key); return; }
    const notice = outcome === 'targetGone' ? 'The field closed or its page changed, so the refined version was not applied.' : 'You edited the dictated text, so the refined version was not applied.';
    this.patch(dictation.key, { phase: 'settled', refined, card: 'offer', notice });
  }

  choose(key: string, text: string) {
    const dictation = this.find(key);
    if (!dictation || this.pasting.has(key)) return;
    const target = dictation.rawInField ? undefined : this.targets.get(key);
    if (target?.kind === 'paste') void this.pasteOnce(key, target, text);
    else void this.insertChosen(key, target, text);
  }

  private async pasteOnce(key: string, target: PasteTarget, text: string, refined?: string) {
    this.targets.delete(key);
    const requestId = this.requests.get(key);
    if (requestId) this.context.refiner().cancel(requestId);
    this.requests.delete(key);
    this.pasting.add(key);
    const pasted = await Promise.resolve(target.paste(text)).finally(() => this.pasting.delete(key));
    target.release();
    if (pasted === 'inserted') { this.remove(key); return; }
    this.patch(key, { phase: 'settled', card: 'offer', ...(refined === undefined ? {} : { refined }), notice: PASTE_LOST[target.place] });
  }

  private async insertChosen(key: string, target: EditTarget | undefined, text: string) {
    const tracked = target ? await target.replace(text) : 'targetGone';
    const outcome = tracked === 'inserted' ? tracked : await insertAtCursor(text);
    if (outcome === 'inserted') { this.remove(key); return; }
    this.patch(key, { notice: outcome === 'targetGone' ? 'Click into a text field to insert, or copy the text.' : 'The field changed. Click where the text should go, or copy it.' });
    if (outcome !== 'targetGone' && target) { target.release(); this.targets.delete(key); }
  }

  copy(key: string, text: string) {
    void navigator.clipboard.writeText(text).then(() => this.remove(key), error => this.patch(key, { notice: `Could not copy: ${error}` }));
  }

  discard(key: string) {
    this.cancel(key);
  }

  forgetOtherProfiles(profileId: string) {
    for (const dictation of useDictationStore.getState().dictations) {
      if (dictation.profileId !== profileId) this.discard(dictation.key);
    }
  }
}
