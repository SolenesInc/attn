use std::ffi::c_int;
use std::ptr::NonNull;

use crate::grid::{
    BOLD, Cell, Cursor, CursorStyle, FAINT, Grid, INVISIBLE, ITALIC, Rgb, STRIKETHROUGH, UNDERLINE,
    Width,
};
use crate::theme::Theme;

#[repr(C)]
struct AttnTerm {
    _private: [u8; 0],
}

#[repr(C)]
#[derive(Default)]
struct Frame {
    dirty: u8,
    fg: Rgb,
    bg: Rgb,
    cursor_color: Rgb,
    has_cursor_color: bool,
    cursor_visible: bool,
    cursor_blinking: bool,
    cursor_style: u8,
    cursor_x: u16,
    cursor_y: u16,
    cols: u16,
    rows: u16,
}

#[repr(C)]
#[derive(Default)]
struct RawCell {
    len: u32,
    flags: u16,
    wide: u8,
    has_fg: bool,
    has_bg: bool,
    fg: Rgb,
    bg: Rgb,
}

const SUCCESS: c_int = 0;
const OUT_OF_SPACE: c_int = -3;
const DIRTY_FULL: u8 = 2;
const DIRTY_FALSE: u8 = 0;

unsafe extern "C" {
    fn attn_term_new(cols: u16, rows: u16) -> *mut AttnTerm;
    fn attn_term_restore(data: *const u8, len: usize) -> *mut AttnTerm;
    fn attn_term_snapshot(t: *mut AttnTerm, len: *mut usize) -> *mut u8;
    fn attn_term_bytes_free(data: *mut u8);
    fn attn_term_free(t: *mut AttnTerm);
    fn attn_term_write(t: *mut AttnTerm, data: *const u8, len: usize);
    fn attn_term_resize(t: *mut AttnTerm, cols: u16, rows: u16, cw: u32, ch: u32) -> c_int;
    fn attn_term_size(t: *mut AttnTerm, cols: *mut u16, rows: *mut u16);
    fn attn_term_mode(t: *mut AttnTerm, value: u16, ansi: bool) -> bool;
    fn attn_term_responses(t: *mut AttnTerm, len: *mut usize) -> *const u8;
    fn attn_term_clear_responses(t: *mut AttnTerm);
    fn attn_term_frame(t: *mut AttnTerm, frame: *mut Frame) -> c_int;
    fn attn_term_next_row(t: *mut AttnTerm, dirty: *mut bool) -> bool;
    fn attn_term_row_cells(t: *mut AttnTerm) -> c_int;
    fn attn_term_next_cell(
        t: *mut AttnTerm,
        cell: *mut RawCell,
        codepoints: *mut u32,
        cap: usize,
    ) -> bool;
    fn attn_term_frame_done(t: *mut AttnTerm) -> c_int;
    fn attn_term_set_theme(
        t: *mut AttnTerm,
        fg: *const u8,
        bg: *const u8,
        cursor: *const u8,
        ansi: *const u8,
    ) -> c_int;
    fn attn_term_encode_key(
        t: *mut AttnTerm,
        action: u8,
        key: u32,
        mods: u16,
        consumed: u16,
        utf8: *const u8,
        utf8_len: usize,
        unshifted: u32,
        option_as_alt: u8,
        out: *mut u8,
        cap: usize,
        written: *mut usize,
    ) -> c_int;
}

/// The snapshot format this build decodes, derived by `scripts/snapshot-format.sh` from the
/// same locks the daemon's encoder is built from.
pub const SNAPSHOT_FORMAT: &str = env!("ATTN_SNAPSHOT_FORMAT");

const BRACKETED_PASTE: u16 = 2004;

/// A key as libghostty-vt's encoder takes it: a physical key, its modifiers, the text it typed
/// and the code point it types without Shift.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct KeyInput {
    pub key: u32,
    pub mods: u16,
    pub consumed: u16,
    pub text: String,
    pub unshifted: u32,
    pub option_as_alt: OptionAsAlt,
    pub repeat: bool,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum OptionAsAlt {
    #[default]
    Off = 0,
    Both = 1,
    Left = 2,
    Right = 3,
}

pub mod mods {
    pub const SHIFT: u16 = 1;
    pub const CTRL: u16 = 1 << 1;
    pub const ALT: u16 = 1 << 2;
    pub const SUPER: u16 = 1 << 3;
    pub const ALT_RIGHT: u16 = 1 << 8;
}

/// One terminal's emulator: the daemon's screen restored from a snapshot, then fed its output.
pub struct Vt(NonNull<AttnTerm>);

