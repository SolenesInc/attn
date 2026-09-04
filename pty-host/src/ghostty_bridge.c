#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <ghostty/vt.h>

extern bool attn_decode_png(void *userdata,
                            const GhosttyAllocator *allocator,
                            const uint8_t *data, size_t data_len,
                            GhosttySysImage *out);

int attn_ghostty_install_png_decoder(void) {
  return ghostty_sys_set(GHOSTTY_SYS_OPT_DECODE_PNG,
                         (const void *)attn_decode_png);
}

uint8_t *attn_ghostty_alloc(const GhosttyAllocator *allocator, size_t len) {
  return ghostty_alloc(allocator, len);
}

void attn_ghostty_set_decoded_image(GhosttySysImage *out, uint32_t width,
                                    uint32_t height, uint8_t *data,
                                    size_t data_len) {
  if (out == NULL) return;
  out->width = width;
  out->height = height;
  out->data = data;
  out->data_len = data_len;
}

typedef struct {
  GhosttyTerminal terminal;
  uint16_t cols;
  uint16_t rows;
  uint32_t cell_width;
  uint32_t cell_height;
  uint8_t *responses;
  size_t responses_len;
  size_t responses_cap;
} AttnGhosttyTerminal;

static void attn_write_pty(GhosttyTerminal terminal, void *userdata,
                           const uint8_t *data, size_t len) {
  (void)terminal;
  AttnGhosttyTerminal *attn = userdata;
  if (attn == NULL || data == NULL || len == 0) return;
  if (len > SIZE_MAX - attn->responses_len) return;
  size_t needed = attn->responses_len + len;
  if (needed > attn->responses_cap) {
    size_t cap = attn->responses_cap == 0 ? 256 : attn->responses_cap;
    while (cap < needed && cap <= SIZE_MAX / 2) cap *= 2;
    if (cap < needed) cap = needed;
    uint8_t *next = realloc(attn->responses, cap);
    if (next == NULL) return;
    attn->responses = next;
    attn->responses_cap = cap;
  }
  memcpy(attn->responses + attn->responses_len, data, len);
  attn->responses_len += len;
}

static GhosttyColorRgb attn_rgb(uint32_t value) {
  GhosttyColorRgb color = {
      .r = (uint8_t)(value >> 16),
      .g = (uint8_t)(value >> 8),
      .b = (uint8_t)value,
  };
  return color;
}

static GhosttyResult attn_configure(AttnGhosttyTerminal *attn,
                                    uint64_t scrollback_bytes,
                                    uint64_t kitty_bytes) {
  GhosttyResult rc = ghostty_terminal_set(
      attn->terminal, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES,
      &scrollback_bytes);
  if (rc != GHOSTTY_SUCCESS) return rc;
  rc = ghostty_terminal_set(
      attn->terminal, GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_STORAGE_LIMIT,
      &kitty_bytes);
  if (rc != GHOSTTY_SUCCESS) return rc;
  size_t continuation = 65U << 20;
  rc = ghostty_terminal_set(
      attn->terminal, GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES,
      &continuation);
  if (rc != GHOSTTY_SUCCESS) return rc;
  rc = ghostty_terminal_set(attn->terminal, GHOSTTY_TERMINAL_OPT_USERDATA,
                            attn);
  if (rc != GHOSTTY_SUCCESS) return rc;
  return ghostty_terminal_set(attn->terminal, GHOSTTY_TERMINAL_OPT_WRITE_PTY,
                              (const void *)attn_write_pty);
}

AttnGhosttyTerminal *attn_ghostty_new(uint16_t cols, uint16_t rows,
                                      uint64_t scrollback_bytes,
                                      uint64_t kitty_bytes) {
  if (cols == 0 || rows == 0) return NULL;
  AttnGhosttyTerminal *attn = calloc(1, sizeof(*attn));
  if (attn == NULL) return NULL;
  attn->cols = cols;
  attn->rows = rows;
  attn->cell_width = 8;
  attn->cell_height = 16;
  if (ghostty_terminal_new(NULL, &attn->terminal, cols, rows) !=
      GHOSTTY_SUCCESS) {
    free(attn);
    return NULL;
  }
  if (attn_configure(attn, scrollback_bytes, kitty_bytes) != GHOSTTY_SUCCESS) {
    ghostty_terminal_free(attn->terminal);
    free(attn);
    return NULL;
  }
  return attn;
}

