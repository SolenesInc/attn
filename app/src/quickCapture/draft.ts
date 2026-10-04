import { invoke } from '@tauri-apps/api/core';
import type { QuickCaptureDraft } from './client';

export type CachedQuickCaptureDraft = QuickCaptureDraft;

export function newQuickCaptureDraft(): QuickCaptureDraft {
  return { id: crypto.randomUUID(), text: '', mailbox: 'chief', files: [], uncertain: false };
}

const writesByProfile = new Map<string, Promise<void>>();

export function quickCaptureDraftCache(profileId: string) {
  const savedFiles = new Map<string, string>();
  return {
    async read(): Promise<QuickCaptureDraft | null> {
      await writesByProfile.get(profileId);
      const stored = await invoke<CachedQuickCaptureDraft | null>('quick_capture_draft_read', { profileId });
      if (!stored) return null;
      const { files, ...draft } = stored;
      for (const file of files) savedFiles.set(file.id, file.url);
      return { ...draft, files };
    },
    save(draft: QuickCaptureDraft): Promise<void> {
      const next = (writesByProfile.get(profileId) ?? Promise.resolve()).catch(() => {}).then(async () => {
        for (const file of draft.files) {
          if (savedFiles.get(file.id) === file.url) continue;
          await invoke('quick_capture_draft_file_write', { profileId, id: file.id, url: file.url });
          savedFiles.set(file.id, file.url);
        }
        const { files, ...metadata } = draft;
        await invoke('quick_capture_draft_write', { profileId, draft: { ...metadata, files: files.map(({ id, name }) => ({ id, name })) } });
        for (const id of savedFiles.keys()) if (!files.some(file => file.id === id)) savedFiles.delete(id);
      });
      writesByProfile.set(profileId, next);
      const finished = () => { if (writesByProfile.get(profileId) === next) writesByProfile.delete(profileId); };
      void next.then(finished, finished);
      return next;
    },
  };
}
