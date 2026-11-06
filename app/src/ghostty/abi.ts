// libghostty-vt's C ABI as the browser sees it, from include/ghostty/vt/*.h at the commit
// in ghostty-vt.pin. Offsets are wasm32 and asserted at runtime by abi.layout.test.ts.

/** Negative values are errors. */
export const GHOSTTY_SUCCESS = 0;
export const GHOSTTY_OUT_OF_SPACE = -3;
/** Not an error: the asked-for value does not exist in the current state. */
export const GHOSTTY_NO_VALUE = -4;

export const TERMINAL_OPT_WRITE_PTY = 2;
export const TERMINAL_OPT_COLOR_FOREGROUND = 12;
export const TERMINAL_OPT_COLOR_BACKGROUND = 13;
export const TERMINAL_OPT_COLOR_CURSOR = 14;
export const TERMINAL_OPT_COLOR_PALETTE = 15;
export const TERMINAL_OPT_SCROLLBACK_MAX_BYTES = 28;

export const TERMINAL_DATA_COLS = 1;
export const TERMINAL_DATA_ROWS = 2;
export const TERMINAL_DATA_ACTIVE_SCREEN = 6;
export const TERMINAL_DATA_MOUSE_TRACKING = 11;
export const TERMINAL_DATA_SCROLLBACK_ROWS = 15;
export const TERMINAL_DATA_COLOR_FOREGROUND = 18;
export const TERMINAL_DATA_COLOR_BACKGROUND = 19;
export const TERMINAL_DATA_COLOR_PALETTE_DEFAULT = 25;
export const TERMINAL_DATA_MODE = 37;

export const SNAPSHOT_DATA_HISTORY_ROWS_PRIMARY = 3;
export const SNAPSHOT_DATA_PROGRESS_ROWS = 6;
export const SNAPSHOT_DATA_PROGRESS_REMAINING = 7;

export const SCREEN_TYPE_ALTERNATE = 1;

export const MOUSE_TRACKING_NONE = 0;

export const RENDER_DATA_DIRTY = 3;
export const RENDER_DATA_ROW_ITERATOR = 4;
export const RENDER_DATA_CURSOR_VISUAL_STYLE = 10;
export const RENDER_DATA_CURSOR_VISIBLE = 11;
export const RENDER_DATA_CURSOR_BLINKING = 12;
export const RENDER_DATA_CURSOR_VIEWPORT_HAS_VALUE = 14;
export const RENDER_DATA_CURSOR_VIEWPORT_X = 15;
export const RENDER_DATA_CURSOR_VIEWPORT_Y = 16;
export const RENDER_DATA_COLORS = 19;

export const RENDER_OPT_DIRTY = 1;

export const DIRTY_FALSE = 0;
export const DIRTY_PARTIAL = 1;
export const DIRTY_FULL = 2;

export const CURSOR_STYLE_BAR = 0;
export const CURSOR_STYLE_BLOCK = 1;
export const CURSOR_STYLE_UNDERLINE = 2;
export const CURSOR_STYLE_BLOCK_HOLLOW = 3;

export const ROW_DATA_DIRTY = 1;
export const ROW_DATA_CELLS = 3;
export const ROW_DATA_CELLS_RAW = 5;

export const ROW_OPT_DIRTY = 1;

export const CELLS_DATA_STYLE = 2;
export const CELLS_DATA_GRAPHEMES_LEN = 3;
export const CELLS_DATA_BG_COLOR = 5;
export const CELLS_DATA_FG_COLOR = 6;


export const CELL_WIDE_WIDTH = [1, 2, 0, 0] as const;

export const cellCodepoint = (lo: number): number => (lo & 2) !== 0 ? 0 : (lo >>> 2) & 0x1fffff;
export const cellHasGrapheme = (lo: number): boolean => (lo & 3) === 1;
export const cellHasStyling = (lo: number, hi: number): boolean => (lo >>> 26) !== 0 || (hi & 0x3ff) !== 0;
export const cellWide = (hi: number): 0 | 1 | 2 | 3 => ((hi >>> 10) & 3) as 0 | 1 | 2 | 3;
export const cellHasHyperlink = (hi: number): boolean => ((hi >>> 13) & 1) !== 0;

/** A raw GhosttyRow, not the render-state row. */
export const ROW_RAW_DATA_WRAP = 1;

/** GhosttyPointTag. */
export const POINT_TAG_ACTIVE = 0;
export const POINT_TAG_HISTORY = 3;

/** GhosttyPoint (24 bytes): the union carries two u64s for ABI padding, and their alignment
 * is what pushes the coordinate to offset 8. */
export const POINT_SIZE = 24;
export const POINT_OFF_TAG = 0;
export const POINT_OFF_X = 8;
export const POINT_OFF_Y = 12;

/** GhosttyString (8 bytes). wasm32 passes it by reference to where its fields live. */
export const STRING_SIZE = 8;
export const STRING_OFF_PTR = 0;
export const STRING_OFF_LEN = 4;

/** GhosttyGridRef (8 bytes): an opaque node and x/y, frozen with no size field. */
export const GRID_REF_SIZE = 8;

/** GhosttyStyle (16 bytes), frozen with no size field. The render-state path resolves colors
 * via CELLS_DATA_FG_COLOR / BG_COLOR; grid refs (scrollback) read the slots: a one-byte tag,
 * then a palette index or three RGB bytes. */