AttnGhosttyTerminal *attn_ghostty_restore(const uint8_t *data, size_t len,
                                          uint64_t scrollback_bytes,
                                          uint64_t kitty_bytes) {
  if (data == NULL || len == 0) return NULL;
  GhosttySnapshotDecoder decoder;
  if (ghostty_snapshot_decoder_new_buf(NULL, &decoder, data, len) !=
      GHOSTTY_SUCCESS) return NULL;
  GhosttyTerminal terminal = NULL;
  GhosttyResult rc = ghostty_snapshot_decoder_decode(decoder, &terminal);
  ghostty_snapshot_decoder_free(decoder);
  if (rc != GHOSTTY_SUCCESS || terminal == NULL) return NULL;

  AttnGhosttyTerminal *attn = calloc(1, sizeof(*attn));
  if (attn == NULL) {
    ghostty_terminal_free(terminal);
    return NULL;
  }
  attn->terminal = terminal;
  attn->cell_width = 8;
  attn->cell_height = 16;
  ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_COLS, &attn->cols);
  ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_ROWS, &attn->rows);
  if (attn_configure(attn, scrollback_bytes, kitty_bytes) != GHOSTTY_SUCCESS) {
    ghostty_terminal_free(terminal);
    free(attn);
    return NULL;
  }
  return attn;
}

void attn_ghostty_free(AttnGhosttyTerminal *attn) {
  if (attn == NULL) return;
  ghostty_terminal_free(attn->terminal);
  free(attn->responses);
  free(attn);
}

void attn_ghostty_write(AttnGhosttyTerminal *attn, const uint8_t *data,
                        size_t len) {
  if (attn == NULL || data == NULL || len == 0) return;
  ghostty_terminal_vt_write(attn->terminal, data, len);
}

void attn_ghostty_cursor_pos(AttnGhosttyTerminal *attn, uint16_t *x,
                             uint16_t *y) {
  if (x != NULL) *x = 0;
  if (y != NULL) *y = 0;
  if (attn == NULL) return;
  if (x != NULL) {
    ghostty_terminal_get(attn->terminal, GHOSTTY_TERMINAL_DATA_CURSOR_X, x);
  }
  if (y != NULL) {
    ghostty_terminal_get(attn->terminal, GHOSTTY_TERMINAL_DATA_CURSOR_Y, y);
  }
}

void attn_ghostty_size(AttnGhosttyTerminal *attn, uint16_t *cols,
                       uint16_t *rows) {
  if (cols != NULL) *cols = attn == NULL ? 0 : attn->cols;
  if (rows != NULL) *rows = attn == NULL ? 0 : attn->rows;
}

static bool attn_mode(AttnGhosttyTerminal *attn, GhosttyMode mode) {
  if (attn == NULL) return false;
  GhosttyTerminalModeConfig config;
  memset(&config, 0, sizeof(config));
  config.mode = mode;
  return ghostty_terminal_get(attn->terminal, GHOSTTY_TERMINAL_DATA_MODE,
                              &config) == GHOSTTY_SUCCESS && config.value;
}

bool attn_ghostty_alt_screen(AttnGhosttyTerminal *attn) {
  if (attn == NULL) return false;
  GhosttyTerminalScreen screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
  if (ghostty_terminal_get(attn->terminal, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN,
                           &screen) != GHOSTTY_SUCCESS) return false;
  return screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE;
}

bool attn_ghostty_left_right_margin_mode(AttnGhosttyTerminal *attn) {
  return attn_mode(attn, GHOSTTY_MODE_LEFT_RIGHT_MARGIN);
}

bool attn_ghostty_wraparound(AttnGhosttyTerminal *attn) {
  return attn_mode(attn, GHOSTTY_MODE_WRAPAROUND);
}

bool attn_ghostty_cursor_visible(AttnGhosttyTerminal *attn) {
  return attn_mode(attn, GHOSTTY_MODE_CURSOR_VISIBLE);
}

static GhosttyPoint attn_point(GhosttyPointTag tag, uint16_t x, uint32_t y) {
  GhosttyPoint point;
  memset(&point, 0, sizeof(point));
  point.tag = tag;
  point.value.coordinate.x = x;
  point.value.coordinate.y = y;
  return point;
}

