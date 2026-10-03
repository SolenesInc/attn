import { invoke } from '@tauri-apps/api/core';
import type { CaptureDraft, DraftAttachment } from './client';

export type CachedCaptureDraft = Omit<CaptureDraft, 'files'> & { images: DraftAttachment[] };

export function newCaptureDraft(): CaptureDraft {
  return { id: crypto.randomUUID(), text: '', recipient: 'chief', files: [], uncertain: false };
}

// The native cache keeps its existing images field and commands so retained drafts survive upgrades.
export function captureDraftCache() {
  let writes = Promise.resolve();
  const savedFiles = new Map<string, string>();
  return {
    async read(): Promise<CaptureDraft | null> {
      const stored = await invoke<CachedCaptureDraft | null>('capture_draft_read');
      if (!stored) return null;
      const { images: files, ...draft } = stored;
      for (const file of files) savedFiles.set(file.id, file.url);
      return { ...draft, files };
    },
    save(draft: CaptureDraft): Promise<void> {
      const next = writes.catch(() => {}).then(async () => {
        for (const file of draft.files) {
          if (savedFiles.get(file.id) === file.url) continue;
          await invoke('capture_draft_image_write', { id: file.id, url: file.url });
          savedFiles.set(file.id, file.url);
        }
        const { files, ...metadata } = draft;
        await invoke('capture_draft_write', { draft: { ...metadata, images: files.map(({ id, name }) => ({ id, name })) } });
        for (const id of savedFiles.keys()) if (!files.some(file => file.id === id)) savedFiles.delete(id);
      });
      writes = next;
      return next;
    },
  };
}
