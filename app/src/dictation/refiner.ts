import { useEffect, useRef } from 'react';
import { emitTo, listen } from '@tauri-apps/api/event';
import { DictationTextFormat, type DictationRefineResultMessage } from '../types/generated';
import type { DaemonApi } from '../contexts/DaemonApiContext';

export interface RefineRequest {
  requestId: string;
  profileId: string;
  rawText: string;
  recordingDurationMs: number;
  format: DictationTextFormat;
}

export type RefineOutcome = { ok: true; text: string } | { ok: false; error: string };

export interface Refiner {
  refine(request: RefineRequest): Promise<RefineOutcome>;
  cancel(requestId: string): void;
}

const REFINE_REQUEST = 'attn://dictation/refine';
const REFINE_CANCEL = 'attn://dictation/refine-cancel';
const REFINE_RESULT = 'attn://dictation/refined';

type Api = Pick<DaemonApi, 'sendDictationRefine' | 'sendDictationRefineCancel'>;

function outcome(result: DictationRefineResultMessage): RefineOutcome {
  if (result.success && typeof result.refined_text === 'string') return { ok: true, text: result.refined_text };
  return { ok: false, error: result.error || 'Refinement failed. Original kept.' };
}

export function daemonRefiner(api: Api): Refiner {
  return {
    refine: request => api.sendDictationRefine({
      request_id: request.requestId,
      profile_id: request.profileId,
      raw_text: request.rawText,
      recording_duration_ms: request.recordingDurationMs,
      format: request.format,
    }).then(outcome, error => ({ ok: false, error: error instanceof Error ? error.message : String(error) })),
    cancel: requestId => api.sendDictationRefineCancel(requestId),
  };
}

export function relayRefiner(): Refiner & { dispose(): void } {
  const waiting = new Map<string, (outcome: RefineOutcome) => void>();
  let results: Promise<() => void> | null = null;
  const subscribed = () => results ??= listen<{ requestId: string; outcome: RefineOutcome }>(REFINE_RESULT, ({ payload }) => {
    waiting.get(payload.requestId)?.(payload.outcome);
    waiting.delete(payload.requestId);
  });
  return {
    refine: request => new Promise(resolve => {
      waiting.set(request.requestId, resolve);
      void subscribed().then(() => emitTo('main', REFINE_REQUEST, request)).catch(error => {
        waiting.delete(request.requestId);
        resolve({ ok: false, error: `Refinement could not reach attn: ${error}. Original kept.` });
      });
    }),
    cancel: requestId => {
      waiting.delete(requestId);
      void emitTo('main', REFINE_CANCEL, requestId);
    },
    dispose: () => { void results?.then(stop => stop()); waiting.clear(); },
  };
}

export function useDictationRefineRelay(api: Api) {
  const current = useRef(daemonRefiner(api));
  useEffect(() => { current.current = daemonRefiner(api); }, [api.sendDictationRefine, api.sendDictationRefineCancel]);
  useEffect(() => {
    const requests = listen<RefineRequest>(REFINE_REQUEST, ({ payload, }) => {
      void current.current.refine(payload).then(result => emitTo('capture', REFINE_RESULT, { requestId: payload.requestId, outcome: result }));
    });
    const cancels = listen<string>(REFINE_CANCEL, ({ payload }) => current.current.cancel(payload));
    return () => { void requests.then(stop => stop()); void cancels.then(stop => stop()); };
  }, []);
}