export const STYLE_SIZE = 16;
export const STYLE_OFF_FG_KIND = 0;
export const STYLE_OFF_FG = 1;
export const STYLE_OFF_BG_KIND = 4;
export const STYLE_OFF_BG = 5;
export const STYLE_COLOR_DEFAULT = 0;
export const STYLE_COLOR_PALETTE = 1;
export const STYLE_COLOR_RGB = 2;
export const STYLE_OFF_FLAGS = 12;
export const STYLE_FLAG_BOLD = 1 << 0;
export const STYLE_FLAG_ITALIC = 1 << 1;
export const STYLE_FLAG_FAINT = 1 << 2;
export const STYLE_FLAG_BLINK = 1 << 3;
export const STYLE_FLAG_INVERSE = 1 << 4;
export const STYLE_FLAG_INVISIBLE = 1 << 5;
export const STYLE_FLAG_STRIKETHROUGH = 1 << 6;
export const STYLE_FLAG_OVERLINE = 1 << 7;
export const STYLE_UNDERLINE_MASK = 0x7 << 8;

/** GhosttyRenderStateColors is 784, not the 782 its fields add up to: the struct is padded
 * to its alignment, and the size field must carry sizeof. */
export const COLORS_SIZE = 784;
export const COLORS_OFF_BACKGROUND = 4;
export const COLORS_OFF_FOREGROUND = 7;
export const COLORS_OFF_CURSOR = 10;
export const COLORS_OFF_CURSOR_HAS_VALUE = 13;
export const COLORS_OFF_PALETTE = 14;

/** GhosttyMode packs the DEC/ANSI distinction into the high bit: ANSI modes set it, DEC
 * private modes clear it. */
export function packMode(value: number, ansi: boolean): number {
  return (value & 0x7fff) | (ansi ? 0x8000 : 0);
}

export interface GhosttyExports extends WebAssembly.Exports {
  memory: WebAssembly.Memory;
  __indirect_function_table: WebAssembly.Table;

  ghostty_wasm_alloc_opaque(): number;
  ghostty_wasm_free_opaque(ptr: number): void;
  ghostty_wasm_alloc(len: number): number;
  ghostty_wasm_free(ptr: number, len: number): void;

  ghostty_type_json(): number;

  ghostty_key_event_new(allocator: number, out: number): number;
  ghostty_key_event_free(event: number): void;
  ghostty_key_event_set_action(event: number, action: number): void;
  ghostty_key_event_set_key(event: number, key: number): void;
  ghostty_key_event_set_mods(event: number, mods: number): void;
  ghostty_key_event_set_consumed_mods(event: number, mods: number): void;
  ghostty_key_event_set_composing(event: number, composing: number): void;
  ghostty_key_event_set_utf8(event: number, utf8: number): number;
  ghostty_key_event_set_unshifted_codepoint(event: number, codepoint: number): void;

  ghostty_key_encoder_new(allocator: number, out: number): number;
  ghostty_key_encoder_free(encoder: number): void;
  ghostty_key_encoder_set(encoder: number, option: number, value: number): void;
  ghostty_key_encoder_set_from_terminal(encoder: number, terminal: number): void;
  ghostty_key_encoder_encode(
    encoder: number,
    event: number,
    out: number,
    outLen: number,
    written: number,
  ): number;

  ghostty_terminal_new(allocator: number, out: number, cols: number, rows: number): number;
  ghostty_terminal_free(terminal: number): void;
  ghostty_terminal_resize(terminal: number, cols: number, rows: number): number;
  ghostty_terminal_set(terminal: number, option: number, value: number): number;
  ghostty_terminal_get(terminal: number, data: number, out: number): number;
  ghostty_terminal_vt_write(terminal: number, data: number, len: number): void;
  ghostty_terminal_grid_ref(terminal: number, point: number, outRef: number): number;

  ghostty_snapshot_decoder_new_buf(allocator: number, out: number, ptr: number, len: number): number;
  ghostty_snapshot_decoder_free(decoder: number): void;
  ghostty_snapshot_decoder_ready(decoder: number, outTerminal: number): number;
  ghostty_snapshot_decoder_next(decoder: number): number;
  ghostty_snapshot_decoder_get(decoder: number, data: number, out: number): number;

  ghostty_grid_ref_cell(ref: number, outCell: number): number;
  ghostty_grid_ref_row(ref: number, outRow: number): number;
  ghostty_grid_ref_style(ref: number, outStyle: number): number;
  ghostty_grid_ref_graphemes(ref: number, buf: number, bufLen: number, outLen: number): number;
  ghostty_grid_ref_hyperlink_uri(ref: number, buf: number, bufLen: number, outLen: number): number;

  ghostty_row_get(row: bigint, data: number, out: number): number;

  ghostty_render_state_new(allocator: number, out: number): number;
  ghostty_render_state_free(state: number): void;
  ghostty_render_state_update(state: number, terminal: number): number;
  ghostty_render_state_get(state: number, data: number, out: number): number;
  ghostty_render_state_set(state: number, option: number, value: number): number;

  ghostty_render_state_row_iterator_new(allocator: number, out: number): number;
  ghostty_render_state_row_iterator_free(iterator: number): void;
  ghostty_render_state_row_iterator_next(iterator: number): number;
  ghostty_render_state_row_get(iterator: number, data: number, out: number): number;
  ghostty_render_state_row_set(iterator: number, option: number, value: number): number;

  ghostty_render_state_row_cells_new(allocator: number, out: number): number;
  ghostty_render_state_row_cells_free(cells: number): void;
  ghostty_render_state_row_cells_select(cells: number, x: number): number;
  ghostty_render_state_row_cells_get(cells: number, data: number, out: number): number;
}
