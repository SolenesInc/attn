use std::ffi::OsStr;
use std::fs::{File, OpenOptions};
use std::io::{Read, Seek, SeekFrom};
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::OpenOptionsExt;

use base64::Engine as _;
use base64::engine::general_purpose::{STANDARD, STANDARD_NO_PAD};

use crate::blocks::BlockTable;
use crate::ghostty::{HandoverScreen, KittyImage, KittyPlacement, Terminal, Theme};
use crate::segmenter::{Segment, Segmenter};

const RESYNC_ANCHOR_LOST: &str = "kitty_layout_anchor_lost";
const RESYNC_ANCHOR_CLAMPED: &str = "kitty_layout_anchor_clamped";
const RESYNC_REVERSE_SCROLL: &str = "kitty_layout_reverse_scroll";
const RESYNC_UNDESCRIBED_IMAGE: &str = "kitty_undescribed_image";
const RESYNC_STAMP_WITHOUT_DELTA: &str = "kitty_stamp_without_delta";
const RESYNC_MARGIN_MODE: &str = "kitty_layout_margin_mode";
const RESYNC_SCROLL_CLAMPED: &str = "kitty_layout_scroll_clamped";
const RESYNC_PENDING_WRAP: &str = "kitty_layout_pending_wrap";
const WIRE_ST: &[u8] = b"\x1b\\";

pub struct FeedResult {
    pub wire: Vec<u8>,
    pub placements: Option<Vec<KittyPlacement>>,
    pub resync: Option<&'static str>,
}

pub struct WireFeeder {
    terminal: Terminal,
    blocks: BlockTable,
    segmenter: Segmenter,
    generation: u64,
    epoch: u64,
    placements: Vec<KittyPlacement>,
    placements_changed: bool,
    resync: Option<&'static str>,
}

impl WireFeeder {
    pub fn new(terminal: Terminal, epoch: u64) -> Self {
        let generation = terminal.kitty_generation();
        Self {
            terminal,
            blocks: BlockTable::new(),
            segmenter: Segmenter::default(),
            generation,
            epoch,
            placements: Vec::new(),
            placements_changed: false,
            resync: None,
        }
    }

    pub fn adopt(
        screen: &HandoverScreen,
        (cols, rows): (u16, u16),
        (cell_width, cell_height): (u16, u16),
        theme: &Theme,
        blocks: &[crate::blocks::AttachBlock],
        next_block_id: u64,
    ) -> Result<Self, String> {
        let mut terminal = Terminal::new(cols, rows)?;
        if cell_width > 0 && cell_height > 0 {
            terminal.resize_no_reflow(cols, rows, u32::from(cell_width), u32::from(cell_height))?;
        }
        terminal.write(&screen.primary);
        let mut wire = Self::new(terminal, mint_epoch());
        wire.blocks = BlockTable::restore(blocks, next_block_id, &wire.terminal);
        if let Some(alternate) = &screen.alternate {
            wire.terminal.write(b"\x1b[?1049h");
            wire.terminal.write(alternate);
        }
        wire.terminal.write(b"\x1b]104\x1b\\");
        wire.terminal.set_theme(theme)?;
        wire.terminal.drain_responses();
        wire.terminal.drain_program_status();
        Ok(wire)
    }

    pub fn feed(&mut self, data: &[u8]) -> FeedResult {
        self.placements_changed = false;
        self.resync = None;
        let mut wire = Vec::with_capacity(data.len().min(256 * 1024));

        if self.segmenter.is_fast_path(data) {
            self.terminal.write(data);
            wire.extend_from_slice(data);
        } else {
            for segment in self.segmenter.feed(data) {
                match segment {
                    Segment::Plain(bytes) => {
                        self.terminal.write(&bytes);
                        wire.extend_from_slice(&bytes);
                    }
                    Segment::Osc133(bytes, marker) => {
                        self.terminal.write(&bytes);
                        if let Some(marker) = marker {
                            self.blocks.apply(marker, &self.terminal);
                        }
                        wire.extend_from_slice(&bytes);
                    }
                    Segment::Kitty(bytes) => {
                        let apc = self
                            .terminal
                            .kitty_images_enabled()
                            .then(|| inline_named_file(&bytes))
                            .flatten()
                            .unwrap_or(bytes);
                        self.write_apc(&apc, &mut wire);
                    }
                }
            }
        }

        let settled = self.settle_unaccounted();
        if !settled && !self.placements.is_empty() {
            self.observe_placements();
        }
        FeedResult {
            wire,
            placements: self.placements_changed.then(|| self.placements.clone()),
            resync: self.resync,
        }
    }