const WRAPAROUND: u16 = 7;
const KEY_PRESS: u8 = 1;
const KEY_REPEAT: u8 = 2;

// The handle owns its allocations alone and is used from one thread at a time.
unsafe impl Send for Vt {}

impl Vt {
    pub fn new(cols: u16, rows: u16) -> Vt {
        let raw = unsafe { attn_term_new(cols.max(1), rows.max(1)) };
        Vt(NonNull::new(raw).expect("libghostty-vt made a terminal"))
    }

    /// The terminal a snapshot describes, or None when its bytes do not decode.
    pub fn restore(snapshot: &[u8]) -> Option<Vt> {
        let raw = unsafe { attn_term_restore(snapshot.as_ptr(), snapshot.len()) };
        NonNull::new(raw).map(Vt)
    }

    /// The screen as a snapshot, or None while the parser is partway through a sequence.
    pub fn snapshot(&mut self) -> Option<Vec<u8>> {
        let mut len = 0;
        unsafe {
            let raw = attn_term_snapshot(self.0.as_ptr(), &mut len);
            if raw.is_null() {
                return None;
            }
            let bytes = std::slice::from_raw_parts(raw, len).to_vec();
            attn_term_bytes_free(raw);
            Some(bytes)
        }
    }

    /// The colors the terminal draws with until its program sets its own.
    pub fn set_theme(&mut self, theme: &Theme) {
        let ansi = theme.ansi.as_flattened();
        let rc = unsafe {
            attn_term_set_theme(
                self.0.as_ptr(),
                theme.fg.as_ptr(),
                theme.bg.as_ptr(),
                theme.cursor.as_ptr(),
                ansi.as_ptr(),
            )
        };
        assert_eq!(rc, SUCCESS, "libghostty-vt took the theme");
    }

    pub fn write(&mut self, bytes: &[u8]) {
        unsafe { attn_term_write(self.0.as_ptr(), bytes.as_ptr(), bytes.len()) }
    }

    pub fn size(&mut self) -> (u16, u16) {
        let (mut cols, mut rows) = (0, 0);
        unsafe { attn_term_size(self.0.as_ptr(), &mut cols, &mut rows) };
        (cols, rows)
    }

    /// Resizes without reflowing history, as the daemon's own terminal does: the daemon's rows
    /// are the ones its output addresses. Ghostty reflows only under wraparound (DECAWM), so it
    /// is off for the resize.
    pub fn resize(&mut self, cols: u16, rows: u16, cell_px: (u32, u32)) {
        if (cols, rows) == self.size() || cols == 0 || rows == 0 {
            return;
        }
        let wraparound = self.dec_mode(WRAPAROUND);
        if wraparound {
            self.write(b"\x1b[?7l");
        }
        let rc = unsafe { attn_term_resize(self.0.as_ptr(), cols, rows, cell_px.0, cell_px.1) };
        assert_eq!(rc, SUCCESS, "libghostty-vt resized to {cols}x{rows}");
        if wraparound {
            self.write(b"\x1b[?7h");
        }
    }

    /// What the terminal wrote back to the program since the last call: replies to queries.
    pub fn take_responses(&mut self) -> Vec<u8> {
        let mut len = 0;
        unsafe {
            let raw = attn_term_responses(self.0.as_ptr(), &mut len);
            if len == 0 {
                return Vec::new();
            }
            let bytes = std::slice::from_raw_parts(raw, len).to_vec();
            attn_term_clear_responses(self.0.as_ptr());
            bytes
        }
    }

    pub fn dec_mode(&mut self, mode: u16) -> bool {
        unsafe { attn_term_mode(self.0.as_ptr(), mode, false) }
    }

