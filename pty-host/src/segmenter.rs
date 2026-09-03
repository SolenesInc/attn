use crate::blocks::{Marker, parse_marker};

const ESC: u8 = 0x1b;
const BEL: u8 = 0x07;
const BACKSLASH: u8 = b'\\';
const KITTY_INTRO: &[u8] = b"\x1b_G";
const OSC_133_PREFIX: &[u8] = b"\x1b]133;";
const KITTY_MAX_PENDING: usize = 72 * 1024 * 1024;
const OSC_133_MAX_PENDING: usize = 16 * 1024 * 1024;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
enum Mode {
    #[default]
    Ground,
    Escape,
    EscapeIntermediate,
    Csi,
    Osc,
    Osc133Prefix,
    Osc133Body,
    Opaque,
    Kitty,
}

impl Mode {
    fn holding(self) -> bool {
        matches!(self, Self::Kitty | Self::Osc133Prefix | Self::Osc133Body)
    }

    fn max_pending(self) -> usize {
        if self == Self::Kitty {
            KITTY_MAX_PENDING
        } else {
            OSC_133_MAX_PENDING
        }
    }

    fn abandoned(self) -> Self {
        if self == Self::Kitty {
            Self::Opaque
        } else {
            Self::Osc
        }
    }
}

#[derive(Debug, PartialEq, Eq)]
pub enum Segment {
    Plain(Vec<u8>),
    Kitty(Vec<u8>),
    Osc133(Vec<u8>, Option<Marker>),
}

#[derive(Default)]
pub struct Segmenter {
    mode: Mode,
    pending: Vec<u8>,
    resume: usize,
}

impl Segmenter {
    pub fn is_fast_path(&self, chunk: &[u8]) -> bool {
        self.mode == Mode::Ground && self.pending.is_empty() && !chunk.contains(&ESC)
    }

