const STORAGE_KEY = 'attn.outpost_updates.dismissed_home_build';

export function readClosedOutpostBanner(): string | null {
  try { return window.localStorage.getItem(STORAGE_KEY); } catch { return null; }
}

export function persistClosedOutpostBanner(key: string): void {
  try { window.localStorage.setItem(STORAGE_KEY, key); } catch (error) {
    console.warn('[App] Failed to persist closed outpost updates banner:', error);
  }
}