    fn write_apc(&mut self, apc: &[u8], wire: &mut Vec<u8>) {
        self.settle_unaccounted();
        self.terminal.write(WIRE_ST);
        wire.extend_from_slice(WIRE_ST);

        let generation = self.generation;
        let (col, row) = self.terminal.cursor_pos();
        let before = self.terminal.track_cursor();
        self.terminal.write(apc);

        let stamped = self.terminal.kitty_generation();
        self.generation = stamped;
        let (moved_col, moved_row) = self.terminal.cursor_pos();
        if stamped == generation && moved_col == col && moved_row == row {
            return;
        }
        if stamped != generation {
            self.observe_placements();
        }

        let after = self.terminal.track_cursor();
        let Some((anchor, landed)) = tracked_rows(before.as_ref(), after.as_ref()) else {
            self.fail_resync(RESYNC_ANCHOR_LOST);
            return;
        };
        let scrolled = i64::from(row) - i64::from(moved_row) + landed - anchor;
        if scrolled < 0 {
            self.fail_resync(RESYNC_REVERSE_SCROLL);
            return;
        }
        if anchor == 0 && self.terminal.alt_screen_active() {
            self.fail_resync(RESYNC_ANCHOR_CLAMPED);
            return;
        }
        let (screen_cols, screen_rows) = self.terminal.size();
        if scrolled > i64::from(screen_rows) {
            self.fail_resync(RESYNC_SCROLL_CLAMPED);
            return;
        }
        if self.terminal.left_right_margin_mode() {
            self.fail_resync(RESYNC_MARGIN_MODE);
        }
        if screen_cols > 0 && col == screen_cols - 1 {
            self.fail_resync(RESYNC_PENDING_WRAP);
        }

        append_csi(wire, scrolled, b'S');
        append_axis_csi(wire, i64::from(moved_row) - i64::from(row), b'B', b'A');
        append_axis_csi(wire, i64::from(moved_col) - i64::from(col), b'C', b'D');
    }

    fn settle_unaccounted(&mut self) -> bool {
        let stamped = self.terminal.kitty_generation();
        if stamped == self.generation {
            return false;
        }
        self.generation = stamped;
        let before = self.placements.clone();
        self.observe_placements();
        if !self.placements_changed {
            self.fail_resync(RESYNC_STAMP_WITHOUT_DELTA);
        } else if has_added_or_updated(&before, &self.placements) {
            self.fail_resync(RESYNC_UNDESCRIBED_IMAGE);
        }
        true
    }

    fn observe_placements(&mut self) {
        let current = self.terminal.kitty_placements(self.epoch);
        if current != self.placements {
            self.placements = current;
            self.placements_changed = true;
        }
    }

    fn fail_resync(&mut self, reason: &'static str) {
        self.resync.get_or_insert(reason);
    }

    pub fn terminal(&self) -> &Terminal {
        &self.terminal
    }

    pub fn terminal_mut(&mut self) -> &mut Terminal {
        &mut self.terminal
    }

    pub fn read_state(&self) -> serde_json::Value {
        self.blocks.read_state()
    }
    pub fn read_block(&self, cols: u16, after: Option<u64>) -> Result<serde_json::Value, String> {
        self.blocks.read_block(&self.terminal, cols, after)
    }

    pub fn snapshot_blocks(&self) -> Vec<crate::blocks::AttachBlock> {
        self.blocks.snapshot()
    }

    pub fn next_block_id(&self) -> u64 {
        self.blocks.next_id()
    }

    pub fn snapshot_placements(&self) -> Option<Vec<KittyPlacement>> {
        if self.placements.is_empty() {
            None
        } else {
            Some(self.terminal.kitty_placements(self.epoch))
        }
    }

    pub fn kitty_image(&self, image_id: u32) -> Option<KittyImage> {
        self.terminal.kitty_image(image_id, self.epoch)
    }
}

const INLINE_FILE_MAX_BYTES: u64 = (65 << 20) / 4 * 3;