void *attn_ghostty_track_cursor(AttnGhosttyTerminal *attn) {
  if (attn == NULL) return NULL;
  uint16_t x = 0, y = 0;
  attn_ghostty_cursor_pos(attn, &x, &y);
  GhosttyTrackedGridRef ref = NULL;
  if (ghostty_terminal_grid_ref_track(
          attn->terminal, attn_point(GHOSTTY_POINT_TAG_ACTIVE, x, y), &ref) !=
      GHOSTTY_SUCCESS) return NULL;
  return ref;
}

void *attn_ghostty_track_screen_point(AttnGhosttyTerminal *attn, uint16_t x,
                                      uint32_t y) {
  if (attn == NULL) return NULL;
  GhosttyTrackedGridRef ref = NULL;
  if (ghostty_terminal_grid_ref_track(
          attn->terminal, attn_point(GHOSTTY_POINT_TAG_SCREEN, x, y), &ref) !=
      GHOSTTY_SUCCESS) return NULL;
  return ref;
}

bool attn_ghostty_tracked_screen_point(void *raw, uint16_t *x, uint32_t *y) {
  if (raw == NULL) return false;
  GhosttyPointCoordinate point;
  if (ghostty_tracked_grid_ref_point((GhosttyTrackedGridRef)raw,
                                     GHOSTTY_POINT_TAG_SCREEN, &point) !=
      GHOSTTY_SUCCESS) return false;
  if (x != NULL) *x = point.x;
  if (y != NULL) *y = point.y;
  return true;
}

void attn_ghostty_tracked_free(void *raw) {
  if (raw != NULL) ghostty_tracked_grid_ref_free((GhosttyTrackedGridRef)raw);
}

int attn_ghostty_resize(AttnGhosttyTerminal *attn, uint16_t cols,
                        uint16_t rows, uint32_t cell_width,
                        uint32_t cell_height) {
  if (attn == NULL || cols == 0 || rows == 0) return GHOSTTY_INVALID_VALUE;
  GhosttyResult rc = ghostty_terminal_resize(attn->terminal, cols, rows,
                                             cell_width, cell_height);
  if (rc == GHOSTTY_SUCCESS) {
    attn->cols = cols;
    attn->rows = rows;
    attn->cell_width = cell_width;
    attn->cell_height = cell_height;
  }
  return rc;
}

int attn_ghostty_set_theme(AttnGhosttyTerminal *attn, bool has_foreground,
                           uint32_t foreground, bool has_background,
                           uint32_t background, bool has_cursor,
                           uint32_t cursor, bool has_palette,
                           const uint32_t *colors) {
  if (attn == NULL) return GHOSTTY_INVALID_VALUE;
  GhosttyResult rc;
  if (has_foreground) {
    GhosttyColorRgb value = attn_rgb(foreground);
    rc = ghostty_terminal_set(attn->terminal,
                              GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND, &value);
    if (rc != GHOSTTY_SUCCESS) return rc;
  }
  if (has_background) {
    GhosttyColorRgb value = attn_rgb(background);
    rc = ghostty_terminal_set(attn->terminal,
                              GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND, &value);
    if (rc != GHOSTTY_SUCCESS) return rc;
  }
  if (has_cursor) {
    GhosttyColorRgb value = attn_rgb(cursor);
    rc = ghostty_terminal_set(attn->terminal,
                              GHOSTTY_TERMINAL_OPT_COLOR_CURSOR, &value);
    if (rc != GHOSTTY_SUCCESS) return rc;
  }
  if (has_palette && colors != NULL) {
    GhosttyColorRgb palette[256];
    rc = ghostty_terminal_get(attn->terminal,
                              GHOSTTY_TERMINAL_DATA_COLOR_PALETTE_DEFAULT,
                              palette);
    if (rc != GHOSTTY_SUCCESS) return rc;
    for (size_t i = 0; i < 16; i++) palette[i] = attn_rgb(colors[i]);
    rc = ghostty_terminal_set(attn->terminal,
                              GHOSTTY_TERMINAL_OPT_COLOR_PALETTE, palette);
    if (rc != GHOSTTY_SUCCESS) return rc;
  }
  return GHOSTTY_SUCCESS;
}