    #[allow(clippy::too_many_lines)]
    pub fn feed(&mut self, chunk: &[u8]) -> Vec<Segment> {
        if chunk.is_empty() {
            return Vec::new();
        }
        if self.is_fast_path(chunk) {
            return vec![Segment::Plain(chunk.to_vec())];
        }

        let carried = !self.pending.is_empty();
        let mut buffer = std::mem::take(&mut self.pending);
        if carried {
            buffer.extend_from_slice(chunk);
        } else {
            buffer = chunk.to_vec();
        }

        let mut segments = Vec::new();
        let mut hold_start = None;
        let mut index = 0;
        if carried && self.mode.holding() {
            hold_start = Some(0);
            index = self.resume;
        }
        let mut plain_start = 0;

        while index < buffer.len() {
            let byte = buffer[index];
            match self.mode {
                Mode::Ground => {
                    if byte != ESC {
                        index += 1;
                        continue;
                    }
                    if index + 1 >= buffer.len()
                        || (buffer[index + 1] == KITTY_INTRO[1] && index + 2 >= buffer.len())
                    {
                        emit_plain(&mut segments, &buffer, plain_start, index);
                        self.hold(&buffer, index, index);
                        return segments;
                    }
                    if buffer[index + 1] == b']' {
                        hold_start = Some(index);
                        self.mode = Mode::Osc133Prefix;
                        index += 2;
                    } else if buffer[index + 1] == KITTY_INTRO[1]
                        && buffer[index + 2] == KITTY_INTRO[2]
                    {
                        hold_start = Some(index);
                        self.mode = Mode::Kitty;
                        index += KITTY_INTRO.len();
                    } else {
                        self.mode = Mode::Escape;
                        index += 1;
                    }
                }
                Mode::Escape | Mode::EscapeIntermediate => {
                    if let Some(mode) = opens_c1(byte) {
                        self.mode = mode;
                        index += 1;
                        continue;
                    }
                    if self.mode == Mode::Escape
                        && let Some(mode) = opens_7bit(byte)
                    {
                        self.mode = mode;
                        index += 1;
                        continue;
                    }
                    self.mode = match byte {
                        ESC => Mode::Escape,
                        0x18 | 0x1a => Mode::Ground,
                        value if c1_executed(value) => Mode::Ground,
                        0x20..=0x2f => Mode::EscapeIntermediate,
                        0x30..=0x7e => Mode::Ground,
                        _ => self.mode,
                    };
                    index += 1;
                }
                Mode::Csi => {
                    self.mode = match byte {
                        ESC => Mode::Escape,
                        0x18 | 0x1a => Mode::Ground,
                        value if value >= 0x80 => opens_c1(value)
                            .or_else(|| c1_executed(value).then_some(Mode::Ground))
                            .unwrap_or(self.mode),
                        0x40..=0x7e => Mode::Ground,
                        _ => self.mode,
                    };
                    index += 1;
                }
                Mode::Osc => {
                    self.mode = match byte {
                        ESC => Mode::Escape,
                        BEL | 0x18 | 0x1a => Mode::Ground,
                        _ => self.mode,
                    };
                    index += 1;
                }
                Mode::Osc133Prefix => {
                    let start = hold_start.expect("OSC 133 prefix must be held");
                    if byte == OSC_133_PREFIX[index - start] {
                        index += 1;
                        if index - start == OSC_133_PREFIX.len() {
                            self.mode = Mode::Osc133Body;
                        }
                    } else {
                        hold_start = None;
                        self.mode = Mode::Osc;
                    }
                }
                Mode::Osc133Body => {
                    let start = hold_start.expect("OSC 133 body must be held");
                    if byte == BEL {
                        index += 1;
                        emit_plain(&mut segments, &buffer, plain_start, start);
                        segments.push(marker_segment(&buffer[start..index]));
                        plain_start = index;
                        hold_start = None;
                        self.mode = Mode::Ground;
                    } else if byte == ESC {
                        if index + 1 >= buffer.len() {
                            break;
                        }
                        if buffer[index + 1] == BACKSLASH {
                            index += 2;
                            emit_plain(&mut segments, &buffer, plain_start, start);
                            segments.push(marker_segment(&buffer[start..index]));
                            plain_start = index;
                            hold_start = None;
                            self.mode = Mode::Ground;
                        } else {
                            hold_start = None;
                            self.mode = Mode::Escape;
                            index += 1;
                        }
                    } else if matches!(byte, 0x18 | 0x1a) {
                        hold_start = None;
                        self.mode = Mode::Ground;
                        index += 1;
                    } else {
                        index += 1;
                    }
                }
                Mode::Opaque => {
                    if byte == ESC {
                        self.mode = Mode::Escape;
                    } else if aborts_string(byte) {
                        self.mode = Mode::Ground;
                    } else if let Some(mode) = opens_inside_string(byte) {
                        self.mode = mode;
                    }
                    index += 1;
                }
                Mode::Kitty => {
                    let start = hold_start.expect("kitty APC must be held");
                    if byte == ESC {
                        if index + 1 >= buffer.len() {
                            break;
                        }
                        if buffer[index + 1] == BACKSLASH {
                            emit_plain(&mut segments, &buffer, plain_start, start);
                            index += 2;
                            segments.push(Segment::Kitty(buffer[start..index].to_vec()));
                            plain_start = index;
                            hold_start = None;
                            self.mode = Mode::Ground;
                        } else {
                            hold_start = None;
                            self.mode = Mode::Escape;
                            index += 1;
                        }
                    } else if byte == 0x9c {
                        emit_plain(&mut segments, &buffer, plain_start, start);
                        index += 1;
                        segments.push(Segment::Kitty(buffer[start..index].to_vec()));
                        plain_start = index;
                        hold_start = None;
                        self.mode = Mode::Ground;
                    } else if aborts_string(byte) {
                        hold_start = None;
                        self.mode = Mode::Ground;
                        index += 1;
                    } else {
                        if let Some(mode) = opens_inside_string(byte) {
                            hold_start = None;
                            self.mode = mode;
                        }
                        index += 1;
                    }
                }
            }
        }

        if let Some(start) = hold_start {
            if buffer.len() - start > self.mode.max_pending() {
                emit_plain(&mut segments, &buffer, plain_start, buffer.len());
                self.release();
                self.mode = self.mode.abandoned();
                return segments;
            }
            emit_plain(&mut segments, &buffer, plain_start, start);
            self.hold(&buffer, start, index);
            return segments;
        }
        emit_plain(&mut segments, &buffer, plain_start, buffer.len());
        self.release();
        segments
    }

