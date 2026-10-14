export type DesktopSelectionStyle = 'dim' | 'rail' | 'spotlight';

export const DESKTOP_SELECTION_STYLE_STORAGE_KEY = 'attn.workspace.selectionStyle';

export function readDesktopSelectionStyle(): DesktopSelectionStyle {
  try {
    const stored = window.localStorage.getItem(DESKTOP_SELECTION_STYLE_STORAGE_KEY);
    return stored === 'dim' || stored === 'rail' ? stored : 'spotlight';
  } catch {
    return 'spotlight';
  }
}

export function persistDesktopSelectionStyle(style: DesktopSelectionStyle): void {
  try {
    window.localStorage.setItem(DESKTOP_SELECTION_STYLE_STORAGE_KEY, style);
  } catch (err) {
    console.warn('[App] Failed to persist desktop-selection style:', err);
  }
}