static uint8_t *attn_copy_bytes(const uint8_t *data, size_t len) {
  if (len == 0) return NULL;
  uint8_t *copy = malloc(len);
  if (copy != NULL) memcpy(copy, data, len);
  return copy;
}

uint8_t *attn_ghostty_drain_responses(AttnGhosttyTerminal *attn,
                                      size_t *out_len) {
  if (out_len == NULL) return NULL;
  *out_len = 0;
  if (attn == NULL || attn->responses_len == 0) return NULL;
  uint8_t *copy = attn_copy_bytes(attn->responses, attn->responses_len);
  if (copy == NULL) return NULL;
  *out_len = attn->responses_len;
  attn->responses_len = 0;
  return copy;
}

uint8_t *attn_ghostty_snapshot(AttnGhosttyTerminal *attn, size_t *out_len) {
  if (out_len == NULL) return NULL;
  *out_len = 0;
  if (attn == NULL) return NULL;
  uint8_t *raw = NULL;
  size_t len = 0;
  if (ghostty_snapshot_encode_alloc(attn->terminal, NULL, &raw, &len) !=
      GHOSTTY_SUCCESS) return NULL;
  uint8_t *copy = attn_copy_bytes(raw, len);
  ghostty_free(NULL, raw, len);
  if (copy == NULL && len != 0) return NULL;
  *out_len = len;
  return copy;
}

static GhosttyFormatterTerminalOptions attn_formatter_options(
    GhosttyFormatterFormat format) {
  GhosttyFormatterTerminalOptions options;
  memset(&options, 0, sizeof(options));
  options.size = sizeof(options);
  options.emit = format;
  options.unwrap = false;
  options.trim = false;
  options.extra.size = sizeof(options.extra);
  options.extra.palette = true;
  options.extra.modes = true;
  options.extra.scrolling_region = true;
  options.extra.tabstops = true;
  options.extra.pwd = true;
  options.extra.keyboard = true;
  options.extra.screen.size = sizeof(options.extra.screen);
  options.extra.screen.cursor = true;
  options.extra.screen.style = true;
  options.extra.screen.hyperlink = true;
  options.extra.screen.protection = true;
  options.extra.screen.kitty_keyboard = true;
  options.extra.screen.charsets = true;
  return options;
}

static GhosttyPoint attn_viewport_point(uint16_t x, uint32_t y) {
  return attn_point(GHOSTTY_POINT_TAG_VIEWPORT, x, y);
}

static uint8_t *attn_ghostty_format_viewport(AttnGhosttyTerminal *attn,
                                             GhosttyFormatterFormat format,
                                             size_t *out_len) {
  if (out_len == NULL) return NULL;
  *out_len = 0;
  if (attn == NULL || attn->cols == 0 || attn->rows == 0) return NULL;
  GhosttyGridRef start, end;
  if (ghostty_terminal_grid_ref(attn->terminal, attn_viewport_point(0, 0),
                                &start) != GHOSTTY_SUCCESS) return NULL;
  if (ghostty_terminal_grid_ref(
          attn->terminal,
          attn_viewport_point(attn->cols - 1, attn->rows - 1),
          &end) != GHOSTTY_SUCCESS) return NULL;
  GhosttySelection selection;
  memset(&selection, 0, sizeof(selection));
  selection.size = sizeof(selection);
  selection.start = start;
  selection.end = end;
  GhosttyFormatterTerminalOptions options = attn_formatter_options(format);
  options.selection = &selection;
  GhosttyFormatter formatter;
  if (ghostty_formatter_terminal_new(NULL, &formatter, attn->terminal,
                                     options) != GHOSTTY_SUCCESS) return NULL;
  uint8_t *raw = NULL;
  size_t len = 0;
  GhosttyResult rc = ghostty_formatter_format_alloc(formatter, NULL, &raw, &len);
  ghostty_formatter_free(formatter);
  if (rc != GHOSTTY_SUCCESS) return NULL;
  uint8_t *copy = attn_copy_bytes(raw, len);
  ghostty_free(NULL, raw, len);
  if (copy == NULL && len != 0) return NULL;
  *out_len = len;
  return copy;
}

