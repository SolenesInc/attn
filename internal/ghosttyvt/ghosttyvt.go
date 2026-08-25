//go:build (darwin && arm64) || (linux && amd64) || (linux && arm64)

// Design: docs/plans/2026-07-22-server-authoritative-terminal.md
package ghosttyvt

/*
// Per-platform archive + headers (download-first; see the build script). The
// four macOS frameworks are darwin-only over-linking Ghostty's build pulls in;
// the Linux targets link only the self-contained .a (its C/C++ runtime deps
// from simdutf/highway are statically baked in) plus libc/libm/libpthread.
#cgo darwin,arm64 CFLAGS: -I${SRCDIR}/../../third_party/ghostty-vt/darwin_arm64/include
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/../../third_party/ghostty-vt/darwin_arm64/lib/libghostty-vt.a
#cgo darwin,arm64 LDFLAGS: -framework CoreFoundation -framework CoreText -framework CoreGraphics -framework Foundation
#cgo linux,amd64 CFLAGS: -I${SRCDIR}/../../third_party/ghostty-vt/linux_amd64/include
#cgo linux,amd64 LDFLAGS: ${SRCDIR}/../../third_party/ghostty-vt/linux_amd64/lib/libghostty-vt.a
#cgo linux,arm64 CFLAGS: -I${SRCDIR}/../../third_party/ghostty-vt/linux_arm64/include
#cgo linux,arm64 LDFLAGS: ${SRCDIR}/../../third_party/ghostty-vt/linux_arm64/lib/libghostty-vt.a
#cgo linux LDFLAGS: -lm -lpthread
#include <stdlib.h>
#include <string.h>
#include <ghostty/vt.h>

// Implemented in callback.go; the terminal invokes it synchronously during
// vt_write with query-response bytes (CPR, DA1, kitty CSI ? u, DECRQM…).
extern void goWritePty(GhosttyTerminal term, void* userdata, const uint8_t* data, size_t len);

// Install the userdata pointer + write_pty callback in one shot. userdata is the
// address of the sink's cgo.Handle field. ghostty_terminal_set retains it past
// this call, so the caller pins that address with a runtime.Pinner (held until
// after ghostty_terminal_free) — the supported way for C to legally retain a Go
// pointer. The callback dereferences it back to a cgo.Handle.
static GhosttyResult ghosttyvt_install(GhosttyTerminal t, void* userdata) {
	GhosttyResult rc = ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_USERDATA, userdata);
	if (rc != GHOSTTY_SUCCESS) return rc;
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_WRITE_PTY, (const void*)goWritePty);
}

// Set the kitty image storage limit. ghostty_terminal_set reads the value
// synchronously, so the caller's stack local need not outlive the call.
static GhosttyResult ghosttyvt_set_kitty_limit(GhosttyTerminal t, uint64_t v) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_STORAGE_LIMIT, &v);
}

// Scrollback is a construction-time concern for us but a post-construction
// option upstream, so it is set immediately after ghostty_terminal_new.
static GhosttyResult ghosttyvt_set_max_scrollback(GhosttyTerminal t, uint64_t v) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, &v);
}

// Retain the bytes of an unfinished sequence so a snapshot taken mid-sequence
// can restore the parser. Tracking is off by default, and encoding a terminal
// whose parser is not at ground fails without it.
static GhosttyResult ghosttyvt_set_continuation(GhosttyTerminal t, size_t v) {
	return ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES, &v);
}

static GhosttyResult ghosttyvt_snapshot_encode(GhosttyTerminal t, uint8_t **ptr, size_t *n) {
	return ghostty_snapshot_encode_alloc(t, NULL, ptr, n);
}

// Decode a whole snapshot — READY prefix and every history page — into a fresh
// terminal. The decoder borrows the source bytes, so it is freed before this
// returns and the caller's buffer need not outlive the call.
static GhosttyResult ghosttyvt_snapshot_decode(const uint8_t *ptr, size_t n, GhosttyTerminal *out) {
	GhosttySnapshotDecoder d;
	GhosttyResult rc = ghostty_snapshot_decoder_new_buf(NULL, &d, ptr, n);
	if (rc != GHOSTTY_SUCCESS) return rc;
	rc = ghostty_snapshot_decoder_decode(d, out);
	ghostty_snapshot_decoder_free(d);
	return rc;
}

static GhosttyColorRgb ghosttyvt_rgb(uint32_t value) {
	GhosttyColorRgb color = {
		.r = (uint8_t)(value >> 16),
		.g = (uint8_t)(value >> 8),
		.b = (uint8_t)value,
	};
	return color;
}

// Set embedder defaults while preserving any program-owned OSC overrides.
// Ghostty's palette setter takes all 256 entries, so begin with its current
// default palette and replace only the 16 ANSI colors attn configures.
static GhosttyResult ghosttyvt_set_color_theme(
	GhosttyTerminal t,
	bool has_foreground, uint32_t foreground,
	bool has_background, uint32_t background,
	bool has_cursor, uint32_t cursor,
	bool has_ansi_palette, const uint32_t* ansi_palette
) {
	GhosttyResult rc;
	if (has_foreground) {
		GhosttyColorRgb color = ghosttyvt_rgb(foreground);
		rc = ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND, &color);
		if (rc != GHOSTTY_SUCCESS) return rc;
	}
	if (has_background) {
		GhosttyColorRgb color = ghosttyvt_rgb(background);
		rc = ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND, &color);
		if (rc != GHOSTTY_SUCCESS) return rc;
	}
	if (has_cursor) {
		GhosttyColorRgb color = ghosttyvt_rgb(cursor);
		rc = ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_COLOR_CURSOR, &color);
		if (rc != GHOSTTY_SUCCESS) return rc;
	}
	if (has_ansi_palette) {
		GhosttyColorRgb palette[256];
		rc = ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_COLOR_PALETTE_DEFAULT, palette);
		if (rc != GHOSTTY_SUCCESS) return rc;
		for (size_t i = 0; i < 16; i++) palette[i] = ghosttyvt_rgb(ansi_palette[i]);
		rc = ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_COLOR_PALETTE, palette);
		if (rc != GHOSTTY_SUCCESS) return rc;
	}
	return GHOSTTY_SUCCESS;
}

// Build formatter options: one self-contained VT (or plain) stream with all
// "extra" state on and unwrap=false so soft-wrap survives the dump. NULL
// selection = the entire screen including scrollback history.
static GhosttyFormatterTerminalOptions ghosttyvt_make_opts(GhosttyFormatterFormat emit) {
	GhosttyFormatterTerminalOptions o;
	memset(&o, 0, sizeof(o));
	o.size = sizeof(GhosttyFormatterTerminalOptions);
	o.emit = emit;
	o.unwrap = false;
	o.trim = false;
	o.extra.size = sizeof(GhosttyFormatterTerminalExtra);
	o.extra.palette = true;
	o.extra.modes = true;
	o.extra.scrolling_region = true;
	o.extra.tabstops = true;
	o.extra.pwd = true;
	o.extra.keyboard = true;
	o.extra.screen.size = sizeof(GhosttyFormatterScreenExtra);
	o.extra.screen.cursor = true;
	o.extra.screen.style = true;
	o.extra.screen.hyperlink = true;
	o.extra.screen.protection = true;
	o.extra.screen.kitty_keyboard = true;
	o.extra.screen.charsets = true;
	o.selection = NULL;
	return o;
}

static size_t ghosttyvt_get_usize(GhosttyTerminal t, GhosttyTerminalData data) {
	size_t v = 0;
	ghostty_terminal_get(t, data, &v);
	return v;
}

static uint16_t ghosttyvt_get_u16(GhosttyTerminal t, GhosttyTerminalData data) {
	uint16_t v = 0;
	ghostty_terminal_get(t, data, &v);
	return v;
}

static int ghosttyvt_active_screen(GhosttyTerminal t) {
	GhosttyTerminalScreen s = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
	ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &s);
	return (int)s;
}

// Modes are read through the terminal's data query: `mode` goes in, `value`
// comes back. False on a failed read is the safe answer everywhere it is used
// below, so the failure is folded into the helper.
static bool ghosttyvt_mode(GhosttyTerminal t, GhosttyMode mode) {
	GhosttyTerminalModeConfig cfg;
	memset(&cfg, 0, sizeof(cfg));
	cfg.mode = mode;
	if (ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_MODE, &cfg) != GHOSTTY_SUCCESS) return false;
	return cfg.value;
}

static bool ghosttyvt_cursor_visible(GhosttyTerminal t) {
	return ghosttyvt_mode(t, GHOSTTY_MODE_CURSOR_VISIBLE);
}

// DEC wraparound (mode 7, DECAWM). False on a failed read is the safe answer:
// the caller then resizes plainly instead of toggling a mode it could not read,
// so a failure can never leave wraparound enabled behind the program's back.
static bool ghosttyvt_wraparound(GhosttyTerminal t) {
	return ghosttyvt_mode(t, GHOSTTY_MODE_WRAPAROUND);
}

// Left/right margin mode (DECLRMM, DEC private mode 69). False on a failed read
// leaves the caller trusting its own scroll measurement, which is what a
// terminal without margins earns anyway.
static bool ghosttyvt_left_right_margin_mode(GhosttyTerminal t) {
	return ghosttyvt_mode(t, GHOSTTY_MODE_LEFT_RIGHT_MARGIN);
}

static GhosttyPoint ghosttyvt_viewport_point(uint16_t x, uint32_t y) {
	GhosttyPoint p;
	memset(&p, 0, sizeof(p));
	p.tag = GHOSTTY_POINT_TAG_VIEWPORT;
	p.value.coordinate.x = x;
	p.value.coordinate.y = y;
	return p;
}

static GhosttyResult ghosttyvt_format_viewport(
	GhosttyTerminal t,
	uint16_t cols,
	uint16_t rows,
	GhosttyFormatterFormat emit,
	uint8_t** out_ptr,
	size_t* out_len
) {
	GhosttyGridRef start;
	GhosttyGridRef end;
	GhosttySelection selection;
	GhosttyFormatter formatter;
	GhosttyFormatterTerminalOptions opts;
	GhosttyResult rc;

	if (cols == 0 || rows == 0) return GHOSTTY_INVALID_VALUE;
	if ((rc = ghostty_terminal_grid_ref(t, ghosttyvt_viewport_point(0, 0), &start)) != GHOSTTY_SUCCESS) return rc;
	if ((rc = ghostty_terminal_grid_ref(t, ghosttyvt_viewport_point(cols - 1, rows - 1), &end)) != GHOSTTY_SUCCESS) return rc;

	memset(&selection, 0, sizeof(selection));
	selection.size = sizeof(selection);
	selection.start = start;
	selection.end = end;
	selection.rectangle = false;

	opts = ghosttyvt_make_opts(emit);
	opts.selection = &selection;
	if ((rc = ghostty_formatter_terminal_new(NULL, &formatter, t, opts)) != GHOSTTY_SUCCESS) return rc;
	rc = ghostty_formatter_format_alloc(formatter, NULL, out_ptr, out_len);
	ghostty_formatter_free(formatter);
	return rc;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"runtime/cgo"
	"strings"
	"sync"
	"unsafe"
)

const (
	defaultCellWidthPx  = 8
	defaultCellHeightPx = 16
)

// Ghostty budgets scrollback in bytes, not rows: measured at 200 columns, 4MB
// held 1,955 rows and 16MB held 9,095 (~1.8KB/row), so this is ~4,400 rows.
const DefaultScrollbackBytes = 8 << 20

// Ghostty's own kitty APC buffer limit; the snapshot decoder defaults to the
// same number.
const ContinuationMaxBytes = 65 << 20

type Options struct {
	ScrollbackBytes int

	// Zero disables the kitty protocol; it is not a "use the default" sentinel.
	KittyImageStorageLimit uint64
}

type Snapshot struct {
	Cols, Rows int
	Payload    []byte
	VTDump     []byte
}

// The cgo.Handle references the sink, NOT the Terminal, so the Terminal's
// finalizer still runs.
type respSink struct {
	mu     sync.Mutex
	buf    []byte
	handle cgo.Handle
}

type Terminal struct {
	mu     sync.Mutex
	term   C.GhosttyTerminal
	sink   *respSink
	pinner runtime.Pinner
	cols   int
	rows   int
	cellW  int
	cellH  int

	closed bool
}

func New(cols, rows int, opts Options) (*Terminal, error) {
	if cols <= 0 || rows <= 0 {
		return nil, fmt.Errorf("ghosttyvt: invalid size %dx%d", cols, rows)
	}
	maxSB := opts.ScrollbackBytes
	if maxSB <= 0 {
		maxSB = DefaultScrollbackBytes
	}
	// Process-global, idempotent; without it ghostty rejects every PNG (f=100).
	installPNGDecoder()
	t := &Terminal{
		cols:  cols,
		rows:  rows,
		cellW: defaultCellWidthPx,
		cellH: defaultCellHeightPx,
		sink:  &respSink{},
	}
	if rc := C.ghostty_terminal_new(nil, &t.term, C.uint16_t(cols), C.uint16_t(rows)); rc != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("ghosttyvt: terminal_new failed: rc=%d", int(rc))
	}
	return t.configure(maxSB, opts)
}

func Restore(payload []byte, opts Options) (*Terminal, error) {
	if len(payload) == 0 {
		return nil, errors.New("ghosttyvt: empty snapshot")
	}
	maxSB := opts.ScrollbackBytes
	if maxSB <= 0 {
		maxSB = DefaultScrollbackBytes
	}
	installPNGDecoder()
	t := &Terminal{
		cellW: defaultCellWidthPx,
		cellH: defaultCellHeightPx,
		sink:  &respSink{},
	}
	if rc := C.ghosttyvt_snapshot_decode(
		(*C.uint8_t)(unsafe.Pointer(&payload[0])), C.size_t(len(payload)), &t.term,
	); rc != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("ghosttyvt: snapshot decode failed: rc=%d", int(rc))
	}
	t.cols = int(C.ghosttyvt_get_u16(t.term, C.GHOSTTY_TERMINAL_DATA_COLS))
	t.rows = int(C.ghosttyvt_get_u16(t.term, C.GHOSTTY_TERMINAL_DATA_ROWS))
	return t.configure(maxSB, opts)
}

func (t *Terminal) configure(maxSB int, opts Options) (*Terminal, error) {
	if rc := C.ghosttyvt_set_max_scrollback(t.term, C.uint64_t(maxSB)); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_terminal_free(t.term)
		return nil, fmt.Errorf("ghosttyvt: set max scrollback failed: rc=%d", int(rc))
	}
	// Written even when zero: zero overrides the library's 10MB default.
	if rc := C.ghosttyvt_set_kitty_limit(t.term, C.uint64_t(opts.KittyImageStorageLimit)); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_terminal_free(t.term)
		return nil, fmt.Errorf("ghosttyvt: set kitty image storage limit failed: rc=%d", int(rc))
	}
	// Enabled from the first byte: tracking cannot reconstruct a sequence already
	// in flight, and encoding mid-sequence fails without it.
	if rc := C.ghosttyvt_set_continuation(t.term, C.size_t(ContinuationMaxBytes)); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_terminal_free(t.term)
		return nil, fmt.Errorf("ghosttyvt: set continuation tracking failed: rc=%d", int(rc))
	}
	// C retains &sink.handle past this call, so it stays pinned until Close.
	t.sink.handle = cgo.NewHandle(t.sink)
	t.pinner.Pin(&t.sink.handle)
	if rc := C.ghosttyvt_install(t.term, unsafe.Pointer(&t.sink.handle)); rc != C.GHOSTTY_SUCCESS {
		C.ghostty_terminal_free(t.term)
		t.pinner.Unpin()
		t.sink.handle.Delete()
		return nil, fmt.Errorf("ghosttyvt: install callbacks failed: rc=%d", int(rc))
	}
	runtime.SetFinalizer(t, (*Terminal).finalize)
	return t, nil
}

func (t *Terminal) Write(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writeLocked(p)
}

func (t *Terminal) SetColorTheme(theme ColorTheme) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	palette := [16]C.uint32_t{}
	for i, color := range theme.ANSIPalette {
		palette[i] = C.uint32_t(color)
	}
	rc := C.ghosttyvt_set_color_theme(
		t.term,
		C.bool(theme.HasForeground), C.uint32_t(theme.Foreground),
		C.bool(theme.HasBackground), C.uint32_t(theme.Background),
		C.bool(theme.HasCursor), C.uint32_t(theme.Cursor),
		C.bool(theme.HasANSIPalette), &palette[0],
	)
	if rc != C.GHOSTTY_SUCCESS {
		return fmt.Errorf("ghosttyvt: set color theme failed: rc=%d", int(rc))
	}
	return nil
}

func (t *Terminal) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resizeLocked(cols, rows)
}

var (
	disableWraparound = []byte("\x1b[?7l")
	enableWraparound  = []byte("\x1b[?7h")
)

// Must mirror the client's resizeGhosttyWithoutReflow
// (app/src/utils/ghosttyResize.ts): worker and client grids stay frame-equal.
func (t *Terminal) ResizeNoReflow(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	if !bool(C.ghosttyvt_wraparound(t.term)) {
		t.resizeLocked(cols, rows)
		return
	}
	// Held across all three steps: an interleaved write would parse wraparound-off.
	t.writeLocked(disableWraparound)
	defer t.writeLocked(enableWraparound)
	t.resizeLocked(cols, rows)
}

func (t *Terminal) writeLocked(p []byte) {
	if len(p) == 0 || t.closed {
		return
	}
	C.ghostty_terminal_vt_write(t.term, (*C.uint8_t)(unsafe.Pointer(&p[0])), C.size_t(len(p)))
}

func (t *Terminal) SetCellPixelSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || (t.cellW == w && t.cellH == h) {
		return
	}
	t.cellW, t.cellH = w, h
	t.resizeLocked(t.cols, t.rows)
}

func (t *Terminal) resizeLocked(cols, rows int) {
	if t.closed {
		return
	}
	C.ghostty_terminal_resize(t.term, C.uint16_t(cols), C.uint16_t(rows), C.uint32_t(t.cellW), C.uint32_t(t.cellH))
	t.cols, t.rows = cols, rows
}

// Sink lock only: the write path takes sink.mu under t.mu, so taking t.mu here
// would deadlock.
func (t *Terminal) DrainResponses() []byte {
	s := t.sink
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) == 0 {
		return nil
	}
	out := s.buf
	s.buf = nil
	return out
}

func (t *Terminal) Size() (cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols, t.rows
}

func (t *Terminal) PlainText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ""
	}
	return string(t.format(C.GHOSTTY_FORMATTER_FORMAT_PLAIN))
}

func (t *Terminal) CursorPos() (x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, 0
	}
	return t.cursorXYLocked()
}

func (t *Terminal) CursorVisible() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed && bool(C.ghosttyvt_cursor_visible(t.term))
}

func (t *Terminal) LeftRightMarginMode() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed && bool(C.ghosttyvt_left_right_margin_mode(t.term))
}

func (t *Terminal) ViewportText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ""
	}
	return t.viewportTextLocked()
}

func (t *Terminal) SerializeViewport() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return Snapshot{Cols: t.cols, Rows: t.rows}
	}

	dump, ok := t.formatViewport(C.GHOSTTY_FORMATTER_FORMAT_VT)
	if !ok {
		return Snapshot{Cols: t.cols, Rows: t.rows}
	}

	return Snapshot{Cols: t.cols, Rows: t.rows, VTDump: t.appendCursorLocked(dump)}
}

// The formatter emits its own cursor CUP before the tabstop resets, so this
// has to go last.
func (t *Terminal) appendCursorLocked(dump []byte) []byte {
	cx, cy := t.cursorXYLocked()
	dump = fmt.Appendf(dump, "\x1b[%d;%dH", cy+1, cx+1)
	if C.ghosttyvt_cursor_visible(t.term) {
		return append(dump, "\x1b[?25h"...)
	}
	return append(dump, "\x1b[?25l"...)
}

// CONSUMES the terminal: reaching the primary screen destroys the alternate
// screen's contents. Call it only on a terminal about to be discarded.
func (t *Terminal) HandoffVT() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return Snapshot{Cols: t.cols, Rows: t.rows}
	}

	alt := C.ghosttyvt_active_screen(t.term) == C.int(C.GHOSTTY_TERMINAL_SCREEN_ALTERNATE)
	var altDump []byte
	if alt {
		altDump = t.dumpActiveLocked()
		t.writeLocked([]byte("\x1b[?1049l"))
	}

	dump := t.dumpActiveLocked()
	if alt {
		dump = append(dump, "\x1b[?1049h"...)
		dump = append(dump, altDump...)
	}
	return Snapshot{Cols: t.cols, Rows: t.rows, VTDump: dump}
}

func (t *Terminal) dumpActiveLocked() []byte {
	dump := t.format(C.GHOSTTY_FORMATTER_FORMAT_VT)
	if dump == nil {
		return nil
	}

	// The formatter stops at the last non-blank row, so a grid with blank bottom
	// rows replays short and the child then overwrites a row it believes is there.
	if deficit := t.replayDeficitLocked(dump); deficit > 0 {
		dump = fmt.Appendf(dump, "\x1b[%d;1H", t.rows)
		dump = append(dump, strings.Repeat("\r\n", deficit)...)
	}

	return t.appendCursorLocked(dump)
}

func (t *Terminal) replayDeficitLocked(dump []byte) int {
	probe, err := New(t.cols, t.rows, Options{})
	if err != nil {
		return 0
	}
	defer probe.Close()
	probe.Write(dump)
	deficit := int(C.ghosttyvt_get_usize(t.term, C.GHOSTTY_TERMINAL_DATA_TOTAL_ROWS)) - probe.TotalRows()
	if deficit < 0 {
		return 0
	}
	return deficit
}

func (t *Terminal) Serialize() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.serializeLocked()
}

func (t *Terminal) serializeLocked() Snapshot {
	if t.closed {
		return Snapshot{Cols: t.cols, Rows: t.rows}
	}
	return Snapshot{
		Cols:    t.cols,
		Rows:    t.rows,
		Payload: t.encodeSnapshotLocked(),
	}
}

func (t *Terminal) encodeSnapshotLocked() []byte {
	var ptr *C.uint8_t
	var n C.size_t
	if rc := C.ghosttyvt_snapshot_encode(t.term, &ptr, &n); rc != C.GHOSTTY_SUCCESS {
		return nil
	}
	defer C.ghostty_free(nil, ptr, n)
	if n == 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(ptr), C.int(n))
}

func (t *Terminal) cursorXYLocked() (x, y int) {
	return int(C.ghosttyvt_get_u16(t.term, C.GHOSTTY_TERMINAL_DATA_CURSOR_X)),
		int(C.ghosttyvt_get_u16(t.term, C.GHOSTTY_TERMINAL_DATA_CURSOR_Y))
}

func (t *Terminal) TotalRows() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0
	}
	return int(C.ghosttyvt_get_usize(t.term, C.GHOSTTY_TERMINAL_DATA_TOTAL_ROWS))
}

func (t *Terminal) AltScreenActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	return C.ghosttyvt_active_screen(t.term) == C.int(C.GHOSTTY_TERMINAL_SCREEN_ALTERNATE)
}

func (t *Terminal) format(emit C.GhosttyFormatterFormat) []byte {
	var f C.GhosttyFormatter
	opts := C.ghosttyvt_make_opts(emit)
	if rc := C.ghostty_formatter_terminal_new(nil, &f, t.term, opts); rc != C.GHOSTTY_SUCCESS {
		return nil
	}
	defer C.ghostty_formatter_free(f)
	var ptr *C.uint8_t
	var n C.size_t
	if rc := C.ghostty_formatter_format_alloc(f, nil, &ptr, &n); rc != C.GHOSTTY_SUCCESS {
		return nil
	}
	defer C.ghostty_free(nil, ptr, n)
	if n == 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(ptr), C.int(n))
}

func (t *Terminal) formatViewport(emit C.GhosttyFormatterFormat) ([]byte, bool) {
	var ptr *C.uint8_t
	var n C.size_t
	if rc := C.ghosttyvt_format_viewport(t.term, C.uint16_t(t.cols), C.uint16_t(t.rows), emit, &ptr, &n); rc != C.GHOSTTY_SUCCESS {
		return nil, false
	}
	defer C.ghostty_free(nil, ptr, n)
	if n == 0 {
		return []byte{}, true
	}
	return C.GoBytes(unsafe.Pointer(ptr), C.int(n)), true
}

func (t *Terminal) viewportTextLocked() string {
	raw, ok := t.formatViewport(C.GHOSTTY_FORMATTER_FORMAT_PLAIN)
	if !ok {
		return ""
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var out strings.Builder
	for row := 0; row < t.rows; row++ {
		line := ""
		if row < len(lines) {
			line = strings.TrimRight(lines[row], " ")
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

func (t *Terminal) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	C.ghostty_terminal_free(t.term)
	t.term = nil
	// Unpin only after the native terminal can no longer read the userdata.
	t.pinner.Unpin()
	t.sink.handle.Delete()
	runtime.SetFinalizer(t, nil)
}

func (t *Terminal) finalize() {
	t.Close()
}
