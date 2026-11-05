// @vitest-environment node

// The receipt behind abi.ts: offsets were read off include/ghostty/vt/*.h by hand,
// and a wrong one reads neighbouring bytes forever (COLORS_SIZE was 782, not 784).

// @types/node isn't a direct dependency of this package.
// @ts-expect-error -- see above
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { beforeAll, describe, expect, it } from 'vitest';
import {
  COLORS_OFF_BACKGROUND,
  COLORS_OFF_CURSOR,
  COLORS_OFF_CURSOR_HAS_VALUE,
  COLORS_OFF_FOREGROUND,
  COLORS_OFF_PALETTE,
  COLORS_SIZE,
  GRID_REF_SIZE,
  STYLE_OFF_BG,
  STYLE_OFF_BG_KIND,
  STYLE_OFF_BLINK,
  STYLE_OFF_BOLD,
  STYLE_OFF_FAINT,
  STYLE_OFF_FG,
  STYLE_OFF_FG_KIND,
  STYLE_OFF_INVERSE,
  STYLE_OFF_INVISIBLE,
  STYLE_OFF_ITALIC,
  STYLE_OFF_OVERLINE,
  STYLE_OFF_STRIKETHROUGH,
  STYLE_OFF_UNDERLINE,
  STYLE_SIZE,
  cellCodepoint,
  cellHasGrapheme,
  cellHasHyperlink,
  cellHasStyling,
  cellWide,
} from './abi';

const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

interface TypeField {
  offset: number;
  size: number;
}

interface TypeInfo {
  size: number;
  align: number;
  fields: Record<string, TypeField>;
}

let types: Record<string, TypeInfo>;
let abi: { pointer_size: number; usize_size: number };
let cellExports: {
  memory: WebAssembly.Memory;
  ghostty_cell_get(cell: bigint, data: number, out: number): number;
  ghostty_wasm_alloc(size: number): number;
};

beforeAll(async () => {
  const mod = await WebAssembly.compile(readFileSync(wasmPath));
  let instance: WebAssembly.Instance;
  instance = await WebAssembly.instantiate(mod, {
    env: { log: () => {} },
  });
  const exports = instance.exports as {
    memory: WebAssembly.Memory;
    ghostty_type_json(): number;
  };
  const ptr = exports.ghostty_type_json();
  const bytes = new Uint8Array(exports.memory.buffer);
  let end = ptr;
  while (bytes[end] !== 0) end += 1;
  const doc = JSON.parse(new TextDecoder().decode(bytes.subarray(ptr, end)));
  types = doc.types;
  cellExports = {
    memory: instance.exports.memory as WebAssembly.Memory,
    ghostty_cell_get: instance.exports.ghostty_cell_get as typeof cellExports.ghostty_cell_get,
    ghostty_wasm_alloc: instance.exports.ghostty_wasm_alloc as typeof cellExports.ghostty_wasm_alloc,
  };
  abi = doc.abi;
});

