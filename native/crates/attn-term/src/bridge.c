#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <ghostty/vt.h>

typedef struct {
  GhosttyTerminal terminal;
  GhosttyRenderState state;
  GhosttyRenderStateRowIterator rows;
  GhosttyRenderStateRowCells cells;
  GhosttyKeyEncoder keys;
  GhosttyKeyEvent key;
  GhosttyMouseEncoder mouse;
  GhosttyMouseEvent pointer;
  uint8_t *responses;
  size_t responses_len;
  size_t responses_cap;
  uint8_t *clipboard;
  size_t clipboard_len;
  size_t clipboard_cap;
} AttnTerm;

typedef struct {
  uint8_t dirty;
  uint8_t fg[3];
  uint8_t bg[3];
  uint8_t cursor_color[3];
  bool has_cursor_color;
  bool cursor_visible;
  bool cursor_blinking;
  uint8_t cursor_style;
  uint16_t cursor_x;
  uint16_t cursor_y;
  uint16_t cols;
  uint16_t rows;
} AttnFrame;

typedef struct {
  uint32_t len;
  uint16_t flags;
  uint8_t wide;
  bool has_fg;
  bool has_bg;
  uint8_t fg[3];
  uint8_t bg[3];
} AttnCell;

static bool attn_append(uint8_t **buf, size_t *len, size_t *cap, const uint8_t *data, size_t n) {
  size_t needed = *len + n;
  if (needed > *cap) {
    size_t next_cap = *cap == 0 ? 256 : *cap;
    while (next_cap < needed) next_cap *= 2;
    uint8_t *next = realloc(*buf, next_cap);
    if (next == NULL) return false;
    *buf = next;
    *cap = next_cap;
  }
  if (n > 0) memcpy(*buf + *len, data, n);
  *len = needed;
  return true;
}

static void attn_write_pty(GhosttyTerminal terminal, void *userdata,
                           const uint8_t *data, size_t len) {
  (void)terminal;
  AttnTerm *t = userdata;
  if (t == NULL || data == NULL || len == 0) return;
  attn_append(&t->responses, &t->responses_len, &t->responses_cap, data, len);
}

static bool attn_text_mime(GhosttyString mime) {
  static const char plain[] = "text/plain";
  return mime.len >= sizeof(plain) - 1 && memcmp(mime.ptr, plain, sizeof(plain) - 1) == 0;
}

static void attn_clipboard_write(GhosttyTerminal terminal, void *userdata,
                                 const GhosttyClipboardWrite *write) {
  (void)terminal;
  AttnTerm *t = userdata;
  GhosttyClipboardWriteReply reply = GHOSTTY_INIT_SIZED(GhosttyClipboardWriteReply);
  reply.result = GHOSTTY_CLIPBOARD_WRITE_RESULT_UNSUPPORTED;
  GhosttyString text = {0};
  bool found = write->contents_len == 0;
  for (size_t i = 0; !found && i < write->contents_len; i++) {
    if (attn_text_mime(write->contents[i].mime)) {
      text = write->contents[i].data;
      found = true;
    }
  }
  uint32_t n = (uint32_t)text.len;
  if (t != NULL && found &&
      attn_append(&t->clipboard, &t->clipboard_len, &t->clipboard_cap, (const uint8_t *)&n, sizeof(n)) &&
      attn_append(&t->clipboard, &t->clipboard_len, &t->clipboard_cap, text.ptr, n)) {
    reply.result = GHOSTTY_CLIPBOARD_WRITE_RESULT_SUCCESS;
  }
  write->reply(write, &reply);
}