fn inline_named_file(apc: &[u8]) -> Option<Vec<u8>> {
    let body = apc.strip_prefix(b"\x1b_G")?;
    let body = body
        .strip_suffix(WIRE_ST)
        .or_else(|| body.strip_suffix(b"\x9c"))
        .unwrap_or(body);
    let (control, payload) = match body.iter().position(|&b| b == b';') {
        Some(at) => (&body[..at], &body[at + 1..]),
        None => (body, &body[body.len()..]),
    };
    let mut medium = b'd';
    let (mut offset, mut size) = (0_u64, 0_u64);
    let mut direct = b"\x1b_Gt=d".to_vec();
    for pair in control.split(|&b| b == b',') {
        let (key, value) = match pair.iter().position(|&b| b == b'=') {
            Some(at) => (&pair[..at], &pair[at + 1..]),
            None => (pair, &pair[pair.len()..]),
        };
        let number = || std::str::from_utf8(value).ok()?.parse::<u64>().ok();
        match key {
            b"t" => medium = value.first().copied().unwrap_or(medium),
            b"O" => offset = number().unwrap_or_default(),
            b"S" => size = number().unwrap_or_default(),
            b"m" => {}
            _ => {
                direct.push(b',');
                direct.extend_from_slice(pair);
            }
        }
    }
    if medium != b'f' {
        return None;
    }
    match read_named_image_file(payload, offset, size) {
        Ok(data) => {
            direct.push(b';');
            direct.extend_from_slice(STANDARD.encode(data).as_bytes());
            direct.extend_from_slice(WIRE_ST);
            Some(direct)
        }
        Err(error) => {
            eprintln!("kitty: did not read an image a program named by path: {error}");
            None
        }
    }
}

fn read_named_image_file(encoded_path: &[u8], offset: u64, size: u64) -> Result<Vec<u8>, String> {
    let path = STANDARD
        .decode(encoded_path)
        .or_else(|_| STANDARD_NO_PAD.decode(encoded_path))
        .map_err(|error| format!("the path is not base64: {error}"))?;
    let resolved =
        std::fs::canonicalize(OsStr::from_bytes(&path)).map_err(|error| error.to_string())?;
    if under_system_directory(&resolved) {
        return Err(format!(
            "{} is under /proc, /sys or /dev",
            resolved.display()
        ));
    }
    if size > INLINE_FILE_MAX_BYTES {
        return Err(format!(
            "size {size} is larger than one transmission carries"
        ));
    }
    let mut file = OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NONBLOCK | libc::O_NOCTTY)
        .open(&resolved)
        .map_err(|error| format!("{}: {error}", resolved.display()))?;
    let regular = file
        .metadata()
        .map_err(|error| error.to_string())?
        .is_file();
    if !regular {
        return Err(format!("{} is not a regular file", resolved.display()));
    }
    let opened =
        opened_file_path(&file).map_err(|error| format!("{}: {error}", resolved.display()))?;
    if under_system_directory(&opened) {
        return Err(format!(
            "{} opened {}, which is under /proc, /sys or /dev",
            resolved.display(),
            opened.display()
        ));
    }
    file.seek(SeekFrom::Start(offset))
        .map_err(|error| error.to_string())?;
    let limit = if size == 0 {
        INLINE_FILE_MAX_BYTES + 1
    } else {
        size
    };
    let mut data = Vec::new();
    file.take(limit)
        .read_to_end(&mut data)
        .map_err(|error| error.to_string())?;
    let read = data.len() as u64;
    if size > 0 && read != size {
        return Err(format!(
            "{} holds {read} bytes at offset {offset}, not the {size} asked for",
            resolved.display()
        ));
    }
    if read > INLINE_FILE_MAX_BYTES {
        return Err(format!(
            "{} is larger than one transmission carries",
            resolved.display()
        ));
    }
    Ok(data)
}

#[cfg(target_os = "linux")]
fn opened_file_path(file: &File) -> std::io::Result<std::path::PathBuf> {
    use std::os::fd::AsRawFd;
    std::fs::read_link(format!("/proc/self/fd/{}", file.as_raw_fd()))
}

#[cfg(target_os = "macos")]
fn opened_file_path(file: &File) -> std::io::Result<std::path::PathBuf> {
    use std::os::fd::AsRawFd;
    use std::os::unix::ffi::OsStringExt;
    let mut path = vec![0_u8; libc::PATH_MAX as usize];
    if unsafe { libc::fcntl(file.as_raw_fd(), libc::F_GETPATH, path.as_mut_ptr()) } == -1 {
        return Err(std::io::Error::last_os_error());
    }
    let len = path.iter().position(|&b| b == 0).unwrap_or(path.len());
    path.truncate(len);
    Ok(std::path::PathBuf::from(std::ffi::OsString::from_vec(path)))
}

fn under_system_directory(path: &std::path::Path) -> bool {
    path.starts_with("/proc")
        || path.starts_with("/sys")
        || (path.starts_with("/dev") && !path.starts_with("/dev/shm"))
}

