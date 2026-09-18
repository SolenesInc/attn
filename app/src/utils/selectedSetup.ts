export const SELECTED_SETUP_STORAGE_KEY = 'attn.setup.selected';

export function readSelectedSetupId(): string | undefined {
  try {
    const stored = window.localStorage.getItem(SELECTED_SETUP_STORAGE_KEY)?.trim();
    return stored || undefined;
  } catch {
    return undefined;
  }
}

export function persistSelectedSetupId(setupId: string | undefined): void {
  try {
    if (setupId) {
      window.localStorage.setItem(SELECTED_SETUP_STORAGE_KEY, setupId);
    } else {
      window.localStorage.removeItem(SELECTED_SETUP_STORAGE_KEY);
    }
  } catch (err) {
    console.warn('[App] Failed to remember the selected setup:', err);
  }
}
