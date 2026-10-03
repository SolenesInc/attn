import { emitTo, listen } from '@tauri-apps/api/event';

export const CAPTURE_STATE = 'attn://capture/state';
export const CAPTURE_REQUEST = 'attn://capture/request';
export const CAPTURE_RESULT = 'attn://capture/result';
export const CAPTURE_READY = 'attn://capture/ready';
export const CAPTURE_FONT = 'attn:capture-font';
export const CAPTURE_SHORTCUT_SETTING = 'capture.shortcut';
export const DEFAULT_CAPTURE_SHORTCUT = 'Control+Alt+Space';

export interface DraftImage { id: string; name: string; url: string }
export interface CaptureDraft { id: string; text: string; recipient: string; images: DraftImage[]; uncertain: boolean }
export interface CaptureSubmission { id: string; text: string; recipient: string; imageIds: string[] }
export interface CaptureRecipient { id: string; name: string; detail: string }
export interface CaptureItem {
  id: string; text: string; recipient: string; state: string; createdAt: string;
  images: { id: string; name: string; mediaType?: string }[]; sessionId?: string; detail?: string;
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
  image(captureId: string, attachmentId: string, mediaType?: string): Promise<string>;
  update(id: string, action: 'cancel' | 'restore' | 'retry' | 'redirect', recipient?: string): Promise<CaptureItem>;
  discard(id: string, imageIds: string[]): Promise<void>;
  setBinding(binding: string | null): Promise<void>;
  resizeText(action: 'increase' | 'decrease' | 'reset'): Promise<void>;
  openRecipient(sessionId: string): Promise<void>;
}
export type CaptureRequest =
  | { id: string; action: 'submit'; submission: CaptureSubmission }
  | { id: string; action: 'stage'; draft: CaptureDraft }
  | { id: string; action: 'resolve'; captureId: string }
  | { id: string; action: 'recent'; cursor?: string }
  | { id: string; action: 'image'; captureId: string; attachmentId: string; mediaType?: string }
  | { id: string; action: 'update'; captureId: string; update: 'cancel' | 'restore' | 'retry' | 'redirect'; recipient?: string }
  | { id: string; action: 'discard'; captureId: string; imageIds: string[] }
  | { id: string; action: 'binding'; binding: string | null }
  | { id: string; action: 'font'; change: 'increase' | 'decrease' | 'reset' }
  | { id: string; action: 'open'; sessionId: string };
export interface CaptureResult { id: string; value?: unknown; error?: string }

export const EMPTY_HOST_STATE: CaptureHostState = {
  connected: false, recipients: [{ id: 'chief', name: 'Chief', detail: 'Chief of staff' }],
  binding: null, activeBinding: null,
};

export function createCaptureBridge(onState: (state: CaptureHostState) => void) {
  const pending = new Map<string, { resolve: (value: unknown) => void; reject: (error: Error) => void }>();
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
      for (const request of pending.values()) request.reject(new Error(payload.connectionError || 'Disconnected. Your draft is retained.'));
      pending.clear();
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
      pending.set(id, { resolve: value => resolve(value as T), reject });
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
    image: (captureId, attachmentId, mediaType) => call({ action: 'image', captureId, attachmentId, mediaType }),
    update: (captureId, update, recipient) => call({ action: 'update', captureId, update, recipient }),
    discard: (captureId, imageIds) => call({ action: 'discard', captureId, imageIds }),
    setBinding: binding => call({ action: 'binding', binding }),
    resizeText: change => call({ action: 'font', change }),
    openRecipient: sessionId => call({ action: 'open', sessionId }),
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