static uint8_t *attn_ghostty_format(AttnGhosttyTerminal *attn,
                                    GhosttyFormatterFormat format,
                                    size_t *out_len) {
  if (out_len == NULL) return NULL;
  *out_len = 0;
  if (attn == NULL) return NULL;
  GhosttyFormatter formatter;
  GhosttyFormatterTerminalOptions options = attn_formatter_options(format);
  if (ghostty_formatter_terminal_new(NULL, &formatter, attn->terminal,
                                     options) != GHOSTTY_SUCCESS) return NULL;
  uint8_t *raw = NULL;
  size_t len = 0;
  GhosttyResult rc = ghostty_formatter_format_alloc(formatter, NULL, &raw, &len);
  ghostty_formatter_free(formatter);
  if (rc != GHOSTTY_SUCCESS) return NULL;
  uint8_t *copy = attn_copy_bytes(raw, len);
  ghostty_free(NULL, raw, len);
  if (copy == NULL && len != 0) return NULL;
  *out_len = len;
  return copy;
}

uint8_t *attn_ghostty_vt_dump(AttnGhosttyTerminal *attn, size_t *out_len) {
  return attn_ghostty_format(attn, GHOSTTY_FORMATTER_FORMAT_VT, out_len);
}

uint8_t *attn_ghostty_plain_text(AttnGhosttyTerminal *attn, size_t *out_len) {
  return attn_ghostty_format(attn, GHOSTTY_FORMATTER_FORMAT_PLAIN, out_len);
}

uint8_t *attn_ghostty_viewport_vt(AttnGhosttyTerminal *attn,
                                  size_t *out_len) {
  return attn_ghostty_format_viewport(attn, GHOSTTY_FORMATTER_FORMAT_VT,
                                      out_len);
}

uint8_t *attn_ghostty_viewport_text(AttnGhosttyTerminal *attn,
                                    size_t *out_len) {
  return attn_ghostty_format_viewport(attn, GHOSTTY_FORMATTER_FORMAT_PLAIN,
                                      out_len);
}

typedef struct {
  uint32_t image_id;
  uint32_t placement_id;
  uint8_t is_virtual;
  int32_t z;
  uint32_t pixel_width;
  uint32_t pixel_height;
  uint32_t grid_cols;
  uint32_t grid_rows;
  int32_t viewport_col;
  int32_t viewport_row;
  uint8_t viewport_visible;
  uint32_t source_x;
  uint32_t source_y;
  uint32_t source_width;
  uint32_t source_height;
  uint64_t image_generation;
} AttnGhosttyPlacement;

static GhosttyKittyGraphics attn_kitty_storage(AttnGhosttyTerminal *attn) {
  if (attn == NULL) return NULL;
  GhosttyKittyGraphics graphics = NULL;
  if (ghostty_terminal_get(attn->terminal,
                           GHOSTTY_TERMINAL_DATA_KITTY_GRAPHICS,
                           &graphics) != GHOSTTY_SUCCESS) return NULL;
  return graphics;
}

uint64_t attn_ghostty_kitty_generation(AttnGhosttyTerminal *attn) {
  GhosttyKittyGraphics graphics = attn_kitty_storage(attn);
  if (graphics == NULL) return 0;
  uint64_t value = 0;
  if (ghostty_kitty_graphics_get(graphics,
                                 GHOSTTY_KITTY_GRAPHICS_DATA_GENERATION,
                                 &value) != GHOSTTY_SUCCESS) return 0;
  return value;
}

