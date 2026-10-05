import { QuickCaptureRequestError } from '../hooks/daemonQuickCaptureEvents';
import type { DaemonApi } from '../contexts/DaemonApiContext';
import {
  QuickCaptureAttachmentDiscardMessageCmd, QuickCaptureAttachmentGetMessageCmd, QuickCaptureAttachmentPutMessageCmd,
  QuickCaptureGetMessageCmd, QuickCaptureListMessageCmd, QuickCaptureSendMessageCmd,
  QuickCaptureMailboxKind, type QuickCaptureRecord, type QuickCaptureResultObject,
} from '../types/generated';
import type { QuickCaptureDeliveryClient, QuickCaptureItem } from './client';

const UPLOAD_CHUNK_BYTES = 524288;

function wireMailbox(mailbox: string) {
  return mailbox === 'chief' ? { kind: QuickCaptureMailboxKind.Chief }
    : { kind: QuickCaptureMailboxKind.CrewMember, member_id: mailbox };
}
function item(record: QuickCaptureRecord): QuickCaptureItem {
  return { id: record.id, text: record.content, mailbox: record.mailbox.kind === 'chief' ? 'chief' : record.mailbox.member_id!,
    createdAt: record.created_at, readAt: record.read_at, files: record.attachments.map(({ id, name, media_type, bytes }) => ({ id, name, mediaType: media_type, bytes })) };
}
function saved(result: QuickCaptureResultObject) {
  if (!result.record) throw new Error('The daemon did not return a saved quick capture receipt. Your draft is retained.');
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

export function quickCaptureDaemonClient(daemon: Pick<DaemonApi, 'sendQuickCaptureRequest'>, profileId: string): QuickCaptureDeliveryClient {
  const request: DaemonApi['sendQuickCaptureRequest'] = command => daemon.sendQuickCaptureRequest({ ...command, profile_id: profileId });
  const uploading = new Map<string, Promise<void>>();
  const ready = new Set<string>();
  const key = (captureId: string, fileId: string) => `${captureId}:${fileId}`;
  async function stage(draft: Parameters<QuickCaptureDeliveryClient['stage']>[0]) {
    if (draft.files.every(file => ready.has(key(draft.id, file.id)))) return;
    let history: Promise<QuickCaptureResultObject> | undefined;
    const pending: Promise<void>[] = [];
    for (const file of draft.files) {
      const identity = key(draft.id, file.id);
      if (ready.has(identity)) continue;
      let upload = uploading.get(identity);
      if (!upload) {
        history ??= request({ cmd: QuickCaptureListMessageCmd.QuickCaptureList, limit: 1 });
        const reconciliation = history;
        upload = (async () => {
          const assets = (await reconciliation).list?.draft_assets ?? [];
          const asset = assets.find(asset => asset.capture_id === draft.id && asset.attachment_id === file.id);
          if (asset?.state === 'ready') { ready.add(identity); return; }
          const bytes = attachmentBytes(file.url);
          let offset = asset?.next_offset ?? 0;
          do {
            const chunk = bytes.chunk(offset);
            const result = await request({ cmd: QuickCaptureAttachmentPutMessageCmd.QuickCaptureAttachmentPut,
              capture_id: draft.id, attachment_id: file.id, name: file.name,
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
      const accepted = saved(await request({ cmd: QuickCaptureSendMessageCmd.QuickCaptureSend, capture_id: draft.id,
        mailbox: wireMailbox(draft.mailbox), content: draft.text, attachment_ids: draft.fileIds }));
      for (const fileId of draft.fileIds) ready.delete(key(draft.id, fileId));
      return accepted;
    },
    async resolve(captureId) {
      try {
        const result = await request({ cmd: QuickCaptureGetMessageCmd.QuickCaptureGet, capture_id: captureId });
        if (!result.record) return null;
        for (const identity of ready) if (identity.startsWith(`${captureId}:`)) ready.delete(identity);
        return item(result.record);
      } catch (error) {
        if (error instanceof QuickCaptureRequestError && error.code === 'quick_capture_not_found') return null;
        throw error;
      }
    },
    async recent(cursor) {
      const result = await request({ cmd: QuickCaptureListMessageCmd.QuickCaptureList, limit: 4, ...(cursor && { cursor }) });
      if (!result.list) throw new Error('The daemon did not return quick capture history.');
      return { nextCursor: result.list.next_cursor, items: result.list.items.map(item), assets: result.list.draft_assets.map(asset => ({
        captureId: asset.capture_id, id: asset.attachment_id, name: asset.name, state: asset.state,
      })) };
    },
    async discard(captureId, fileIds) {
      for (const attachmentId of fileIds) {
        const identity = key(captureId, attachmentId);
        await uploading.get(identity)?.catch(() => {});
        ready.delete(identity);
        await request({ cmd: QuickCaptureAttachmentDiscardMessageCmd.QuickCaptureAttachmentDiscard,
          capture_id: captureId, attachment_id: attachmentId });
      }
    },
    async file(captureId, attachmentId, mediaType) {
      const pieces: Uint8Array[] = [];
      let offset = 0;
      for (;;) {
        const result = await request({ cmd: QuickCaptureAttachmentGetMessageCmd.QuickCaptureAttachmentGet,
          capture_id: captureId, attachment_id: attachmentId, offset });
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
  };
}
