import { invoke } from '@tauri-apps/api/core';
import { QUICK_CAPTURE_CHUNK_BYTES, type QuickCaptureDraft, type StoredDraftFile } from './client';

export type CachedQuickCaptureDraft = QuickCaptureDraft;

export function newQuickCaptureDraft(): QuickCaptureDraft {
  return { id: crypto.randomUUID(), text: '', mailbox: 'chief', files: [], uncertain: false };
}

const writesByProfile = new Map<string, Promise<void>>();

export function quickCaptureDraftCache(profileId: string) {
  let pending: QuickCaptureDraft | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let firstEditAt = 0;
  const cache = {
    async read(): Promise<QuickCaptureDraft | null> {
      await writesByProfile.get(profileId);
      return invoke<CachedQuickCaptureDraft | null>('quick_capture_draft_read', { profileId });
    },
    save(draft: QuickCaptureDraft): Promise<void> {
      clearTimeout(timer);
      timer = undefined;
      pending = undefined;
      const { files, ...metadata } = draft;
      const next = (writesByProfile.get(profileId) ?? Promise.resolve()).catch(() => {}).then(() =>
        invoke<void>('quick_capture_draft_write', { profileId, draft: { ...metadata, files: files.map(({ id, name }) => ({ id, name })) } }));
      writesByProfile.set(profileId, next);
      const finished = () => { if (writesByProfile.get(profileId) === next) writesByProfile.delete(profileId); };
      void next.then(finished, finished);
      return next;
    },
    schedule(draft: QuickCaptureDraft, onError: (error: unknown) => void) {
      if (!pending) firstEditAt = Date.now();
      pending = draft;
      clearTimeout(timer);
      timer = setTimeout(() => { void cache.flush().catch(onError); }, Math.min(1000, Math.max(0, 5000 - (Date.now() - firstEditAt))));
    },
    flush(): Promise<void> {
      return pending ? cache.save(pending) : writesByProfile.get(profileId) ?? Promise.resolve();
    },
    copy(id: string, path: string): Promise<StoredDraftFile> {
      return invoke('quick_capture_draft_file_copy', { profileId, id, path });
    },
    async write(id: string, file: File): Promise<StoredDraftFile> {
      let offset = 0;
      do {
        const chunk = new Uint8Array(await file.slice(offset, offset + QUICK_CAPTURE_CHUNK_BYTES).arrayBuffer());
        await invoke('quick_capture_draft_file_append', chunk, { headers: { 'profile-id': profileId, 'file-id': id, offset: String(offset) } });
        offset += chunk.length;
      } while (offset < file.size);
      return invoke('quick_capture_draft_file_finish', { profileId, id, name: file.name });
    },
    pasteImage(id: string): Promise<StoredDraftFile> {
      return invoke('quick_capture_draft_clipboard_image', { profileId, id });
    },
  };
  return cache;
}
