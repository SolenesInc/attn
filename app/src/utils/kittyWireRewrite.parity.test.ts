// @vitest-environment node

// The shipped wasm build can never parse kitty at any ghostty pin, so a native-only
// witness (internal/pty/kittycorpus_test.go) would not prove the grid reproduces.

// Regenerate the corpus with:
//   go test ./internal/pty -run TestKittyWireRewriteCorpus -update

// @types/node isn't a direct dependency of this package (transitive peer only).
// @ts-expect-error -- see above
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { Ghostty, type GhosttyCell, type GhosttyTerminal } from '../ghostty';
import { describe, expect, it } from 'vitest';

interface CorpusEntry {
  name: string;
  cols: number;
  rows: number;
  chunks: string[];
  wire: string[];
  resync: string[];
  workerPlainText: string;
  workerViewportText: string;
  cursorCol: number;
  cursorRow: number;
}

const corpusPath = fileURLToPath(
  new URL('../../../internal/pty/testdata/kitty_rewrite_corpus.json', import.meta.url),
);
const corpus = JSON.parse(readFileSync(corpusPath, 'utf8')) as { entries: CorpusEntry[] };

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

// Matches internal/ghosttyvt's DefaultMaxScrollback, so the two models retain the same
// history and a scrolled entry compares like for like.
const SCROLLBACK_LIMIT = 10000;

async function loadGhostty(): Promise<Ghostty> {
  const bytes = readFileSync(wasmPath);
  const mod = await WebAssembly.compile(bytes);
  let instance: WebAssembly.Instance;
  instance = await WebAssembly.instantiate(mod, {
    env: {
      log: (ptr: number, len: number) => {
        const memory = (instance.exports.memory as WebAssembly.Memory).buffer;
        console.log('[ghostty-vt]', new TextDecoder().decode(new Uint8Array(memory, ptr, len)));
      },
    },
  });
  return new Ghostty(instance);
}

// One row as text, up to the last cell the program wrote. An unwritten cell reads as
// codepoint 0 and a written space as 32; the two are NOT interchangeable at a row's end.
function rowText(cells: GhosttyCell[]): string {
  let end = cells.length;
  while (end > 0 && cells[end - 1].codepoint === 0) end--;
  let text = '';
  for (let i = 0; i < end; i++) {
    const cell = cells[i];
    if (cell.width === 0) continue;
    text += cell.codepoint === 0 ? ' ' : String.fromCodePoint(cell.codepoint);
  }
  return text;
}

function viewportText(term: GhosttyTerminal): string {
  let out = '';
  for (let y = 0; y < term.rows; y++) {
    out += `${rowText(term.getLine(y) ?? []).replace(/ +$/, '')}\n`;
  }
  return out;
}

// Mirrors ghostty's plain formatter: scrollback then viewport, each row cut at its last
// written cell, trailing untouched rows dropped, no terminating newline.
function plainText(term: GhosttyTerminal): string {
  const rows: string[] = [];
  for (let offset = 0; offset < term.getScrollbackLength(); offset++) {
    rows.push(rowText(term.getScrollbackLine(offset) ?? []));
  }
  for (let y = 0; y < term.rows; y++) {
    rows.push(rowText(term.getLine(y) ?? []));
  }
  while (rows.length > 0 && rows[rows.length - 1] === '') rows.pop();
  return rows.join('\n');
}

function decodeBase64(encoded: string): Uint8Array {
  const binary = atob(encoded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

function replayWire(term: GhosttyTerminal, entry: CorpusEntry): void {
  for (const encoded of entry.wire) {
    if (encoded === '') continue;
    term.write(decodeBase64(encoded));
  }
  term.update();
}

describe('kitty wire rewrite replayed into the wasm model', () => {
  it('covers every corpus entry', () => {
    expect(corpus.entries.length).toBeGreaterThan(0);
  });

  for (const entry of corpus.entries) {
    const resynced = entry.resync.some((reason) => reason !== '');
    const run = resynced ? it.skip : it;

    run(entry.name, async () => {
      const ghostty = await loadGhostty();
      const term = ghostty.createTerminal(entry.cols, entry.rows, {
        scrollbackLimit: SCROLLBACK_LIMIT,
      });
      replayWire(term, entry);

      expect(viewportText(term)).toBe(entry.workerViewportText);
      expect(plainText(term)).toBe(entry.workerPlainText);
      const cursor = term.getCursor();
      expect({ col: cursor.x, row: cursor.y }).toEqual({
        col: entry.cursorCol,
        row: entry.cursorRow,
      });
    });
  }
});
