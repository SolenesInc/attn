use std::fs::File;
use std::io::Read;

use crate::blocks::BlockTable;
use crate::ghostty::{KittyImage, KittyPlacement, Terminal};
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
                    Segment::Kitty(bytes) => self.write_apc(&bytes, &mut wire),
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

    pub fn snapshot_blocks(&self) -> Vec<crate::blocks::AttachBlock> {
        self.blocks.snapshot()
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
    use crate::ghostty::Terminal;

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