    /// Brings `grid` up to date with the screen, reading only rows that changed, and marks
    /// them dirty. True when anything changed.
    pub fn sync(&mut self, grid: &mut Grid) -> bool {
        let t = self.0.as_ptr();
        let mut frame = Frame::default();
        let rc = unsafe { attn_term_frame(t, &mut frame) };
        assert_eq!(rc, SUCCESS, "libghostty-vt updated its render state");
        let reshaped = (grid.cols, grid.rows) != (frame.cols, frame.rows);
        let recolored = (grid.fg, grid.bg) != (frame.fg, frame.bg);
        let cursor = frame.cursor_visible.then_some(Cursor {
            col: frame.cursor_x,
            row: frame.cursor_y,
            style: match frame.cursor_style {
                0 => CursorStyle::Bar,
                2 => CursorStyle::Underline,
                3 => CursorStyle::BlockHollow,
                _ => CursorStyle::Block,
            },
            color: frame.has_cursor_color.then_some(frame.cursor_color),
        });
        let cursor_moved = cursor != grid.cursor;
        if frame.dirty == DIRTY_FALSE && !reshaped && !cursor_moved {
            return false;
        }
        if reshaped {
            grid.reshape(frame.cols, frame.rows);
        }
        grid.fg = frame.fg;
        grid.bg = frame.bg;
        let full = frame.dirty == DIRTY_FULL || reshaped || recolored;
        let cols = usize::from(frame.cols);
        let mut codepoints = [0u32; 32];
        let mut y = 0usize;
        let mut row_dirty = false;
        while y < usize::from(frame.rows) && unsafe { attn_term_next_row(t, &mut row_dirty) } {
            if row_dirty || full {
                assert_eq!(unsafe { attn_term_row_cells(t) }, SUCCESS);
                let mut x = 0;
                let mut raw = RawCell::default();
                while x < cols
                    && unsafe {
                        attn_term_next_cell(t, &mut raw, codepoints.as_mut_ptr(), codepoints.len())
                    }
                {
                    let grapheme = grid.intern(&codepoints[..raw.len as usize]);
                    grid.cells[y * cols + x] = cell(&raw, grapheme, frame.fg, frame.bg);
                    x += 1;
                }
                for rest in x..cols {
                    grid.cells[y * cols + rest] = Cell {
                        fg: frame.fg,
                        ..Cell::default()
                    };
                }
                grid.dirty[y] = true;
            }
            y += 1;
        }
        if cursor_moved {
            for row in [grid.cursor, cursor].into_iter().flatten().map(|c| c.row) {
                grid.mark(row);
            }
            grid.cursor = cursor;
        }
        unsafe { attn_term_frame_done(t) };
        true
    }

    /// The bytes the program reads for `input`, in the modes the program set.
    pub fn encode_key(&mut self, input: &KeyInput) -> Vec<u8> {
        let mut out = vec![0u8; 64];
        loop {
            let mut written = 0;
            let rc = unsafe {
                attn_term_encode_key(
                    self.0.as_ptr(),
                    if input.repeat { KEY_REPEAT } else { KEY_PRESS },
                    input.key,
                    input.mods,
                    input.consumed,
                    input.text.as_ptr(),
                    input.text.len(),
                    input.unshifted,
                    input.option_as_alt as u8,
                    out.as_mut_ptr(),
                    out.len(),
                    &mut written,
                )
            };
            match rc {
                SUCCESS => {
                    out.truncate(written);
                    return out;
                }
                OUT_OF_SPACE => out.resize(written, 0),
                other => panic!("libghostty-vt failed to encode {input:?}: {other}"),
            }
        }
    }

    /// `text` as a paste reaches the program, as the Tauri app sends it: line breaks become
    /// carriage returns, wrapped in bracketed-paste marks when the program asked for them.
    pub fn encode_paste(&mut self, text: &str) -> Vec<u8> {
        let body = text.replace("\r\n", "\r").replace('\n', "\r");
        if self.dec_mode(BRACKETED_PASTE) {
            format!("\x1b[200~{body}\x1b[201~").into_bytes()
        } else {
            body.into_bytes()
        }
    }
}

impl Drop for Vt {
    fn drop(&mut self) {
        unsafe { attn_term_free(self.0.as_ptr()) }
    }
}

fn cell(raw: &RawCell, grapheme: crate::grid::Grapheme, fg: Rgb, bg: Rgb) -> Cell {
    let style = raw.flags;
    let mut flags = 0;
    for (bit, flag) in [
        (1, BOLD),
        (2, ITALIC),
        (4, FAINT),
        (32, INVISIBLE),
        (64, STRIKETHROUGH),
    ] {
        if style & bit != 0 {
            flags |= flag;
        }
    }
    if (style >> 8) & 7 != 0 {
        flags |= UNDERLINE;
    }
    let mut cell_fg = if raw.has_fg { raw.fg } else { fg };
    let mut cell_bg = raw.has_bg.then_some(raw.bg);
    if style & 16 != 0 {
        let under = cell_bg.unwrap_or(bg);
        cell_bg = Some(cell_fg);
        cell_fg = under;
    }
    Cell {
        grapheme,
        fg: cell_fg,
        bg: cell_bg,
        flags,
        width: match raw.wide {
            1 => Width::Wide,
            2 | 3 => Width::Spacer,
            _ => Width::Narrow,
        },
    }
}
