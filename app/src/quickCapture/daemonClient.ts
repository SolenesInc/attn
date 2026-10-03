import { CaptureRequestError } from '../hooks/daemonCaptureEvents';
import type { DaemonApi } from '../contexts/DaemonApiContext';
import {
  CaptureAttachmentDiscardMessageCmd, CaptureAttachmentGetMessageCmd, CaptureAttachmentPutMessageCmd,
  CaptureGetMessageCmd, CaptureListMessageCmd, CaptureSendMessageCmd,
  CaptureTargetKind, type CaptureRecord, type CaptureResultObject,
} from '../types/generated';
import type { CaptureClient, CaptureItem } from './client';

// 524288 raw bytes encode to 699309 bytes in the measured WebSocket request; see docs/context/quick-capture.md.
const UPLOAD_CHUNK_BYTES = 524288;

function target(recipient: string) {
  return recipient === 'chief' ? { kind: CaptureTargetKind.Chief }
    : { kind: CaptureTargetKind.Crew, member_id: recipient };
}
function item(record: CaptureRecord): CaptureItem {
  return { id: record.id, text: record.content, recipient: record.target.kind === 'chief' ? 'chief' : record.target.member_id!,
    createdAt: record.created_at, readAt: record.read_at, images: record.attachments.map(({ id, name, media_type }) => ({ id, name, mediaType: media_type })) };
}
function saved(result: CaptureResultObject) {
  if (!result.record) throw new Error('The daemon did not return a saved capture receipt. Your draft is retained.');
  return item(result.record);
}
function encode(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

export function captureDaemonClient(daemon: Pick<DaemonApi, 'sendCaptureRequest'>): CaptureClient {
  const request = daemon.sendCaptureRequest;
  const uploading = new Map<string, Promise<void>>();
  const ready = new Set<string>();
  const key = (captureId: string, imageId: string) => `${captureId}:${imageId}`;
  async function stage(draft: Parameters<CaptureClient['stage']>[0]) {
    if (draft.images.every(image => ready.has(key(draft.id, image.id)))) return;
    let history: Promise<CaptureResultObject> | undefined;
    const pending: Promise<void>[] = [];
    for (const image of draft.images) {
      const identity = key(draft.id, image.id);
      if (ready.has(identity)) continue;
      let upload = uploading.get(identity);
      if (!upload) {
        history ??= request({ cmd: CaptureListMessageCmd.CaptureList, limit: 1 });
        const reconciliation = history;
        upload = (async () => {
          const assets = (await reconciliation).list?.draft_assets ?? [];
          const asset = assets.find(asset => asset.capture_id === draft.id && asset.attachment_id === image.id);
          if (asset?.state === 'ready') { ready.add(identity); return; }
          const bytes = new Uint8Array(await (await fetch(image.url)).arrayBuffer());
          let offset = asset?.next_offset ?? 0;
          do {
            const chunk = bytes.subarray(offset, offset + UPLOAD_CHUNK_BYTES);
            const result = await request({ cmd: CaptureAttachmentPutMessageCmd.CaptureAttachmentPut,
              capture_id: draft.id, attachment_id: image.id, name: image.name,
              offset, data_base64: encode(chunk), final: offset + chunk.length === bytes.length });
            if (!result.upload) throw new Error(`No upload receipt for ${image.name}. Your draft is retained.`);
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
      const accepted = saved(await request({ cmd: CaptureSendMessageCmd.CaptureSend, capture_id: draft.id,
        target: target(draft.recipient), content: draft.text, attachment_ids: draft.imageIds }));
      for (const imageId of draft.imageIds) ready.delete(key(draft.id, imageId));
      return accepted;
    },
    async resolve(captureId) {
      try {
        const result = await request({ cmd: CaptureGetMessageCmd.CaptureGet, capture_id: captureId });
        if (!result.record) return null;
        for (const identity of ready) if (identity.startsWith(`${captureId}:`)) ready.delete(identity);
        return item(result.record);
      } catch (error) {
        if (error instanceof CaptureRequestError && error.code === 'capture_not_found') return null;
        throw error;
      }
    },
    async recent(cursor) {
      // A page is four notes in the fixed 390px composer; older notes remain available through the cursor.
      const result = await request({ cmd: CaptureListMessageCmd.CaptureList, limit: 4, ...(cursor && { cursor }) });
      if (!result.list) throw new Error('The daemon did not return capture history.');
      return { nextCursor: result.list.next_cursor, items: result.list.items.map(item), assets: result.list.draft_assets.map(asset => ({
        captureId: asset.capture_id, id: asset.attachment_id, name: asset.name, state: asset.state,
      })) };
    },
    async discard(captureId, imageIds) {
      for (const attachmentId of imageIds) {
        const identity = key(captureId, attachmentId);
        await uploading.get(identity)?.catch(() => {});
        ready.delete(identity);
        await request({ cmd: CaptureAttachmentDiscardMessageCmd.CaptureAttachmentDiscard,
          capture_id: captureId, attachment_id: attachmentId });
      }
    },
    async image(captureId, attachmentId, mediaType) {
      const pieces: Uint8Array[] = [];
      let offset = 0;
      for (;;) {
        const result = await request({ cmd: CaptureAttachmentGetMessageCmd.CaptureAttachmentGet,
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
    setBinding: async () => { throw new Error('Shortcut preferences belong to the native host.'); },
    resizeText: async () => { throw new Error('Text size preferences belong to the main app.'); },
  };
}
