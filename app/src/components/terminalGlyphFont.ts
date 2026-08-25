export const APPLE_COLOR_EMOJI_FAMILY = '"Apple Color Emoji"';

const ZWJ = 0x200d;
const VS16 = 0xfe0f;
const KEYCAP = 0x20e3;

function isRegionalIndicator(cp: number): boolean {
  return cp >= 0x1f1e6 && cp <= 0x1f1ff;
}
function isSkinToneModifier(cp: number): boolean {
  return cp >= 0x1f3fb && cp <= 0x1f3ff;
}
// Stops short of the PUA planes (F0000+) the bundled Nerd Font occupies, so
// icon glyphs are never routed to the emoji font.
function isSupplementaryEmoji(cp: number): boolean {
  return cp >= 0x1f000 && cp <= 0x1faff;
}

// ZWJ counts as emoji intent only alongside an emoji base, or complex-script
// text (Devanagari, Arabic) gets misrouted to the emoji font.
export function graphemeNeedsEmojiShaping(text: string): boolean {
  let hasVs16 = false;
  let hasKeycap = false;
  let hasZwj = false;
  let hasEmojiBase = false;
  let codepoints = 0;

  for (const ch of text) {
    const cp = ch.codePointAt(0);
    if (cp === undefined) continue;
    codepoints += 1;
    if (cp === VS16) hasVs16 = true;
    else if (cp === KEYCAP) hasKeycap = true;
    else if (cp === ZWJ) hasZwj = true;
    if (isRegionalIndicator(cp) || isSkinToneModifier(cp) || isSupplementaryEmoji(cp)) {
      hasEmojiBase = true;
    }
  }

  if (hasVs16) return true;
  if (hasKeycap) return true;
  if (hasZwj && hasEmojiBase) return true;
  if (hasEmojiBase && codepoints > 1) return true;
  return false;
}

// `sizePx` must already be DPR-scaled.
export function terminalGlyphFont(
  style: string,
  sizePx: number,
  baseFamily: string,
  text: string,
): string {
  const family = graphemeNeedsEmojiShaping(text)
    ? `${APPLE_COLOR_EMOJI_FAMILY}, ${baseFamily}`
    : baseFamily;
  return `${style}${sizePx}px ${family}`;
}
