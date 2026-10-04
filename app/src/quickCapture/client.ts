import { emitTo, listen } from '@tauri-apps/api/event';

export const QUICK_CAPTURE_STATE = 'attn://capture/state';
export const QUICK_CAPTURE_REQUEST = 'attn://capture/request';
export const QUICK_CAPTURE_RESULT = 'attn://capture/result';
export const QUICK_CAPTURE_READY = 'attn://capture/ready';
export const QUICK_CAPTURE_FONT = 'attn:capture-font';
export const QUICK_CAPTURE_SHORTCUT_SETTING = 'capture.shortcut';
export const DEFAULT_QUICK_CAPTURE_SHORTCUT = 'Control+Alt+Space';

export interface DraftAttachment { id: string; name: string; url: string }
export interface UserMessageDraft { id: string; text: string; recipient: string; files: DraftAttachment[]; uncertain: boolean }
export interface UserMessageSubmission { id: string; text: string; recipient: string; fileIds: string[] }
export interface UserMessageRecipient { id: string; name: string; detail: string }
export interface UserMessageItem {
  id: string; text: string; recipient: string; createdAt: string; readAt?: string;
  files: { id: string; name: string; mediaType?: string; bytes: number }[];
}
export interface QuickCaptureHostState {
  profileId: string; connected: boolean; recipients: UserMessageRecipient[]; binding: string | null;
  activeBinding: string | null; shortcutError?: string; connectionError?: string; messageRevision?: number;
  fontScale?: number; keybindings?: string;
}
export interface UserMessageDraftAsset { messageId: string; id: string; name: string; state: string }
export interface UserMessageHistory { nextCursor?: string; items: UserMessageItem[]; assets: UserMessageDraftAsset[] }
export interface UserMessageClient {
  stage(draft: UserMessageDraft): Promise<void>;
  submit(submission: UserMessageSubmission): Promise<UserMessageItem>;
  resolve(id: string): Promise<UserMessageItem | null>;
  recent(cursor?: string): Promise<UserMessageHistory>;
  file(messageId: string, attachmentId: string, mediaType?: string): Promise<string>;
  discard(id: string, fileIds: string[]): Promise<void>;
  setBinding(binding: string | null): Promise<void>;
  resizeText(action: 'increase' | 'decrease' | 'reset'): Promise<void>;
}
export type QuickCaptureRequest = { profileId: string } & (
  | { id: string; action: 'submit'; submission: UserMessageSubmission }
  | { id: string; action: 'stage'; draft: UserMessageDraft }
  | { id: string; action: 'resolve'; messageId: string }
  | { id: string; action: 'recent'; cursor?: string }
  | { id: string; action: 'file'; messageId: string; attachmentId: string; mediaType?: string }
  | { id: string; action: 'discard'; messageId: string; fileIds: string[] }
  | { id: string; action: 'binding'; binding: string | null }
  | { id: string; action: 'font'; change: 'increase' | 'decrease' | 'reset' });
export interface QuickCaptureResult { id: string; value?: unknown; error?: string }

export const EMPTY_HOST_STATE: QuickCaptureHostState = {
  profileId: '', connected: false, recipients: [{ id: 'chief', name: 'Chief', detail: 'Chief of staff' }],
  binding: null, activeBinding: null,
};

export function createQuickCaptureBridge(onState: (state: QuickCaptureHostState) => void) {
  const pending = new Map<string, { resolve: (value: unknown) => void; reject: (error: Error) => void; stage: boolean }>();
  let connected = false;
  let disposed = false;
  const resultListener = listen<QuickCaptureResult>(QUICK_CAPTURE_RESULT, ({ payload }) => {
    const request = pending.get(payload.id);
    if (!request) return;
    pending.delete(payload.id);
    if (payload.error) request.reject(new Error(payload.error));
    else request.resolve(payload.value);
  });
  const stateListener = listen<QuickCaptureHostState>(QUICK_CAPTURE_STATE, ({ payload }) => {
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
    if (!disposed) return emitTo('main', QUICK_CAPTURE_READY);
  });
  async function request<T>(profileId: string, body: QuickCaptureRequestBody): Promise<T> {
    await ready;
    if (!connected || disposed) throw new Error('Capture is connecting. Your draft is retained.');
    const id = crypto.randomUUID();
    return new Promise<T>((resolve, reject) => {
      pending.set(id, { resolve: value => resolve(value as T), reject, stage: body.action === 'stage' });
      void emitTo('main', QUICK_CAPTURE_REQUEST, { ...body, id, profileId }).catch(error => {
        pending.delete(id); reject(error);
      });
    });
  }
  function forProfile(profileId: string): UserMessageClient {
    const call = <T>(body: QuickCaptureRequestBody) => request<T>(profileId, body);
    return {
      stage: draft => call({ action: 'stage', draft }),
      submit: submission => call({ action: 'submit', submission }),
      resolve: messageId => call({ action: 'resolve', messageId }),
      recent: cursor => call({ action: 'recent', cursor }),
      file: (messageId, attachmentId, mediaType) => call({ action: 'file', messageId, attachmentId, mediaType }),
      discard: (messageId, fileIds) => call({ action: 'discard', messageId, fileIds }),
      setBinding: binding => call({ action: 'binding', binding }),
      resizeText: change => call({ action: 'font', change }),
    };
  }
  return { forProfile, ready, refresh: () => emitTo('main', QUICK_CAPTURE_READY), dispose() {
    disposed = true;
    for (const request of pending.values()) request.reject(new Error('Capture closed. Your draft is retained.'));
    pending.clear();
    void resultListener.then(unlisten => unlisten());
    void stateListener.then(unlisten => unlisten());
  } };
}
type QuickCaptureRequestBody = QuickCaptureRequest extends infer R ? R extends { id: string } ? Omit<R, 'id' | 'profileId'> : never : never;
