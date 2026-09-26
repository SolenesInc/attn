import { describe, expect, it } from 'vitest';
import { writeReassertingClustering } from './terminalGraphemeMode';

const RIS = '\x1bc';
const ENABLE_CLUSTERING = '\x1b[?2027h';
const FAMILY = '\u{1F468}\u{200D}\u{1F469}\u{200D}\u{1F467}\u{200D}\u{1F466}';
const encode = (text: string) => new TextEncoder().encode(text);

function chunkings(length: number): number[][] {
  const cuts: number[][] = [[], Array.from({ length: length - 1 }, (_, i) => i + 1)];
  for (let a = 1; a < length; a += 1) {
    cuts.push([a]);
    for (let b = a + 1; b < length; b += 1) cuts.push([a, b]);
  }
  return cuts;
}

function writeInChunks(stream: Uint8Array, cuts: number[]): number[] {
  const written: number[] = [];
  const terminal = { getMode: () => true, write: (data: Uint8Array) => written.push(...data) };
  let trailingEsc = false;
  for (const [from, to] of [0, ...cuts].map((at, i, all) => [at, all[i + 1] ?? stream.length])) {
    trailingEsc = writeReassertingClustering(terminal, stream.subarray(from, to), trailingEsc);
  }
  return written;
}

describe('writeReassertingClustering', () => {
  it.each([
    ['plain output', `hello ${FAMILY}`],
    ['a reset before an emoji', `${RIS}${FAMILY}`],
    ['several resets', `${RIS}${FAMILY}${RIS}${FAMILY}`],
    ['a reset at the very end', `A${RIS}`],
    ['back-to-back resets', `${RIS}${RIS}x`],
    ['an escape before a reset', `\x1b${RIS}${FAMILY}`],
    ['a CSI that is not a reset', `\x1b[mA\x1b[31m${FAMILY}`],
    ['a lone escape then a c elsewhere', `\x1b[1mc\x1b7c${FAMILY}`],
    ['a trailing escape', `${FAMILY}\x1b`],
  ])('re-enables clustering after every reset in %s, however the output is chunked', (_name, text) => {
    const stream = encode(text);
    const expected = Array.from(encode(text.split(RIS).join(RIS + ENABLE_CLUSTERING)));

    for (const cuts of chunkings(stream.length)) {
      expect(writeInChunks(stream, cuts), `cut at ${cuts.join(',')}`).toEqual(expected);
    }
  });
});
