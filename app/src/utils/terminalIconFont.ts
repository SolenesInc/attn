// A 2D canvas ignores a face loaded only via the CSS Font Loading API unless it is also USED in
// DOM layout, and the face then loads asynchronously, so glyphs rasterized before it is ready cache blank.

export const TERMINAL_ICON_FONT_FAMILY = 'Symbols Nerd Font Mono';

const PROBE_GLYPHS = '\uF07B\uE0B0\uF15B';

let probeMounted = false;
let readyPromise: Promise<void> | null = null;

function mountProbe(): void {
  if (probeMounted || typeof document === 'undefined' || !document.body) return;
  probeMounted = true;
  const probe = document.createElement('span');
  probe.setAttribute('aria-hidden', 'true');
  // Off-screen but still laid out: display:none / visibility:hidden can skip font loading.
  probe.style.cssText =
    'position:absolute;left:-9999px;top:-9999px;width:0;height:0;overflow:hidden;' +
    `font-family:"${TERMINAL_ICON_FONT_FAMILY}";font-size:32px;`;
  probe.textContent = PROBE_GLYPHS;
  document.body.appendChild(probe);
}

export function ensureTerminalIconFont(fontSize: number): Promise<void> {
  mountProbe();
  if (readyPromise) return readyPromise;
  const fonts = typeof document !== 'undefined' ? document.fonts : null;
  if (!fonts || typeof fonts.load !== 'function') {
    readyPromise = Promise.resolve();
    return readyPromise;
  }
  const spec = `${fontSize}px "${TERMINAL_ICON_FONT_FAMILY}"`;
  readyPromise = fonts
    .load(spec)
    .then(() => undefined)
    .catch(() => undefined);
  return readyPromise;
}