static AttnTerm *attn_wrap(GhosttyTerminal terminal, size_t scrollback_bytes) {
  AttnTerm *t = calloc(1, sizeof(*t));
  if (t == NULL) goto fail;
  t->terminal = terminal;
  if (ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES,
                           &scrollback_bytes) != GHOSTTY_SUCCESS) goto fail;
  if (ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_USERDATA, t) != GHOSTTY_SUCCESS) goto fail;
  if (ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_WRITE_PTY,
                           (const void *)attn_write_pty) != GHOSTTY_SUCCESS) goto fail;
  if (ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE,
                           (const void *)attn_clipboard_write) != GHOSTTY_SUCCESS) goto fail;
  if (ghostty_render_state_new(NULL, &t->state) != GHOSTTY_SUCCESS) goto fail;
  if (ghostty_render_state_row_iterator_new(NULL, &t->rows) != GHOSTTY_SUCCESS) goto fail;
  if (ghostty_render_state_row_cells_new(NULL, &t->cells) != GHOSTTY_SUCCESS) goto fail;
  if (ghostty_key_encoder_new(NULL, &t->keys) != GHOSTTY_SUCCESS) goto fail;
  if (ghostty_key_event_new(NULL, &t->key) != GHOSTTY_SUCCESS) goto fail;
  if (ghostty_mouse_encoder_new(NULL, &t->mouse) != GHOSTTY_SUCCESS) goto fail;
  if (ghostty_mouse_event_new(NULL, &t->pointer) != GHOSTTY_SUCCESS) goto fail;
  return t;
fail:
  if (t == NULL) {
    ghostty_terminal_free(terminal);
    return NULL;
  }
  ghostty_mouse_encoder_free(t->mouse);
  ghostty_key_event_free(t->key);
  ghostty_key_encoder_free(t->keys);
  ghostty_render_state_row_cells_free(t->cells);
  ghostty_render_state_row_iterator_free(t->rows);
  ghostty_render_state_free(t->state);
  ghostty_terminal_free(terminal);
  free(t);
  return NULL;
}

AttnTerm *attn_term_new(uint16_t cols, uint16_t rows, size_t scrollback_bytes) {
  if (cols == 0 || rows == 0) return NULL;
  GhosttyTerminal terminal = NULL;
  if (ghostty_terminal_new(NULL, &terminal, cols, rows) != GHOSTTY_SUCCESS) return NULL;
  return attn_wrap(terminal, scrollback_bytes);
}

AttnTerm *attn_term_restore(const uint8_t *data, size_t len, size_t scrollback_bytes) {
  if (data == NULL || len == 0) return NULL;
  GhosttySnapshotDecoder decoder;
  if (ghostty_snapshot_decoder_new_buf(NULL, &decoder, data, len) != GHOSTTY_SUCCESS) return NULL;
  GhosttyTerminal terminal = NULL;
  GhosttyResult rc = ghostty_snapshot_decoder_decode(decoder, &terminal);
  ghostty_snapshot_decoder_free(decoder);
  if (rc != GHOSTTY_SUCCESS || terminal == NULL) return NULL;
  return attn_wrap(terminal, scrollback_bytes);
}

uint8_t *attn_term_snapshot(AttnTerm *t, size_t *out_len) {
  *out_len = 0;
  uint8_t *raw = NULL;
  size_t len = 0;
  if (ghostty_snapshot_encode_alloc(t->terminal, NULL, &raw, &len) != GHOSTTY_SUCCESS) return NULL;
  uint8_t *copy = malloc(len);
  if (copy != NULL) {
    memcpy(copy, raw, len);
    *out_len = len;
  }
  ghostty_free(NULL, raw, len);
  return copy;
}

void attn_term_bytes_free(uint8_t *data) { free(data); }

void attn_term_free(AttnTerm *t) {
  if (t == NULL) return;
  ghostty_mouse_event_free(t->pointer);
  ghostty_mouse_encoder_free(t->mouse);
  ghostty_key_event_free(t->key);
  ghostty_key_encoder_free(t->keys);
  ghostty_render_state_row_cells_free(t->cells);
  ghostty_render_state_row_iterator_free(t->rows);
  ghostty_render_state_free(t->state);
  ghostty_terminal_free(t->terminal);
  free(t->responses);
  free(t->clipboard);
  free(t);
}

void attn_term_write(AttnTerm *t, const uint8_t *data, size_t len) {
  if (len == 0) return;
  ghostty_terminal_vt_write(t->terminal, data, len);
}

int attn_term_resize(AttnTerm *t, uint16_t cols, uint16_t rows,
                     uint32_t cell_width, uint32_t cell_height) {
  if (cols == 0 || rows == 0) return GHOSTTY_INVALID_VALUE;
  return ghostty_terminal_resize(t->terminal, cols, rows, cell_width, cell_height);
}