    fn hold(&mut self, buffer: &[u8], from: usize, resume_at: usize) {
        if from >= buffer.len() {
            self.release();
            return;
        }
        self.pending = buffer[from..].to_vec();
        self.resume = resume_at - from;
    }

    fn release(&mut self) {
        self.pending = Vec::new();
        self.resume = 0;
    }
}

fn emit_plain(segments: &mut Vec<Segment>, buffer: &[u8], from: usize, to: usize) {
    if to > from {
        segments.push(Segment::Plain(buffer[from..to].to_vec()));
    }
}

fn marker_segment(raw: &[u8]) -> Segment {
    let payload_end = if raw.last() == Some(&BEL) {
        raw.len() - 1
    } else {
        raw.len() - 2
    };
    let payload = String::from_utf8_lossy(&raw[OSC_133_PREFIX.len()..payload_end]);
    Segment::Osc133(raw.to_vec(), parse_marker(&payload))
}

fn c1_executed(byte: u8) -> bool {
    matches!(byte, 0x80..=0x8f | 0x91..=0x97 | 0x99..=0x9a | 0x9c)
}

fn aborts_string(byte: u8) -> bool {
    matches!(byte, 0x18 | 0x1a) || c1_executed(byte)
}

fn opens_inside_string(byte: u8) -> Option<Mode> {
    match byte {
        0x90 => Some(Mode::Opaque),
        0x9b => Some(Mode::Csi),
        0x9d => Some(Mode::Osc),
        _ => None,
    }
}

fn opens_c1(byte: u8) -> Option<Mode> {
    match byte {
        0x98 | 0x9e | 0x9f => Some(Mode::Opaque),
        _ => opens_inside_string(byte),
    }
}

fn opens_7bit(byte: u8) -> Option<Mode> {
    match byte {
        b'P' | b'X' | b'^' | b'_' => Some(Mode::Opaque),
        b']' => Some(Mode::Osc),
        b'[' => Some(Mode::Csi),
        _ => opens_c1(byte),
    }
}

#[cfg(test)]
mod tests {
    use super::{Segment, Segmenter};

    #[test]
    fn segments_split_kitty_and_marker_sequences() {
        let kitty = b"\x1b_Ga=T,f=24,s=2,v=2;QUJDRA==\x1b\\";
        let mut segmenter = Segmenter::default();
        assert_eq!(
            segmenter.feed(b"before\x1b_"),
            vec![Segment::Plain(b"before".to_vec())]
        );
        assert_eq!(
            segmenter.feed(
                &[
                    b"G".as_slice(),
                    kitty.strip_prefix(b"\x1b_G").unwrap(),
                    b"after"
                ]
                .concat()
            ),
            vec![
                Segment::Kitty(kitty.to_vec()),
                Segment::Plain(b"after".to_vec())
            ]
        );

        let segments = segmenter.feed(b"x\x1b]133;C;cmdline_url=echo%20hi\x07y");
        let Segment::Osc133(_, Some(marker)) = &segments[1] else {
            panic!("missing OSC 133 marker: {segments:?}");
        };
        assert_eq!(marker.command.as_deref(), Some("echo hi"));
    }

    #[test]
    fn does_not_extract_kitty_from_an_open_osc() {
        let bytes = b"\x1b]0;title\x1b_Ga=T,f=24,s=1,v=1;QQ==\x1b\\";
        let mut segmenter = Segmenter::default();
        assert_eq!(segmenter.feed(bytes), vec![Segment::Plain(bytes.to_vec())]);
    }
}
