use std::ffi::c_void;
use std::ptr::NonNull;
use std::sync::OnceLock;

const DEFAULT_SCROLLBACK_BYTES: u64 = 8 << 20;
const DEFAULT_KITTY_BYTES: u64 = 320_000_000;

#[repr(C)]
#[derive(Clone, Copy)]
struct RawPlacement {
    image_id: u32,
    placement_id: u32,
    is_virtual: u8,
    z: i32,
    pixel_width: u32,
    pixel_height: u32,
    grid_cols: u32,
    grid_rows: u32,
    viewport_col: i32,
    viewport_row: i32,
    viewport_visible: u8,
    source_x: u32,
    source_y: u32,
    source_width: u32,
    source_height: u32,
    image_generation: u64,
}

#[repr(C)]
struct RawImage {
    width: u32,
    height: u32,
    format: i32,
    compression: i32,
    data: *const u8,
    data_len: usize,
    generation: u64,
}

#[repr(C)]
struct RawProgramStatus {
    state: u8,
    kind: u8,
    message: *mut u8,
    message_len: usize,
}

unsafe extern "C" {
    fn attn_ghostty_install_png_decoder() -> i32;
    fn attn_ghostty_new(
        cols: u16,
        rows: u16,
        scrollback_bytes: u64,
        kitty_bytes: u64,
    ) -> *mut c_void;
    fn attn_ghostty_restore(
        data: *const u8,
        len: usize,
        scrollback_bytes: u64,
        kitty_bytes: u64,
    ) -> *mut c_void;
    fn attn_ghostty_free(terminal: *mut c_void);
    fn attn_ghostty_write(terminal: *mut c_void, data: *const u8, len: usize);
    fn attn_ghostty_cursor_pos(terminal: *mut c_void, x: *mut u16, y: *mut u16);
    fn attn_ghostty_size(terminal: *mut c_void, cols: *mut u16, rows: *mut u16);
    fn attn_ghostty_alt_screen(terminal: *mut c_void) -> bool;
    fn attn_ghostty_left_right_margin_mode(terminal: *mut c_void) -> bool;
    fn attn_ghostty_wraparound(terminal: *mut c_void) -> bool;
    fn attn_ghostty_cursor_visible(terminal: *mut c_void) -> bool;
    fn attn_ghostty_at_ground(terminal: *mut c_void) -> bool;
    fn attn_ghostty_origin_mode(terminal: *mut c_void) -> bool;
    #[cfg(test)]
    fn attn_ghostty_dec_mode(terminal: *mut c_void, mode: u16) -> bool;
    fn attn_ghostty_track_cursor(terminal: *mut c_void) -> *mut c_void;
    fn attn_ghostty_tracked_screen_point(raw: *mut c_void, x: *mut u16, y: *mut u32) -> bool;
    fn attn_ghostty_tracked_free(raw: *mut c_void);
    fn attn_ghostty_resize(
        terminal: *mut c_void,
        cols: u16,
        rows: u16,
        cell_width: u32,
        cell_height: u32,
    ) -> i32;
    fn attn_ghostty_set_theme(
        terminal: *mut c_void,
        has_foreground: bool,
        foreground: u32,
        has_background: bool,
        background: u32,
        has_cursor: bool,
        cursor: u32,
        has_palette: bool,
        palette: *const u32,
    ) -> i32;
    fn attn_ghostty_drain_responses(terminal: *mut c_void, len: *mut usize) -> *mut u8;
    fn attn_ghostty_drain_program_status(
        terminal: *mut c_void,
        len: *mut usize,
    ) -> *mut RawProgramStatus;
    fn attn_ghostty_program_status_free(reports: *mut RawProgramStatus, len: usize);
    fn attn_ghostty_snapshot(terminal: *mut c_void, len: *mut usize) -> *mut u8;
    fn attn_ghostty_vt_dump(terminal: *mut c_void, len: *mut usize) -> *mut u8;
    fn attn_ghostty_total_rows(terminal: *mut c_void) -> usize;
    fn attn_ghostty_track_screen_point(terminal: *mut c_void, x: u16, y: u32) -> *mut c_void;
    #[cfg(test)]
    fn attn_ghostty_plain_text(terminal: *mut c_void, len: *mut usize) -> *mut u8;
    fn attn_ghostty_viewport_vt(terminal: *mut c_void, len: *mut usize) -> *mut u8;
    fn attn_ghostty_viewport_text(terminal: *mut c_void, len: *mut usize) -> *mut u8;
    fn attn_ghostty_kitty_generation(terminal: *mut c_void) -> u64;
    fn attn_ghostty_kitty_placements(terminal: *mut c_void, out: *mut *mut RawPlacement) -> usize;
    fn attn_ghostty_kitty_image(terminal: *mut c_void, image_id: u32, out: *mut RawImage) -> bool;
    fn attn_ghostty_bytes_free(data: *mut u8);
}

