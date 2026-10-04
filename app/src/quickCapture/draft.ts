import { invoke } from '@tauri-apps/api/core';
import type { UserMessageDraft, DraftAttachment } from './client';

export type CachedUserMessageDraft = Omit<UserMessageDraft, 'files'> & { images: DraftAttachment[] };

export function newUserMessageDraft(): UserMessageDraft {
  return { id: crypto.randomUUID(), text: '', recipient: 'chief', files: [], uncertain: false };
}

const writesByProfile = new Map<string, Promise<void>>();

export function userMessageDraftCache(profileId: string) {
  const savedFiles = new Map<string, string>();
  return {
    async read(): Promise<UserMessageDraft | null> {
      await writesByProfile.get(profileId);
      const stored = await invoke<CachedUserMessageDraft | null>('user_message_draft_read', { profileId });
      if (!stored) return null;
      const { images: files, ...draft } = stored;
      for (const file of files) savedFiles.set(file.id, file.url);
      return { ...draft, files };
    },
    save(draft: UserMessageDraft): Promise<void> {
      const next = (writesByProfile.get(profileId) ?? Promise.resolve()).catch(() => {}).then(async () => {
        for (const file of draft.files) {
          if (savedFiles.get(file.id) === file.url) continue;
          await invoke('user_message_draft_image_write', { profileId, id: file.id, url: file.url });
          savedFiles.set(file.id, file.url);
        }
        const { files, ...metadata } = draft;
        await invoke('user_message_draft_write', { profileId, draft: { ...metadata, images: files.map(({ id, name }) => ({ id, name })) } });
        for (const id of savedFiles.keys()) if (!files.some(file => file.id === id)) savedFiles.delete(id);
      });
      writesByProfile.set(profileId, next);
      const finished = () => { if (writesByProfile.get(profileId) === next) writesByProfile.delete(profileId); };
      void next.then(finished, finished);
      return next;
    },
  };
}
