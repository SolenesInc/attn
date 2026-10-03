import { emitTo, listen } from '@tauri-apps/api/event';

export const CAPTURE_STATE = 'attn://capture/state';
export const CAPTURE_REQUEST = 'attn://capture/request';
export const CAPTURE_RESULT = 'attn://capture/result';
export const CAPTURE_READY = 'attn://capture/ready';
export const CAPTURE_FONT = 'attn:capture-font';
export const CAPTURE_SHORTCUT_SETTING = 'capture.shortcut';
export const DEFAULT_CAPTURE_SHORTCUT = 'Control+Alt+Space';

export interface DraftAttachment { id: string; name: string; url: string }
export interface CaptureDraft { id: string; text: string; recipient: string; files: DraftAttachment[]; uncertain: boolean }
export interface CaptureSubmission { id: string; text: string; recipient: string; fileIds: string[] }
export interface CaptureRecipient { id: string; name: string; detail: string }
export interface CaptureItem {
  id: string; text: string; recipient: string; createdAt: string; readAt?: string;
  files: { id: string; name: string; mediaType?: string; bytes: number }[];
}
export interface CaptureHostState {
  connected: boolean; recipients: CaptureRecipient[]; binding: string | null;
  activeBinding: string | null; shortcutError?: string; connectionError?: string; captureRevision?: number;
  fontScale?: number; keybindings?: string;
}
export interface CaptureDraftAsset { captureId: string; id: string; name: string; state: string }
export interface CaptureHistory { nextCursor?: string; items: CaptureItem[]; assets: CaptureDraftAsset[] }
export interface CaptureClient {
  stage(draft: CaptureDraft): Promise<void>;
  submit(submission: CaptureSubmission): Promise<CaptureItem>;
  resolve(id: string): Promise<CaptureItem | null>;
  recent(cursor?: string): Promise<CaptureHistory>;
  file(captureId: string, attachmentId: string, mediaType?: string): Promise<string>;
  discard(id: string, fileIds: string[]): Promise<void>;
  setBinding(binding: string | null): Promise<void>;
  resizeText(action: 'increase' | 'decrease' | 'reset'): Promise<void>;
}
export type CaptureRequest =
  | { id: string; action: 'submit'; submission: CaptureSubmission }
  | { id: string; action: 'stage'; draft: CaptureDraft }
  | { id: string; action: 'resolve'; captureId: string }
  | { id: string; action: 'recent'; cursor?: string }
  | { id: string; action: 'file'; captureId: string; attachmentId: string; mediaType?: string }
  | { id: string; action: 'discard'; captureId: string; fileIds: string[] }
  | { id: string; action: 'binding'; binding: string | null }
  | { id: string; action: 'font'; change: 'increase' | 'decrease' | 'reset' }
export interface CaptureResult { id: string; value?: unknown; error?: string }

export const EMPTY_HOST_STATE: CaptureHostState = {
  connected: false, recipients: [{ id: 'chief', name: 'Chief', detail: 'Chief of staff' }],
  binding: null, activeBinding: null,
};

export function createCaptureBridge(onState: (state: CaptureHostState) => void) {
  const pending = new Map<string, { resolve: (value: unknown) => void; reject: (error: Error) => void; stage: boolean }>();
  let connected = false;
  let disposed = false;
  const resultListener = listen<CaptureResult>(CAPTURE_RESULT, ({ payload }) => {
    const request = pending.get(payload.id);
    if (!request) return;
    pending.delete(payload.id);
    if (payload.error) request.reject(new Error(payload.error));
    else request.resolve(payload.value);
  });
  const stateListener = listen<CaptureHostState>(CAPTURE_STATE, ({ payload }) => {
    connected = payload.connected;
    onState(payload);
    if (!connected) {
      for (const [id, request] of pending) {
        if (request.stage) continue;
        request.reject(new Error(payload.connectionError || 'Disconnected. Your draft is retained.'));
        pending.delete(id);
      }
    }
  });
  const ready = Promise.all([resultListener, stateListener]).then(() => {
    if (!disposed) return emitTo('main', CAPTURE_READY);
  });
  async function request<T>(body: CaptureRequestBody): Promise<T> {
    await ready;
    if (!connected || disposed) throw new Error('Capture is connecting. Your draft is retained.');
    const id = crypto.randomUUID();
    return new Promise<T>((resolve, reject) => {
      pending.set(id, { resolve: value => resolve(value as T), reject, stage: body.action === 'stage' });
      void emitTo('main', CAPTURE_REQUEST, { ...body, id }).catch(error => {
        pending.delete(id); reject(error);
      });
    });
  }
  // Distributive Omit preserves the fields of each request variant.
  const call = <T>(body: CaptureRequestBody) => request<T>(body);
  const client: CaptureClient = {
    stage: draft => call({ action: 'stage', draft }),
    submit: submission => call({ action: 'submit', submission }),
    resolve: captureId => call({ action: 'resolve', captureId }),
    recent: cursor => call({ action: 'recent', cursor }),
    file: (captureId, attachmentId, mediaType) => call({ action: 'file', captureId, attachmentId, mediaType }),
    discard: (captureId, fileIds) => call({ action: 'discard', captureId, fileIds }),
    setBinding: binding => call({ action: 'binding', binding }),
    resizeText: change => call({ action: 'font', change }),
  };
  return { client, ready, refresh: () => emitTo('main', CAPTURE_READY), dispose() {
    disposed = true;
    for (const request of pending.values()) request.reject(new Error('Capture closed. Your draft is retained.'));
    pending.clear();
    void resultListener.then(unlisten => unlisten());
    void stateListener.then(unlisten => unlisten());
  } };
}
type CaptureRequestBody = CaptureRequest extends infer R ? R extends { id: string } ? Omit<R, 'id'> : never : never;
