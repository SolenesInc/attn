const STORAGE_KEY = 'attn.outpost_updates.dismissed_home_build';

export function readDismissedOutpostBuild(): string | null {
  try { return window.localStorage.getItem(STORAGE_KEY); } catch { return null; }
}

export function persistDismissedOutpostBuild(build: string): void {
  try { window.localStorage.setItem(STORAGE_KEY, build); } catch (error) {
    console.warn('[App] Failed to persist dismissed outpost updates:', error);
  }
}
