import { invoke } from '@tauri-apps/api/core';
import type { CaptureDraft } from './client';

export function newCaptureDraft(): CaptureDraft {
  return { id: crypto.randomUUID(), text: '', recipient: 'chief', images: [], uncertain: false };
}

// Native cache belongs to this instance. Images are written once; typing writes only draft metadata.
export function captureDraftCache() {
  let writes = Promise.resolve();
  const savedImages = new Map<string, string>();
  return {
    async read() {
      const draft = await invoke<CaptureDraft | null>('capture_draft_read');
      for (const image of draft?.images ?? []) savedImages.set(image.id, image.url);
      return draft;
    },
    save(draft: CaptureDraft): Promise<void> {
      const next = writes.catch(() => {}).then(async () => {
        for (const image of draft.images) {
          if (savedImages.get(image.id) === image.url) continue;
          await invoke('capture_draft_image_write', { id: image.id, url: image.url });
          savedImages.set(image.id, image.url);
        }
        await invoke('capture_draft_write', { draft: { ...draft, images: draft.images.map(({ id, name }) => ({ id, name })) } });
        for (const id of savedImages.keys()) if (!draft.images.some(image => image.id === id)) savedImages.delete(id);
      });
      writes = next;
      return next;
    },
  };
}
