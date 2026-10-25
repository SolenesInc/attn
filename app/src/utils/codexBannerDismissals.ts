export const CODEX_UPDATE_DISMISSED_STORAGE_KEY = 'attn.codex_update_banner.dismissed_version';
export const CODEX_OLD_TILES_DISMISSED_STORAGE_KEY = 'attn.codex_old_tiles_banner.dismissed';

export interface OldTilesDismissal {
  running: string;
  sessions: string[];
}

export type OldTilesDismissals = Record<string, OldTilesDismissal>;

export function readDismissedCodexUpdate(): string | null {
  try {
    return window.localStorage.getItem(CODEX_UPDATE_DISMISSED_STORAGE_KEY);
  } catch {
    return null;
  }
}

export function persistDismissedCodexUpdate(version: string): void {
  try {
    window.localStorage.setItem(CODEX_UPDATE_DISMISSED_STORAGE_KEY, version);
  } catch (err) {
    console.warn('[App] Failed to persist the dismissed Codex update:', err);
  }
}

export function readDismissedOldTiles(): OldTilesDismissals {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(CODEX_OLD_TILES_DISMISSED_STORAGE_KEY) ?? '{}');
    return parsed && typeof parsed === 'object' ? (parsed as OldTilesDismissals) : {};
  } catch {
    return {};
  }
}

export function persistDismissedOldTiles(dismissals: OldTilesDismissals): void {
  try {
    window.localStorage.setItem(CODEX_OLD_TILES_DISMISSED_STORAGE_KEY, JSON.stringify(dismissals));
  } catch (err) {
    console.warn('[App] Failed to persist the dismissed old Codex tiles notice:', err);
  }
}