void attn_term_size(AttnTerm *t, uint16_t *cols, uint16_t *rows) {
  ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_COLS, cols);
  ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_ROWS, rows);
}

void attn_term_cursor(AttnTerm *t, uint16_t *col, uint16_t *row) {
  ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_CURSOR_X, col);
  ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_CURSOR_Y, row);
}

void attn_term_scrollbar(AttnTerm *t, uint64_t *total, uint64_t *offset, uint64_t *len) {
  GhosttyTerminalScrollbar bar = {0};
  ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBAR, &bar);
  *total = bar.total;
  *offset = bar.offset;
  *len = bar.len;
}

void attn_term_scroll_to_row(AttnTerm *t, size_t row) {
  GhosttyTerminalScrollViewport behavior = {
      .tag = GHOSTTY_TERMINAL_SCROLL_VIEWPORT_ROW,
      .value = {.row = row},
  };
  ghostty_terminal_scroll_viewport(t->terminal, behavior);
}

/* The text of cells [start, end) of row `row` counted from the top of the scrollback, as code
   points: an empty cell is a space and a wide character's spacer is left out. Returns how many
   code points the row needs, which is more than `cap` when `out` was too small. */
size_t attn_term_row_text(AttnTerm *t, uint32_t row, uint16_t start, uint16_t end, uint32_t *out,
                          size_t cap) {
  size_t n = 0;
  for (uint16_t x = start; x < end; x++) {
    GhosttyPoint point = {.tag = GHOSTTY_POINT_TAG_SCREEN,
                          .value = {.coordinate = {.x = x, .y = row}}};
    GhosttyGridRef ref = {0};
    if (ghostty_terminal_grid_ref(t->terminal, point, &ref) != GHOSTTY_SUCCESS) break;
    GhosttyCell cell = 0;
    ghostty_grid_ref_cell(&ref, &cell);
    GhosttyCellWide wide = GHOSTTY_CELL_WIDE_NARROW;
    ghostty_cell_get(cell, GHOSTTY_CELL_DATA_WIDE, &wide);
    if (wide == GHOSTTY_CELL_WIDE_SPACER_TAIL) continue;
    uint32_t cluster[16];
    size_t len = 0;
    if (ghostty_grid_ref_graphemes(&ref, cluster, 16, &len) != GHOSTTY_SUCCESS) len = 0;
    if (len == 0) {
      cluster[0] = ' ';
      len = 1;
    }
    for (size_t i = 0; i < len; i++, n++) {
      if (n < cap) out[n] = cluster[i];
    }
  }
  return n;
}

static bool attn_term_ref(AttnTerm *t, uint32_t row, uint16_t col, GhosttyGridRef *ref) {
  GhosttyPoint point = {.tag = GHOSTTY_POINT_TAG_SCREEN,
                        .value = {.coordinate = {.x = col, .y = row}}};
  *ref = (GhosttyGridRef){0};
  return ghostty_terminal_grid_ref(t->terminal, point, ref) == GHOSTTY_SUCCESS;
}

static bool attn_term_row_data(AttnTerm *t, uint32_t row, GhosttyRowData data) {
  GhosttyGridRef ref;
  if (!attn_term_ref(t, row, 0, &ref)) return false;
  GhosttyRow r = 0;
  if (ghostty_grid_ref_row(&ref, &r) != GHOSTTY_SUCCESS) return false;
  bool value = false;
  return ghostty_row_get(r, data, &value) == GHOSTTY_SUCCESS && value;
}

bool attn_term_row_continues(AttnTerm *t, uint32_t row) {
  return attn_term_row_data(t, row, GHOSTTY_ROW_DATA_WRAP_CONTINUATION);
}

bool attn_term_row_links(AttnTerm *t, uint32_t row) {
  return attn_term_row_data(t, row, GHOSTTY_ROW_DATA_HYPERLINK);
}