#[derive(Clone, Debug, PartialEq, serde::Serialize)]
pub struct KittyPlacement {
    pub image_id: u32,
    pub placement_id: u32,
    #[serde(rename = "virtual", skip_serializing_if = "is_false")]
    pub is_virtual: bool,
    #[serde(skip_serializing_if = "is_zero_i32")]
    pub z: i32,
    pub pixel_width: u32,
    pub pixel_height: u32,
    pub grid_cols: u32,
    pub grid_rows: u32,
    pub viewport_col: i32,
    pub viewport_row: i32,
    #[serde(skip_serializing_if = "is_false")]
    pub viewport_visible: bool,
    #[serde(skip_serializing_if = "is_zero_u32")]
    pub source_x: u32,
    #[serde(skip_serializing_if = "is_zero_u32")]
    pub source_y: u32,
    #[serde(skip_serializing_if = "is_zero_u32")]
    pub source_width: u32,
    #[serde(skip_serializing_if = "is_zero_u32")]
    pub source_height: u32,
    pub image_generation: u64,
}

#[allow(clippy::trivially_copy_pass_by_ref)]
fn is_zero_i32(value: &i32) -> bool {
    *value == 0
}

#[allow(clippy::trivially_copy_pass_by_ref)]
fn is_false(value: &bool) -> bool {
    !*value
}

