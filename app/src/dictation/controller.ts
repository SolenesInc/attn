import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { create } from 'zustand';
import { afterComposition, insertAtCursor, missingTargetReason, TrackedField, type Range } from './field';
import { writableField } from '../utils/textFields';
import type { DictationInteraction } from './settings';
import type { Refiner } from './refiner';

export type DictationPhase = 'starting' | 'recording' | 'transcribing' | 'refining' | 'settled';

export interface Dictation {
  key: string;
  recording: number | null;
  profileId: string;
  mode: DictationInteraction;
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

interface Target {
  field: TrackedField;
  range: Range;
}

export interface NativeDictation {
  start(): Promise<{ recording: number }>;
  stop(recording: number): Promise<unknown>;
  cancel(recording: number | null): Promise<unknown>;
  subscribe(onRequest: () => void, onUpdate: (update: NativeUpdate) => void): () => void;
  openMicrophoneSettings(): void;
}

const MICROPHONE_SETTINGS = 'x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone';

export const tauriDictation: NativeDictation = {
  start: () => invoke<{ recording: number }>('dictation_start'),
  stop: recording => invoke('dictation_stop', { recording }),
  cancel: recording => invoke('dictation_cancel', { recording }),
  subscribe(onRequest, onUpdate) {
    const request = listen(DICTATION_REQUEST_EVENT, () => onRequest());
    const updates = listen<NativeUpdate>(DICTATION_EVENT, ({ payload }) => onUpdate(payload));
    return () => { void request.then(stop => stop()); void updates.then(stop => stop()); };
  },
  openMicrophoneSettings: () => { void import('@tauri-apps/plugin-opener').then(({ openUrl }) => openUrl(MICROPHONE_SETTINGS)); },
};

export interface DictationContext {
  profileId: () => string;
  interaction: () => DictationInteraction;
  refiner: () => Refiner;
}

export class DictationController {
  private targets = new Map<string, Target>();
  private requests = new Map<string, string>();
  private early = new Map<number, NativeUpdate[]>();
  private stops: (() => void)[] = [];
  private sequence = 0;

  constructor(private readonly context: DictationContext, private readonly native: NativeDictation = tauriDictation) {}

  start() {
    this.stops.push(this.native.subscribe(() => this.begin(), update => this.receive(update)));
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
    if (target) target.field.release(target.range);
    this.targets.delete(key);
    const requestId = this.requests.get(key);
    if (requestId) this.context.refiner().cancel(requestId);
    this.requests.delete(key);
    useDictationStore.setState(state => ({ dictations: state.dictations.filter(item => item.key !== key) }));
  }

  private notice(message: string, extra: Partial<Dictation> = {}) {
    const key = `notice-${++this.sequence}`;
    this.add({ key, recording: null, profileId: this.context.profileId(), mode: this.context.interaction(), phase: 'settled', rawInField: false, card: null, notice: message, ...extra });
    if (!extra.microphoneDenied) setTimeout(() => this.remove(key), NOTICE_MS);
  }

  begin() {
    if (useDictationStore.getState().dictations.some(item => item.phase === 'starting' || item.phase === 'recording')) return;
    const focused = document.activeElement;
    const element = writableField(focused);
    if (!element) { this.notice(missingTargetReason(focused)); return; }
    const key = `dictation-${++this.sequence}`;
    const field = new TrackedField(element);
    const range = field.track(element.selectionStart ?? element.value.length, element.selectionEnd ?? element.value.length);
    this.targets.set(key, { field, range });
    this.add({ key, recording: null, profileId: this.context.profileId(), mode: this.context.interaction(), phase: 'starting', rawInField: false, card: null });
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
    if (dictation.mode === 'compareFirst') {
      this.patch(dictation.key, { raw, audioMs: update.audioMs, card: 'compare', notice: undefined });
      this.paintReceipt(update);
    } else {
      await afterComposition();
      const target = this.targets.get(dictation.key);
      const inserted = target?.field.replace(target.range, raw) === 'inserted';
      this.patch(dictation.key, inserted ? { raw, audioMs: update.audioMs, rawInField: true, notice: undefined } : { raw, audioMs: update.audioMs, card: 'offer', notice: 'The field changed while you spoke, so nothing was inserted.' });
      this.paintReceipt(update);
    }
    this.refine(dictation.key, raw, update.audioMs);
  }

  private paintReceipt(update: Extract<NativeUpdate, { kind: 'transcript' }>) {
    const { timeline } = update;
    requestAnimationFrame(() => setTimeout(() => {
      const painted = Date.now();
      const receipt: DictationReceipt = {
        recording: update.recording,
        audioMs: update.audioMs,
        reason: update.reason,
        pressToListeningMs: timeline.pressed != null && timeline.firstAudio != null ? timeline.firstAudio - timeline.pressed : null,
        stopToTranscriptMs: timeline.stopped != null && timeline.transcribed != null ? timeline.transcribed - timeline.stopped : null,
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
    void this.context.refiner().refine({ requestId, profileId: this.find(key)?.profileId ?? '', rawText: raw, recordingDurationMs: audioMs }).then(outcome => {
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

  private refined(dictation: Dictation, refined: string) {
    if (dictation.card || !dictation.rawInField) { this.patch(dictation.key, { phase: 'settled', refined }); return; }
    const target = this.targets.get(dictation.key);
    if (refined === dictation.raw) { this.remove(dictation.key); return; }
    if (target?.field.replace(target.range, refined) === 'inserted') { this.remove(dictation.key); return; }
    this.patch(dictation.key, { phase: 'settled', refined, card: 'offer', notice: 'You edited the dictated text, so the refined version was not applied.' });
  }

  choose(key: string, text: string) {
    const dictation = this.find(key);
    const target = this.targets.get(key);
    if (!dictation) return;
    const tracked = !dictation.rawInField && target ? target.field.replace(target.range, text) : 'targetGone';
    const outcome = tracked === 'inserted' ? tracked : insertAtCursor(text);
    if (outcome === 'inserted') { this.remove(key); return; }
    this.patch(key, { notice: outcome === 'targetGone' ? 'Click into a text field to insert, or copy the text.' : 'The field changed. Click where the text should go, or copy it.' });
    if (outcome !== 'targetGone' && target && !dictation.rawInField) { target.field.release(target.range); this.targets.delete(key); }
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