size_t attn_term_cell_uri(AttnTerm *t, uint32_t row, uint16_t col, uint8_t *out, size_t cap) {
  GhosttyGridRef ref;
  if (!attn_term_ref(t, row, col, &ref)) return 0;
  size_t len = 0;
  GhosttyResult result = ghostty_grid_ref_hyperlink_uri(&ref, out, cap, &len);
  return result == GHOSTTY_SUCCESS || result == GHOSTTY_OUT_OF_SPACE ? len : 0;
}

bool attn_term_mode(AttnTerm *t, uint16_t value, bool ansi) {
  GhosttyTerminalModeConfig config;
  memset(&config, 0, sizeof(config));
  config.mode = ghostty_mode_new(value, ansi);
  return ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_MODE, &config) ==
             GHOSTTY_SUCCESS &&
         config.value;
}

bool attn_term_mouse_tracking(AttnTerm *t) {
  bool tracking = false;
  return ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &tracking) ==
             GHOSTTY_SUCCESS &&
         tracking;
}

bool attn_term_alternate_screen(AttnTerm *t) {
  GhosttyTerminalScreen screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
  return ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &screen) ==
             GHOSTTY_SUCCESS &&
         screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE;
}

const uint8_t *attn_term_responses(AttnTerm *t, size_t *len) {
  *len = t->responses_len;
  return t->responses;
}

void attn_term_clear_responses(AttnTerm *t) { t->responses_len = 0; }

const uint8_t *attn_term_clipboard(AttnTerm *t, size_t *len) {
  *len = t->clipboard_len;
  return t->clipboard;
}

void attn_term_clear_clipboard(AttnTerm *t) { t->clipboard_len = 0; }

static void attn_rgb(GhosttyColorRgb c, uint8_t out[3]) {
  out[0] = c.r;
  out[1] = c.g;
  out[2] = c.b;
}

int attn_term_frame(AttnTerm *t, AttnFrame *f) {
  GhosttyResult rc = ghostty_render_state_update(t->state, t->terminal);
  if (rc != GHOSTTY_SUCCESS) return rc;
  GhosttyRenderStateDirty dirty = GHOSTTY_RENDER_STATE_DIRTY_FALSE;
  ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_DIRTY, &dirty);
  f->dirty = (uint8_t)dirty;
  GhosttyColorRgb c;
  ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_COLOR_FOREGROUND, &c);
  attn_rgb(c, f->fg);
  ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_COLOR_BACKGROUND, &c);
  attn_rgb(c, f->bg);
  f->has_cursor_color = false;
  ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_COLOR_CURSOR_HAS_VALUE,
                           &f->has_cursor_color);
  if (f->has_cursor_color) {
    ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_COLOR_CURSOR, &c);
    attn_rgb(c, f->cursor_color);
  }
  bool has = false, visible = false, blinking = false;
  ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_HAS_VALUE, &has);
  ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_CURSOR_VISIBLE, &visible);
  ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_CURSOR_BLINKING, &blinking);
  f->cursor_visible = has && visible;
  f->cursor_blinking = blinking;
  GhosttyCursorStyle style = GHOSTTY_CURSOR_STYLE_BLOCK;
  ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_CURSOR_VISUAL_STYLE, &style);
  f->cursor_style = (uint8_t)style;
  f->cursor_x = 0;
  f->cursor_y = 0;
  if (has) {
    ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_X, &f->cursor_x);
    ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_Y, &f->cursor_y);
  }
  ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_COLS, &f->cols);
  ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_ROWS, &f->rows);
  return ghostty_render_state_get(t->state, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &t->rows);
}

bool attn_term_next_row(AttnTerm *t, bool *dirty) {
  if (!ghostty_render_state_row_iterator_next(t->rows)) return false;
  *dirty = false;
  ghostty_render_state_row_get(t->rows, GHOSTTY_RENDER_STATE_ROW_DATA_DIRTY, dirty);
  return true;
}

int attn_term_row_cells(AttnTerm *t) {
  bool clean = false;
  ghostty_render_state_row_set(t->rows, GHOSTTY_RENDER_STATE_ROW_OPT_DIRTY, &clean);
  return ghostty_render_state_row_get(t->rows, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &t->cells);
}

