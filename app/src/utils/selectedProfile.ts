export const SELECTED_PROFILE_STORAGE_KEY = 'attn.profile.selected';

export function readSelectedProfileId(): string | undefined {
  try {
    const stored = window.localStorage.getItem(SELECTED_PROFILE_STORAGE_KEY)?.trim();
    return stored || undefined;
  } catch {
    return undefined;
  }
}

export function persistSelectedProfileId(profileId: string | undefined): void {
  try {
    if (profileId) {
      window.localStorage.setItem(SELECTED_PROFILE_STORAGE_KEY, profileId);
    } else {
      window.localStorage.removeItem(SELECTED_PROFILE_STORAGE_KEY);
    }
  } catch (err) {
    console.warn('[App] Failed to remember the selected profile:', err);
  }
}
