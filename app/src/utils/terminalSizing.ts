// "Symbols Nerd Font Mono" sits last so it only supplies the Private Use Area icon
// glyphs no system monospace font has.
export const FONT_FAMILY =
  'Iosevka, Menlo, Monaco, "DejaVu Sans Mono", "Courier New", "Symbols Nerd Font Mono", monospace';
export const TERMINAL_SCROLLBACK_BYTES = 8 * 1024 * 1024;

export function measureCellWidth(fontSize: number, fontFamily: string): number {
  const context = document.createElement('canvas').getContext('2d');
  if (!context) {
    throw new Error('Unable to measure terminal font');
  }
  context.font = `${fontSize}px ${fontFamily}`;
  return Math.max(1, Math.ceil(context.measureText('M').width));
}