bool attn_term_next_cell(AttnTerm *t, AttnCell *cell, uint32_t *codepoints, size_t cap) {
  if (!ghostty_render_state_row_cells_next(t->cells)) return false;
  GhosttyCodepointBuffer graphemes = {.ptr = codepoints, .cap = cap, .len = 0};
  ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_BUF,
                                     &graphemes);
  cell->len = (uint32_t)(graphemes.len < cap ? graphemes.len : cap);
  GhosttyStyle style;
  ghostty_style_default(&style);
  ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style);
  cell->flags = style.flags;
  GhosttyColorRgb c;
  cell->has_fg = ghostty_render_state_row_cells_get(
                     t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &c) == GHOSTTY_SUCCESS;
  if (cell->has_fg) attn_rgb(c, cell->fg);
  cell->has_bg = ghostty_render_state_row_cells_get(
                     t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &c) == GHOSTTY_SUCCESS;
  if (cell->has_bg) attn_rgb(c, cell->bg);
  GhosttyCell raw = 0;
  ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &raw);
  GhosttyCellWide wide = GHOSTTY_CELL_WIDE_NARROW;
  ghostty_cell_get(raw, GHOSTTY_CELL_DATA_WIDE, &wide);
  cell->wide = (uint8_t)wide;
  return true;
}

int attn_term_frame_done(AttnTerm *t) {
  GhosttyRenderStateDirty clean = GHOSTTY_RENDER_STATE_DIRTY_FALSE;
  return ghostty_render_state_set(t->state, GHOSTTY_RENDER_STATE_OPT_DIRTY, &clean);
}

int attn_term_encode_key(AttnTerm *t, uint8_t action, uint32_t key, uint16_t mods,
                         uint16_t consumed, const uint8_t *utf8, size_t utf8_len,
                         uint32_t unshifted, uint8_t option_as_alt, uint8_t *out,
                         size_t cap, size_t *written) {
  *written = 0;
  GhosttyResult rc = ghostty_key_encoder_set_from_terminal(t->keys, t->terminal);
  if (rc != GHOSTTY_SUCCESS) return rc;
  GhosttyOptionAsAlt alt = (GhosttyOptionAsAlt)option_as_alt;
  rc = ghostty_key_encoder_set(t->keys, GHOSTTY_KEY_ENCODER_OPT_MACOS_OPTION_AS_ALT, &alt);
  if (rc != GHOSTTY_SUCCESS) return rc;
  ghostty_key_event_set_action(t->key, (GhosttyKeyAction)action);
  ghostty_key_event_set_key(t->key, (GhosttyKey)key);
  ghostty_key_event_set_mods(t->key, (GhosttyMods)mods);
  ghostty_key_event_set_consumed_mods(t->key, (GhosttyMods)consumed);
  ghostty_key_event_set_composing(t->key, false);
  GhosttyString text = {.ptr = utf8, .len = utf8_len};
  ghostty_key_event_set_utf8(t->key, text);
  ghostty_key_event_set_unshifted_codepoint(t->key, unshifted);
  return ghostty_key_encoder_encode(t->keys, t->key, out, cap, written);
}


int attn_term_set_theme(AttnTerm *t, const uint8_t *fg, const uint8_t *bg,
                        const uint8_t *cursor, const uint8_t *ansi) {
  GhosttyColorRgb palette[256];
  GhosttyResult rc = ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_COLOR_PALETTE, palette);
  if (rc != GHOSTTY_SUCCESS) return rc;
  for (int i = 0; i < 16; i++) {
    palette[i] = (GhosttyColorRgb){ansi[i * 3], ansi[i * 3 + 1], ansi[i * 3 + 2]};
  }
  GhosttyColorRgb f = {fg[0], fg[1], fg[2]};
  GhosttyColorRgb b = {bg[0], bg[1], bg[2]};
  GhosttyColorRgb c = {cursor[0], cursor[1], cursor[2]};
  if ((rc = ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND, &f)) != GHOSTTY_SUCCESS) return rc;
  if ((rc = ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND, &b)) != GHOSTTY_SUCCESS) return rc;
  if ((rc = ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_COLOR_CURSOR, &c)) != GHOSTTY_SUCCESS) return rc;
  return ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_COLOR_PALETTE, palette);
}
