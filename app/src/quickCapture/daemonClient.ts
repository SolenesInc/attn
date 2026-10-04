import { UserMessageRequestError } from '../hooks/daemonUserMessageEvents';
import type { DaemonApi } from '../contexts/DaemonApiContext';
import {
  UserMessageAttachmentDiscardMessageCmd, UserMessageAttachmentGetMessageCmd, UserMessageAttachmentPutMessageCmd,
  UserMessageGetMessageCmd, UserMessageListMessageCmd, UserMessageSendMessageCmd,
  UserMessageTargetKind, type UserMessageRecord, type UserMessageResultObject,
} from '../types/generated';
import type { UserMessageClient, UserMessageItem } from './client';

const UPLOAD_CHUNK_BYTES = 524288;

function target(recipient: string) {
  return recipient === 'chief' ? { kind: UserMessageTargetKind.Chief }
    : { kind: UserMessageTargetKind.Crew, member_id: recipient };
}
function item(record: UserMessageRecord): UserMessageItem {
  return { id: record.id, text: record.content, recipient: record.target.kind === 'chief' ? 'chief' : record.target.member_id!,
    createdAt: record.created_at, readAt: record.read_at, files: record.attachments.map(({ id, name, media_type, bytes }) => ({ id, name, mediaType: media_type, bytes })) };
}
function saved(result: UserMessageResultObject) {
  if (!result.record) throw new Error('The daemon did not return a saved user message receipt. Your draft is retained.');
  return item(result.record);
}
function attachmentBytes(url: string) {
  const start = url.indexOf(',') + 1;
  const length = (url.length - start) / 4 * 3 - (url.endsWith('==') ? 2 : url.endsWith('=') ? 1 : 0);
  return { length, chunk(offset: number) {
    const end = Math.min(offset + UPLOAD_CHUNK_BYTES, length);
    const decoded = atob(url.slice(start + Math.floor(offset / 3) * 4, start + Math.ceil(end / 3) * 4));
    return { end, data: btoa(decoded.slice(offset % 3, offset % 3 + end - offset)) };
  } };
}

export function userMessageDaemonClient(daemon: Pick<DaemonApi, 'sendUserMessageRequest'>, profileId: string): UserMessageClient {
  const request: DaemonApi['sendUserMessageRequest'] = command => daemon.sendUserMessageRequest({ ...command, profile_id: profileId });
  const uploading = new Map<string, Promise<void>>();
  const ready = new Set<string>();
  const key = (messageId: string, fileId: string) => `${messageId}:${fileId}`;
  async function stage(draft: Parameters<UserMessageClient['stage']>[0]) {
    if (draft.files.every(file => ready.has(key(draft.id, file.id)))) return;
    let history: Promise<UserMessageResultObject> | undefined;
    const pending: Promise<void>[] = [];
    for (const file of draft.files) {
      const identity = key(draft.id, file.id);
      if (ready.has(identity)) continue;
      let upload = uploading.get(identity);
      if (!upload) {
        history ??= request({ cmd: UserMessageListMessageCmd.UserMessageList, limit: 1 });
        const reconciliation = history;
        upload = (async () => {
          const assets = (await reconciliation).list?.draft_assets ?? [];
          const asset = assets.find(asset => asset.message_id === draft.id && asset.attachment_id === file.id);
          if (asset?.state === 'ready') { ready.add(identity); return; }
          const bytes = attachmentBytes(file.url);
          let offset = asset?.next_offset ?? 0;
          do {
            const chunk = bytes.chunk(offset);
            const result = await request({ cmd: UserMessageAttachmentPutMessageCmd.UserMessageAttachmentPut,
              message_id: draft.id, attachment_id: file.id, name: file.name,
              offset, data_base64: chunk.data, final: chunk.end === bytes.length });
            if (!result.upload) throw new Error(`No upload receipt for ${file.name}. Your draft is retained.`);
            offset = result.upload.next_offset;
          } while (offset < bytes.length);
          ready.add(identity);
        })();
        uploading.set(identity, upload);
        void upload.then(() => uploading.delete(identity), () => uploading.delete(identity));
      }
      pending.push(upload);
    }
    await Promise.all(pending);
  }
  return {
    stage,
    async submit(draft) {
      const accepted = saved(await request({ cmd: UserMessageSendMessageCmd.UserMessageSend, message_id: draft.id,
        target: target(draft.recipient), content: draft.text, attachment_ids: draft.fileIds }));
      for (const fileId of draft.fileIds) ready.delete(key(draft.id, fileId));
      return accepted;
    },
    async resolve(messageId) {
      try {
        const result = await request({ cmd: UserMessageGetMessageCmd.UserMessageGet, message_id: messageId });
        if (!result.record) return null;
        for (const identity of ready) if (identity.startsWith(`${messageId}:`)) ready.delete(identity);
        return item(result.record);
      } catch (error) {
        if (error instanceof UserMessageRequestError && error.code === 'user_message_not_found') return null;
        throw error;
      }
    },
    async recent(cursor) {
      const result = await request({ cmd: UserMessageListMessageCmd.UserMessageList, limit: 4, ...(cursor && { cursor }) });
      if (!result.list) throw new Error('The daemon did not return user message history.');
      return { nextCursor: result.list.next_cursor, items: result.list.items.map(item), assets: result.list.draft_assets.map(asset => ({
        messageId: asset.message_id, id: asset.attachment_id, name: asset.name, state: asset.state,
      })) };
    },
    async discard(messageId, fileIds) {
      for (const attachmentId of fileIds) {
        const identity = key(messageId, attachmentId);
        await uploading.get(identity)?.catch(() => {});
        ready.delete(identity);
        await request({ cmd: UserMessageAttachmentDiscardMessageCmd.UserMessageAttachmentDiscard,
          message_id: messageId, attachment_id: attachmentId });
      }
    },
    async file(messageId, attachmentId, mediaType) {
      const pieces: Uint8Array[] = [];
      let offset = 0;
      for (;;) {
        const result = await request({ cmd: UserMessageAttachmentGetMessageCmd.UserMessageAttachmentGet,
          message_id: messageId, attachment_id: attachmentId, offset });
        if (!result.download) throw new Error('The daemon did not return file bytes.');
        pieces.push(Uint8Array.from(atob(result.download.data_base64), character => character.charCodeAt(0)));
        if (result.download.eof) return new Promise<string>((resolve, reject) => {
          const reader = new FileReader(); reader.onload = () => resolve(String(reader.result));
          reader.onerror = () => reject(reader.error);
          reader.readAsDataURL(new Blob(pieces, { type: mediaType ?? 'application/octet-stream' }));
        });
        offset = result.download.next_offset;
      }
    },
    setBinding: async () => { throw new Error('Shortcut preferences belong to the native host.'); },
    resizeText: async () => { throw new Error('Text size preferences belong to the main app.'); },
  };
}
