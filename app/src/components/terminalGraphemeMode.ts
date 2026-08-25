// An emoji cluster only ligates while mode 2027 holds it in one cell, and RIS (ESC c)
// turns 2027 off — so the re-assert has to happen mid-chunk, not after it.

const ENCODER = new TextEncoder();

const ENABLE_SEQUENCE = ENCODER.encode('\x1b[?2027h');

const ESC = 0x1b;
const RIS_FINAL = 0x63; // 'c'; ESC c is RIS (full reset).

export const GRAPHEME_CLUSTERING_MODE = 2027;

export interface GraphemeModeTerminal {
  getMode(mode: number, isAnsi?: boolean): boolean;
  write(data: Uint8Array): void;
}

export function enableGraphemeClustering(terminal: GraphemeModeTerminal): void {
  terminal.write(ENABLE_SEQUENCE);
}

export function ensureGraphemeClustering(terminal: GraphemeModeTerminal): boolean {
  if (terminal.getMode(GRAPHEME_CLUSTERING_MODE)) return false;
  terminal.write(ENABLE_SEQUENCE);
  return true;
}

// `trailingEsc` carries a lone ESC across a chunk boundary, since a RIS can straddle
// it; feed the return value back in, and reset it to false when the model is recreated.
export function writeReassertingClustering(
  terminal: GraphemeModeTerminal,
  bytes: Uint8Array,
  trailingEsc: boolean,
): boolean {
  if (bytes.length === 0) return trailingEsc;

  let start = 0;
  if (trailingEsc && bytes[0] === RIS_FINAL) {
    terminal.write(bytes.subarray(0, 1));
    terminal.write(ENABLE_SEQUENCE);
    start = 1;
  }

  for (let i = start; i + 1 < bytes.length; i += 1) {
    if (bytes[i] === ESC && bytes[i + 1] === RIS_FINAL) {
      terminal.write(bytes.subarray(start, i + 2));
      terminal.write(ENABLE_SEQUENCE);
      start = i + 2;
      i += 1;
    }
  }

  if (start < bytes.length) terminal.write(bytes.subarray(start));

  return bytes[bytes.length - 1] === ESC;
}