describe('the ABI abi.ts transcribes', () => {
  // Every offset below is a wasm32 offset: on a 64-bit ABI the leading size_t
  // alone would shift all of them.
  it('is the wasm32 one', () => {
    expect(abi.pointer_size).toBe(4);
    expect(abi.usize_size).toBe(4);
  });

  it('lays GhosttyRenderStateColors out where getColors reads it', () => {
    const t = types.GhosttyRenderStateColors;
    expect(t.size).toBe(COLORS_SIZE);
    expect(t.fields.background.offset).toBe(COLORS_OFF_BACKGROUND);
    expect(t.fields.foreground.offset).toBe(COLORS_OFF_FOREGROUND);
    expect(t.fields.cursor.offset).toBe(COLORS_OFF_CURSOR);
    expect(t.fields.cursor_has_value.offset).toBe(COLORS_OFF_CURSOR_HAS_VALUE);
    expect(t.fields.palette.offset).toBe(COLORS_OFF_PALETTE);
    expect(t.fields.palette.size).toBe(256 * 3);
  });

  it('lays GhosttyStyle out where the cell attributes are read', () => {
    const t = types.GhosttyStyle;
    expect(t.size).toBe(STYLE_SIZE);
    expect(t.fields.bold.offset).toBe(STYLE_OFF_BOLD);
    expect(t.fields.italic.offset).toBe(STYLE_OFF_ITALIC);
    expect(t.fields.faint.offset).toBe(STYLE_OFF_FAINT);
    expect(t.fields.blink.offset).toBe(STYLE_OFF_BLINK);
    expect(t.fields.inverse.offset).toBe(STYLE_OFF_INVERSE);
    expect(t.fields.invisible.offset).toBe(STYLE_OFF_INVISIBLE);
    expect(t.fields.strikethrough.offset).toBe(STYLE_OFF_STRIKETHROUGH);
    expect(t.fields.overline.offset).toBe(STYLE_OFF_OVERLINE);
    expect(t.fields.underline.offset).toBe(STYLE_OFF_UNDERLINE);
  });

  it('lays the style colour slots out where the scrollback path reads them', () => {
    const slot = types.GhosttyStyleColor;
    const fg = types.GhosttyStyle.fields.fg_color.offset;
    const bg = types.GhosttyStyle.fields.bg_color.offset;
    expect(fg + slot.fields.tag.offset).toBe(STYLE_OFF_FG_KIND);
    expect(fg + slot.fields.value.offset).toBe(STYLE_OFF_FG);
    expect(bg + slot.fields.tag.offset).toBe(STYLE_OFF_BG_KIND);
    expect(bg + slot.fields.value.offset).toBe(STYLE_OFF_BG);
  });

  it('decodes a cell word as ghostty_cell_get reads it', () => {
    const CELL_DATA = { codepoint: 1, contentTag: 2, wide: 3, hasStyling: 5, hasHyperlink: 7 };
    const GRAPHEME_TAG = 1;
    const out = cellExports.ghostty_wasm_alloc(8);
    const view = new DataView(cellExports.memory.buffer);
    const read = (word: bigint, data: number, width: 'u8' | 'u32'): number => {
      view.setBigUint64(out, 0n, true);
      if (cellExports.ghostty_cell_get(BigInt.asIntN(64, word), data, out) !== 0) throw new Error(`ghostty_cell_get(${data}) failed`);
      return width === 'u8' ? view.getUint8(out) : view.getUint32(out, true);
    };
    let state = 0x9e3779b97f4a7c15n;
    const next = (): bigint => {
      state ^= (state << 13n) & 0xffffffffffffffffn;
      state ^= state >> 7n;
      state ^= (state << 17n) & 0xffffffffffffffffn;
      return state;
    };
    const STYLE_ID_MASK = 0xffffn << 26n;
    for (let i = 0; i < 20_000; i += 1) {
      const word = i % 4 === 0 ? next() & ~STYLE_ID_MASK : i % 4 === 1 ? next() & next() & next() : next();
      const lo = Number(word & 0xffffffffn);
      const hi = Number(word >> 32n);
      const decoded = [cellCodepoint(lo), cellHasGrapheme(lo), cellWide(hi), cellHasStyling(lo, hi), cellHasHyperlink(hi)];
      const expected = [
        read(word, CELL_DATA.codepoint, 'u32'),
        read(word, CELL_DATA.contentTag, 'u32') === GRAPHEME_TAG,
        read(word, CELL_DATA.wide, 'u32'),
        read(word, CELL_DATA.hasStyling, 'u8') !== 0,
        read(word, CELL_DATA.hasHyperlink, 'u8') !== 0,
      ];
      if (decoded.some((value, field) => value !== expected[field])) {
        expect({ word: word.toString(16), decoded }).toEqual({ word: word.toString(16), decoded: expected });
      }
    }
  });

  it('sizes GhosttyGridRef as the scratch buffer assumes', () => {
    expect(types.GhosttyGridRef.size).toBe(GRID_REF_SIZE);
  });
});