size_t attn_ghostty_kitty_placements(AttnGhosttyTerminal *attn,
                                     AttnGhosttyPlacement **out) {
  if (out == NULL) return 0;
  *out = NULL;
  GhosttyKittyGraphics graphics = attn_kitty_storage(attn);
  if (graphics == NULL) return 0;
  GhosttyKittyGraphicsPlacementIterator iterator = NULL;
  if (ghostty_kitty_graphics_placement_iterator_new(NULL, &iterator) !=
      GHOSTTY_SUCCESS) return 0;
  if (ghostty_kitty_graphics_get(
          graphics, GHOSTTY_KITTY_GRAPHICS_DATA_PLACEMENT_ITERATOR,
          &iterator) != GHOSTTY_SUCCESS) {
    ghostty_kitty_graphics_placement_iterator_free(iterator);
    return 0;
  }
  AttnGhosttyPlacement *records = NULL;
  size_t len = 0, cap = 0;
  while (ghostty_kitty_graphics_placement_next(iterator)) {
    uint32_t image_id = 0, placement_id = 0;
    bool is_virtual = false;
    int32_t z = 0;
    const GhosttyKittyGraphicsPlacementData keys[4] = {
        GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_IMAGE_ID,
        GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_PLACEMENT_ID,
        GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_IS_VIRTUAL,
        GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_Z,
    };
    void *values[4] = {&image_id, &placement_id, &is_virtual, &z};
    if (ghostty_kitty_graphics_placement_get_multi(
            iterator, 4, keys, values, NULL) != GHOSTTY_SUCCESS) continue;
    GhosttyKittyGraphicsImage image =
        ghostty_kitty_graphics_image(graphics, image_id);
    if (image == NULL) continue;
    uint64_t generation = 0;
    ghostty_kitty_graphics_image_get(
        image, GHOSTTY_KITTY_IMAGE_DATA_GENERATION, &generation);
    GhosttyKittyGraphicsPlacementRenderInfo info;
    memset(&info, 0, sizeof(info));
    info.size = sizeof(info);
    if (ghostty_kitty_graphics_placement_render_info(
            iterator, image, attn->terminal, &info) != GHOSTTY_SUCCESS) continue;
    if (len == cap) {
      size_t next_cap = cap == 0 ? 4 : cap * 2;
      AttnGhosttyPlacement *next =
          realloc(records, next_cap * sizeof(*records));
      if (next == NULL) break;
      records = next;
      cap = next_cap;
    }
    records[len++] = (AttnGhosttyPlacement){
        .image_id = image_id,
        .placement_id = placement_id,
        .is_virtual = is_virtual ? 1 : 0,
        .z = z,
        .pixel_width = info.pixel_width,
        .pixel_height = info.pixel_height,
        .grid_cols = info.grid_cols,
        .grid_rows = info.grid_rows,
        .viewport_col = info.viewport_col,
        .viewport_row = info.viewport_row,
        .viewport_visible = info.viewport_visible ? 1 : 0,
        .source_x = info.source_x,
        .source_y = info.source_y,
        .source_width = info.source_width,
        .source_height = info.source_height,
        .image_generation = generation,
    };
  }
  ghostty_kitty_graphics_placement_iterator_free(iterator);
  if (len == 0) {
    free(records);
    return 0;
  }
  *out = records;
  return len;
}

typedef struct {
  uint32_t width;
  uint32_t height;
  int32_t format;
  int32_t compression;
  const uint8_t *data;
  size_t data_len;
  uint64_t generation;
} AttnGhosttyImage;

bool attn_ghostty_kitty_image(AttnGhosttyTerminal *attn, uint32_t image_id,
                              AttnGhosttyImage *out) {
  if (out == NULL) return false;
  memset(out, 0, sizeof(*out));
  GhosttyKittyGraphics graphics = attn_kitty_storage(attn);
  if (graphics == NULL) return false;
  GhosttyKittyGraphicsImage image =
      ghostty_kitty_graphics_image(graphics, image_id);
  if (image == NULL) return false;
  const GhosttyKittyGraphicsImageData keys[7] = {
      GHOSTTY_KITTY_IMAGE_DATA_WIDTH,
      GHOSTTY_KITTY_IMAGE_DATA_HEIGHT,
      GHOSTTY_KITTY_IMAGE_DATA_FORMAT,
      GHOSTTY_KITTY_IMAGE_DATA_COMPRESSION,
      GHOSTTY_KITTY_IMAGE_DATA_DATA_PTR,
      GHOSTTY_KITTY_IMAGE_DATA_DATA_LEN,
      GHOSTTY_KITTY_IMAGE_DATA_GENERATION,
  };
  void *values[7] = {&out->width, &out->height, &out->format,
                     &out->compression, (void *)&out->data, &out->data_len,
                     &out->generation};
  return ghostty_kitty_graphics_image_get_multi(image, 7, keys, values, NULL) ==
         GHOSTTY_SUCCESS;
}

void attn_ghostty_bytes_free(uint8_t *data) { free(data); }