fn tracked_rows(
    before: Option<&crate::ghostty::TrackedRef>,
    after: Option<&crate::ghostty::TrackedRef>,
) -> Option<(i64, i64)> {
    let (_, anchor) = before?.screen_point()?;
    let (_, landed) = after?.screen_point()?;
    Some((i64::from(anchor), i64::from(landed)))
}

fn append_axis_csi(wire: &mut Vec<u8>, delta: i64, positive: u8, negative: u8) {
    if delta >= 0 {
        append_csi(wire, delta, positive);
    } else {
        append_csi(wire, -delta, negative);
    }
}

fn append_csi(wire: &mut Vec<u8>, count: i64, final_byte: u8) {
    if count == 0 {
        return;
    }
    wire.extend_from_slice(b"\x1b[");
    wire.extend_from_slice(count.to_string().as_bytes());
    wire.push(final_byte);
}

fn has_added_or_updated(before: &[KittyPlacement], after: &[KittyPlacement]) -> bool {
    after.iter().any(|current| {
        before.iter().find(|prior| {
            prior.image_id == current.image_id && prior.placement_id == current.placement_id
        }) != Some(current)
    })
}

pub fn mint_epoch() -> u64 {
    const FLOOR: u64 = 1 << 32;
    const SPAN: u64 = (1 << 52) - FLOOR;
    let mut bytes = [0_u8; 8];
    if File::open("/dev/urandom")
        .and_then(|mut file| file.read_exact(&mut bytes))
        .is_ok()
    {
        return FLOOR + u64::from_be_bytes(bytes) % SPAN;
    }
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos();
    FLOOR + u64::try_from(nanos % u128::from(SPAN)).unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::{WireFeeder, mint_epoch};
    use crate::ghostty::{Terminal, Theme};

    #[test]
    fn command_blocks_survive_a_handover_under_an_alternate_screen() {
        let mut source = WireFeeder::new(Terminal::new(60, 12).expect("terminal"), mint_epoch());
        for command in 0..30 {
            source.feed(
                format!("\x1b]133;A\x07$ \x1b]133;C;cmdline=cmd-{command}\x07out {command}\r\n\x1b]133;D;0\x07").as_bytes(),
            );
        }
        source.feed(b"\x1b]133;A\x07$ \x1b]133;C;cmdline=vim\x07\x1b[?1049h\x1b[Hediting");
        let before = serde_json::to_value(source.snapshot_blocks()).expect("blocks");

        let adopted = WireFeeder::adopt(
            &source.terminal().handover_vt().expect("dump"),
            (60, 12),
            (0, 0),
            &Theme::default(),
            &source.snapshot_blocks(),
            source.next_block_id(),
        )
        .expect("adopt");
        assert!(adopted.terminal().alt_screen_active());
        assert_eq!(
            serde_json::to_value(adopted.snapshot_blocks()).expect("blocks"),
            before
        );
    }

    #[test]
    fn an_image_a_program_names_by_file_path_is_stored_on_either_screen() {
        use base64::Engine as _;
        let path =
            std::env::temp_dir().join(format!("attn-wire-named-image-{}.png", std::process::id()));
        let mut png = Vec::new();
        let mut encoder = png::Encoder::new(&mut png, 3, 2);
        encoder.set_color(png::ColorType::Rgba);
        encoder
            .write_header()
            .expect("png header")
            .write_image_data(&[255; 24])
            .expect("png pixels");
        std::fs::write(&path, &png).expect("write png");
        let named =
            base64::engine::general_purpose::STANDARD.encode(path.as_os_str().as_encoded_bytes());
        let mut feeder = WireFeeder::new(Terminal::new(80, 24).expect("terminal"), mint_epoch());
        feeder.feed(format!("\x1b_Ga=t,t=f,f=100,i=5,q=2;{named}\x1b\\").as_bytes());
        let primary = feeder.kitty_image(5).map(|i| (i.width, i.height));
        feeder.feed(format!("\x1b[?1049h\x1b_Ga=t,t=f,f=100,i=6,q=2;{named}\x1b\\").as_bytes());
        let alternate = feeder.kitty_image(6).map(|i| (i.width, i.height));
        std::fs::remove_file(&path).expect("remove png");
        assert_eq!((primary, alternate), (Some((3, 2)), Some((3, 2))));
    }

    #[test]
    fn plain_output_passes_through() {
        let terminal = Terminal::new(80, 24).expect("terminal");
        let mut feeder = WireFeeder::new(terminal, mint_epoch());
        let result = feeder.feed(b"hello\r\n");
        assert_eq!(result.wire, b"hello\r\n");
        assert!(result.placements.is_none());
        assert!(result.resync.is_none());
    }
}