#[allow(clippy::trivially_copy_pass_by_ref)]
fn is_zero_u32(value: &u32) -> bool {
    *value == 0
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ProgramState {
    Idle,
    Working,
    Done,
    Blocked,
    Error,
    Clear,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ProgramKind {
    None,
    Permission,
    Question,
    Auth,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ProgramStatus {
    pub state: ProgramState,
    pub kind: ProgramKind,
    pub message: String,
}

impl ProgramStatus {
    fn from_raw(raw: &RawProgramStatus) -> Option<Self> {
        let state = match raw.state {
            0 => ProgramState::Idle,
            1 => ProgramState::Working,
            2 => ProgramState::Done,
            3 => ProgramState::Blocked,
            4 => ProgramState::Error,
            5 => ProgramState::Clear,
            _ => return None,
        };
        let kind = match raw.kind {
            1 => ProgramKind::Permission,
            2 => ProgramKind::Question,
            3 => ProgramKind::Auth,
            _ => ProgramKind::None,
        };
        let message = if raw.message.is_null() || raw.message_len == 0 {
            String::new()
        } else {
            let bytes = unsafe { std::slice::from_raw_parts(raw.message, raw.message_len) };
            String::from_utf8_lossy(bytes).into_owned()
        };
        Some(Self {
            state,
            kind,
            message,
        })
    }
}

pub struct KittyImage {
    pub image_id: u32,
    pub width: u32,
    pub height: u32,
    pub format: &'static str,
    pub generation: u64,
    pub data: Vec<u8>,
}

pub struct TrackedRef {
    raw: NonNull<c_void>,
}

unsafe impl Send for TrackedRef {}
unsafe impl Sync for TrackedRef {}

impl TrackedRef {
    pub fn screen_point(&self) -> Option<(u16, u32)> {
        let mut x = 0;
        let mut y = 0;
        if unsafe { attn_ghostty_tracked_screen_point(self.raw.as_ptr(), &raw mut x, &raw mut y) } {
            Some((x, y))
        } else {
            None
        }
    }
}

impl Drop for TrackedRef {
    fn drop(&mut self) {
        unsafe { attn_ghostty_tracked_free(self.raw.as_ptr()) };
    }
}

pub struct Terminal {
    raw: NonNull<c_void>,
}

// libghostty-vt terminals are only touched while their Session model mutex is held.
unsafe impl Send for Terminal {}

impl Terminal {
    pub fn new(cols: u16, rows: u16) -> Result<Self, String> {
        install_png_decoder()?;
        let kitty_bytes = std::env::var("ATTN_KITTY_STORAGE_LIMIT")
            .ok()
            .and_then(|value| value.trim().parse().ok())
            .unwrap_or(DEFAULT_KITTY_BYTES);
        let raw = unsafe { attn_ghostty_new(cols, rows, DEFAULT_SCROLLBACK_BYTES, kitty_bytes) };
        NonNull::new(raw)
            .map(|raw| Self { raw })
            .ok_or_else(|| format!("libghostty-vt could not create a {cols}x{rows} terminal"))
    }

    pub fn restore(snapshot: &[u8]) -> Result<Self, String> {
        let raw = unsafe {
            attn_ghostty_restore(
                snapshot.as_ptr(),
                snapshot.len(),
                DEFAULT_SCROLLBACK_BYTES,
                DEFAULT_KITTY_BYTES,
            )
        };
        NonNull::new(raw)
            .map(|raw| Self { raw })
            .ok_or_else(|| "libghostty-vt could not restore the snapshot".to_owned())
    }

    pub fn write(&mut self, data: &[u8]) {
        if data.is_empty() {
            return;
        }
        unsafe { attn_ghostty_write(self.raw.as_ptr(), data.as_ptr(), data.len()) };
    }

    pub fn cursor_pos(&self) -> (u16, u16) {
        let mut x = 0;
        let mut y = 0;
        unsafe { attn_ghostty_cursor_pos(self.raw.as_ptr(), &raw mut x, &raw mut y) };
        (x, y)
    }

    pub fn size(&self) -> (u16, u16) {
        let mut cols = 0;
        let mut rows = 0;
        unsafe { attn_ghostty_size(self.raw.as_ptr(), &raw mut cols, &raw mut rows) };
        (cols, rows)
    }

    pub fn alt_screen_active(&self) -> bool {
        unsafe { attn_ghostty_alt_screen(self.raw.as_ptr()) }
    }

    pub fn left_right_margin_mode(&self) -> bool {
        unsafe { attn_ghostty_left_right_margin_mode(self.raw.as_ptr()) }
    }

    pub fn track_cursor(&self) -> Option<TrackedRef> {
        NonNull::new(unsafe { attn_ghostty_track_cursor(self.raw.as_ptr()) })
            .map(|raw| TrackedRef { raw })
    }

    pub fn track_screen_point(&self, x: u16, y: u32) -> Option<TrackedRef> {
        NonNull::new(unsafe { attn_ghostty_track_screen_point(self.raw.as_ptr(), x, y) })
            .map(|raw| TrackedRef { raw })
    }

    pub fn total_rows(&self) -> usize {
        unsafe { attn_ghostty_total_rows(self.raw.as_ptr()) }
    }

    pub fn resize(
        &mut self,
        cols: u16,
        rows: u16,
        cell_width: u32,
        cell_height: u32,
    ) -> Result<(), String> {
        let rc =
            unsafe { attn_ghostty_resize(self.raw.as_ptr(), cols, rows, cell_width, cell_height) };
        if rc == 0 {
            Ok(())
        } else {
            Err(format!("libghostty-vt resize failed with {rc}"))
        }
    }

    pub fn resize_no_reflow(
        &mut self,
        cols: u16,
        rows: u16,
        cell_width: u32,
        cell_height: u32,
    ) -> Result<(), String> {
        if !unsafe { attn_ghostty_wraparound(self.raw.as_ptr()) } {
            return self.resize(cols, rows, cell_width, cell_height);
        }
        self.write(b"\x1b[?7l");
        let result = self.resize(cols, rows, cell_width, cell_height);
        self.write(b"\x1b[?7h");
        result
    }

    pub fn set_theme(&mut self, theme: &Theme) -> Result<(), String> {
        let foreground = parse_color(&theme.foreground);
        let background = parse_color(&theme.background);
        let cursor = parse_color(&theme.cursor);
        let mut palette = [0_u32; 16];
        let has_palette = theme.ansi_palette.len() == 16
            && theme
                .ansi_palette
                .iter()
                .zip(&mut palette)
                .all(|(value, slot)| parse_color(value).map(|v| *slot = v).is_some());
        let rc = unsafe {
            attn_ghostty_set_theme(
                self.raw.as_ptr(),
                foreground.is_some(),
                foreground.unwrap_or_default(),
                background.is_some(),
                background.unwrap_or_default(),
                cursor.is_some(),
                cursor.unwrap_or_default(),
                has_palette,
                palette.as_ptr(),
            )
        };
        if rc == 0 {
            Ok(())
        } else {
            Err(format!("libghostty-vt theme update failed with {rc}"))
        }
    }

    pub fn drain_responses(&mut self) -> Vec<u8> {
        unsafe { take_bytes(|len| attn_ghostty_drain_responses(self.raw.as_ptr(), len)) }
    }

    pub fn drain_program_status(&mut self) -> Vec<ProgramStatus> {
        let mut len = 0_usize;
        let raw = unsafe { attn_ghostty_drain_program_status(self.raw.as_ptr(), &raw mut len) };
        if raw.is_null() {
            return Vec::new();
        }
        let reports = unsafe { std::slice::from_raw_parts(raw, len) }
            .iter()
            .filter_map(ProgramStatus::from_raw)
            .collect();
        unsafe { attn_ghostty_program_status_free(raw, len) };
        reports
    }

    pub fn snapshot(&self) -> Vec<u8> {
        unsafe { take_bytes(|len| attn_ghostty_snapshot(self.raw.as_ptr(), len)) }
    }

    fn vt_dump(&self) -> Vec<u8> {
        unsafe { take_bytes(|len| attn_ghostty_vt_dump(self.raw.as_ptr(), len)) }
    }

    pub fn handover_vt(&self) -> Result<HandoverScreen, String> {
        let mut scratch = Self::restore(&self.snapshot())?;
        if !scratch.alt_screen_active() {
            return Ok(HandoverScreen {
                primary: scratch.dump_active()?,
                alternate: None,
            });
        }
        let alternate = scratch.dump_active()?;
        scratch.write(b"\x1b[?1049l");
        Ok(HandoverScreen {
            primary: scratch.dump_active()?,
            alternate: Some(alternate),
        })
    }

    fn dump_active(&self) -> Result<Vec<u8>, String> {
        let mut dump = self.vt_dump();
        let region = scroll_region(&dump);
        let origin = self.origin_mode();
        dump.extend_from_slice(b"\x1b[r\x1b[?6l");
        let (cols, rows) = self.size();
        let mut probe = Self::new(cols, rows)?;
        probe.write(&dump);
        let deficit = self.total_rows().saturating_sub(probe.total_rows());
        if deficit > 0 {
            dump.extend(format!("\x1b[{rows};1H").as_bytes());
            dump.extend(b"\r\n".repeat(deficit));
        }
        let mut top = 1;
        if let Some((region_top, bottom)) = region {
            dump.extend(format!("\x1b[{region_top};{bottom}r").as_bytes());
            top = region_top;
        }
        let (x, y) = self.cursor_pos();
        let row = if origin {
            dump.extend_from_slice(b"\x1b[?6h");
            (y + 1).saturating_sub(top - 1)
        } else {
            y + 1
        };
        dump.extend(format!("\x1b[{row};{}H", x + 1).as_bytes());
        dump.extend_from_slice(if self.cursor_visible() {
            b"\x1b[?25h"
        } else {
            b"\x1b[?25l"
        });
        Ok(dump)
    }

    pub fn at_ground(&self) -> bool {
        unsafe { attn_ghostty_at_ground(self.raw.as_ptr()) }
    }

    #[cfg(test)]
    pub fn dec_mode(&self, mode: u16) -> bool {
        unsafe { attn_ghostty_dec_mode(self.raw.as_ptr(), mode) }
    }

    fn origin_mode(&self) -> bool {
        unsafe { attn_ghostty_origin_mode(self.raw.as_ptr()) }
    }

    fn cursor_visible(&self) -> bool {
        unsafe { attn_ghostty_cursor_visible(self.raw.as_ptr()) }
    }

    pub fn viewport_vt(&self) -> Vec<u8> {
        let mut bytes =
            unsafe { take_bytes(|len| attn_ghostty_viewport_vt(self.raw.as_ptr(), len)) };
        let (x, y) = self.cursor_pos();
        bytes.extend(format!("\x1b[{};{}H", y + 1, x + 1).as_bytes());
        if unsafe { attn_ghostty_cursor_visible(self.raw.as_ptr()) } {
            bytes.extend_from_slice(b"\x1b[?25h");
        } else {
            bytes.extend_from_slice(b"\x1b[?25l");
        }
        bytes
    }

    #[cfg(test)]
    pub fn plain_text(&self) -> String {
        let bytes = unsafe { take_bytes(|len| attn_ghostty_plain_text(self.raw.as_ptr(), len)) };
        String::from_utf8_lossy(&bytes).into_owned()
    }

    pub fn viewport_text(&self, rows: u16) -> String {
        let bytes = unsafe { take_bytes(|len| attn_ghostty_viewport_text(self.raw.as_ptr(), len)) };
        let text = String::from_utf8_lossy(&bytes);
        let mut lines = text.split('\n').collect::<Vec<_>>();
        if lines.last() == Some(&"") {
            lines.pop();
        }
        let mut result = String::new();
        for row in 0..usize::from(rows) {
            if let Some(line) = lines.get(row) {
                result.push_str(line.trim_end_matches(' '));
            }
            result.push('\n');
        }
        result
    }

    pub fn kitty_generation(&self) -> u64 {
        unsafe { attn_ghostty_kitty_generation(self.raw.as_ptr()) }
    }

    pub fn kitty_placements(&self, epoch: u64) -> Vec<KittyPlacement> {
        let mut raw = std::ptr::null_mut();
        let len = unsafe { attn_ghostty_kitty_placements(self.raw.as_ptr(), &raw mut raw) };
        if raw.is_null() || len == 0 {
            return Vec::new();
        }
        let records = unsafe { std::slice::from_raw_parts(raw, len) };
        let result = records
            .iter()
            .map(|record| KittyPlacement {
                image_id: record.image_id,
                placement_id: record.placement_id,
                is_virtual: record.is_virtual != 0,
                z: record.z,
                pixel_width: record.pixel_width,
                pixel_height: record.pixel_height,
                grid_cols: record.grid_cols,
                grid_rows: record.grid_rows,
                viewport_col: record.viewport_col,
                viewport_row: record.viewport_row,
                viewport_visible: record.viewport_visible != 0,
                source_x: record.source_x,
                source_y: record.source_y,
                source_width: record.source_width,
                source_height: record.source_height,
                image_generation: record.image_generation + epoch,
            })
            .collect();
        unsafe { attn_ghostty_bytes_free(raw.cast()) };
        result
    }

    pub fn kitty_image(&self, image_id: u32, epoch: u64) -> Option<KittyImage> {
        let mut raw = RawImage {
            width: 0,
            height: 0,
            format: 0,
            compression: 0,
            data: std::ptr::null(),
            data_len: 0,
            generation: 0,
        };
        if !unsafe { attn_ghostty_kitty_image(self.raw.as_ptr(), image_id, &raw mut raw) }
            || raw.compression != 0
        {
            return None;
        }
        let format = match raw.format {
            0 => "rgb",
            1 => "rgba",
            3 => "gray_alpha",
            4 => "gray",
            _ => return None,
        };
        let data = if raw.data.is_null() || raw.data_len == 0 {
            Vec::new()
        } else {
            unsafe { std::slice::from_raw_parts(raw.data, raw.data_len) }.to_vec()
        };
        Some(KittyImage {
            image_id,
            width: raw.width,
            height: raw.height,
            format,
            generation: raw.generation + epoch,
            data,
        })
    }
}

fn install_png_decoder() -> Result<(), String> {
    static RESULT: OnceLock<i32> = OnceLock::new();
    let result = *RESULT.get_or_init(|| unsafe { attn_ghostty_install_png_decoder() });
    if result == 0 {
        Ok(())
    } else {
        Err(format!(
            "libghostty-vt PNG decoder install failed with {result}"
        ))
    }
}

impl Drop for Terminal {
    fn drop(&mut self) {
        unsafe { attn_ghostty_free(self.raw.as_ptr()) };
    }
}

unsafe fn take_bytes(call: impl FnOnce(*mut usize) -> *mut u8) -> Vec<u8> {
    let mut len = 0_usize;
    let ptr = call(&raw mut len);
    if ptr.is_null() || len == 0 {
        if !ptr.is_null() {
            unsafe { attn_ghostty_bytes_free(ptr) };
        }
        return Vec::new();
    }
    let bytes = unsafe { std::slice::from_raw_parts(ptr, len) }.to_vec();
    unsafe { attn_ghostty_bytes_free(ptr) };
    bytes
}

fn parse_color(value: &str) -> Option<u32> {
    let value = value.strip_prefix('#')?;
    if value.len() != 6 {
        return None;
    }
    u32::from_str_radix(value, 16).ok()
}

pub struct HandoverScreen {
    pub primary: Vec<u8>,
    pub alternate: Option<Vec<u8>>,
}

fn scroll_region(dump: &[u8]) -> Option<(u16, u16)> {
    let mut found = None;
    let mut rest = dump;
    while let Some(start) = rest.windows(2).position(|pair| pair == b"\x1b[") {
        let params = &rest[start + 2..];
        let end = params
            .iter()
            .position(|byte| !(byte.is_ascii_digit() || *byte == b';'))
            .unwrap_or(params.len());
        if params.get(end) == Some(&b'r') {
            let text = std::str::from_utf8(&params[..end]).unwrap_or_default();
            found = text
                .split_once(';')
                .and_then(|(top, bottom)| Some((top.parse().ok()?, bottom.parse().ok()?)));
        }
        rest = &params[end..];
    }
    found
}

#[derive(Clone, Default, serde::Deserialize, serde::Serialize)]
pub struct Theme {
    #[serde(default)]
    pub foreground: String,
    #[serde(default)]
    pub background: String,
    #[serde(default)]
    pub cursor: String,
    #[serde(default)]
    pub ansi_palette: Vec<String>,
}

#[cfg(test)]
mod tests {
    use super::Terminal;

    #[test]
    fn snapshot_round_trip_keeps_text() {
        let mut terminal = Terminal::new(80, 24).expect("create terminal");
        terminal.write(b"hello from rust\r\n");
        let snapshot = terminal.snapshot();
        assert!(!snapshot.is_empty());
        let restored = Terminal::restore(&snapshot).expect("restore terminal");
        assert!(restored.plain_text().contains("hello from rust"));
    }

    use super::Theme;
    use crate::wire::WireFeeder;

    const COLS: u16 = 40;
    const ROWS: u16 = 10;

    fn screen(output: &[u8]) -> Terminal {
        let mut terminal = Terminal::new(COLS, ROWS).expect("create terminal");
        terminal.write(output);
        terminal
    }

    fn adopt(source: &Terminal, theme: &Theme) -> WireFeeder {
        WireFeeder::adopt(
            &source.handover_vt().expect("handover dump"),
            (COLS, ROWS),
            (0, 0),
            theme,
            &[],
            1,
        )
        .expect("adopt")
    }

    fn handed_over(source: &Terminal) -> WireFeeder {
        adopt(source, &Theme::default())
    }

    fn assert_same_screen(source: &Terminal, replayed: &Terminal, shape: &str) {
        assert_eq!(
            replayed.viewport_text(ROWS),
            source.viewport_text(ROWS),
            "{shape}: visible rows"
        );
        assert_eq!(
            replayed.cursor_pos(),
            source.cursor_pos(),
            "{shape}: cursor"
        );
        assert_eq!(
            replayed.total_rows(),
            source.total_rows(),
            "{shape}: rows with scrollback"
        );
        assert_eq!(
            replayed.plain_text(),
            source.plain_text(),
            "{shape}: scrollback text"
        );
    }

    fn numbered(lines: usize) -> Vec<u8> {
        (0..lines)
            .flat_map(|line| format!("line {line}\r\n").into_bytes())
            .collect()
    }

    #[test]
    fn handover_dump_restores_every_grid_shape() {
        let shapes: Vec<(&str, Vec<u8>)> = vec![
            ("empty", Vec::new()),
            ("short screen", b"one\r\ntwo\r\nthree".to_vec()),
            ("blank rows under the cursor after scrolling", numbered(25)),
            (
                "prompt above blank rows",
                [
                    numbered(25),
                    b"> prompt\r\n  status\r\n\x1b[2A\x1b[3C".to_vec(),
                ]
                .concat(),
            ),
            ("full screen", [numbered(25), b"last row".to_vec()].concat()),
            (
                "soft-wrapped rows",
                (0..12)
                    .flat_map(|line| format!("{line} {}\r\n", "x".repeat(60)).into_bytes())
                    .collect(),
            ),
            (
                "styled text",
                [
                    numbered(15),
                    b"\x1b[1;31mred\x1b[0m \x1b[38;2;10;20;30mtrue\x1b[0m\r\n".to_vec(),
                ]
                .concat(),
            ),
            (
                "hidden cursor",
                [numbered(5), b"\x1b[?25l".to_vec()].concat(),
            ),
            (
                "scrolling region over blank rows",
                [numbered(25), b"\x1b[3;8r\x1b[5;1Hinside".to_vec()].concat(),
            ),
            (
                "cleared screen with a scrolling region",
                [numbered(25), b"\x1b[2J\x1b[3;8r\x1b[4;2Hx".to_vec()].concat(),
            ),
            (
                "origin mode",
                [numbered(25), b"\x1b[3;8r\x1b[?6h\x1b[2;4Hrelative".to_vec()].concat(),
            ),
        ];
        for (shape, output) in shapes {
            let source = screen(&output);
            assert_same_screen(&source, handed_over(&source).terminal(), shape);
        }
    }

    #[test]
    fn handover_dump_keeps_later_output_on_its_rows() {
        let prompt = [numbered(25), b"> prompt\r\n  status\r\n\x1b[2A".to_vec()].concat();
        let region = [numbered(25), b"\x1b[3;8r\x1b[?6h\x1b[6;1H".to_vec()].concat();
        let redraws: [(&str, &[u8], &[u8]); 2] = [
            (
                "relative redraw",
                &prompt,
                b"\r\x1b[J> redrawn prompt\r\n  new status",
            ),
            (
                "output inside a region",
                &region,
                b"a\r\nb\r\nc\r\nd\r\ne\x1b[2;2Hz",
            ),
        ];
        for (shape, before, after) in redraws {
            let mut source = screen(before);
            let mut replayed = handed_over(&source);
            source.write(after);
            replayed.terminal_mut().write(after);
            assert_same_screen(&source, replayed.terminal(), shape);
        }
    }

    #[test]
    fn handover_dump_keeps_the_primary_screen_under_an_alternate_screen() {
        let mut source = screen(&[numbered(25), b"shell prompt$ ".to_vec()].concat());
        source.write(b"\x1b[?1049h\x1b[Hfull screen app\x1b[5;3H");
        let mut replayed = handed_over(&source);
        assert!(replayed.terminal().alt_screen_active());
        assert_eq!(
            replayed.terminal().viewport_text(ROWS),
            source.viewport_text(ROWS)
        );
        assert_eq!(replayed.terminal().cursor_pos(), source.cursor_pos());
        assert!(
            source.alt_screen_active(),
            "the dump must not change the live terminal"
        );

        source.write(b"\x1b[?1049l");
        replayed.terminal_mut().write(b"\x1b[?1049l");
        assert_same_screen(
            &source,
            replayed.terminal(),
            "primary screen after leaving the app",
        );
    }

    #[test]
    fn handover_dump_keeps_input_modes_on_either_screen() {
        const MODES: [u16; 7] = [1, 1000, 1002, 1004, 1006, 2004, 2031];
        let enable = MODES
            .iter()
            .flat_map(|mode| format!("\x1b[?{mode}h").into_bytes())
            .collect::<Vec<u8>>();
        for (screen_name, output) in [
            ("primary", [numbered(25), enable.clone()].concat()),
            (
                "alternate",
                [numbered(25), b"\x1b[?1049h".to_vec(), enable].concat(),
            ),
        ] {
            let source = screen(&output);
            let replayed = handed_over(&source);
            for mode in MODES {
                assert!(source.dec_mode(mode), "{screen_name}: source mode {mode}");
                assert!(
                    replayed.terminal().dec_mode(mode),
                    "{screen_name}: mode {mode} after the handover"
                );
            }
        }
    }

    #[test]
    fn a_handed_over_terminal_follows_later_theme_changes() {
        let theme = |background: &str, red: &str| Theme {
            foreground: "#dddddd".to_owned(),
            background: background.to_owned(),
            cursor: "#ffffff".to_owned(),
            ansi_palette: (0..16)
                .map(|slot| {
                    if slot == 1 {
                        red.to_owned()
                    } else {
                        format!("#0{slot:x}0{slot:x}0{slot:x}")
                    }
                })
                .collect(),
        };
        let (dark, light) = (theme("#101010", "#aa0000"), theme("#f0f0f0", "#bb1111"));
        let mut source = screen(&[numbered(12), b"\x1b[31mred text\x1b[0m".to_vec()].concat());
        source.set_theme(&dark).expect("theme");
        let mut replayed = adopt(&source, &dark);
        source.set_theme(&light).expect("theme");
        replayed.terminal_mut().set_theme(&light).expect("theme");
        assert_eq!(replayed.terminal().viewport_vt(), source.viewport_vt());
    }
}
