//! What an attach does to a terminal's stream, from `app/src/pty/attachPlanning.ts`.

use std::collections::VecDeque;

/// How the app asks the daemon to attach. A first attach is `SameAppRemount`: the Tauri app's
/// `fresh_spawn` reaches the daemon as that.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Policy {
    SameAppRemount,
    Revive,
    RelaunchRestore,
}

impl Policy {
    pub fn wire(self) -> &'static str {
        match self {
            Policy::SameAppRemount => "same_app_remount",
            Policy::Revive => "revive",
            Policy::RelaunchRestore => "relaunch_restore",
        }
    }

    /// Whether the app sizes the PTY before it attaches: a revive carries its size in the
    /// attach, and a relaunch keeps the size the process had.
    pub fn resizes_first(self) -> bool {
        self == Policy::SameAppRemount
    }
}

/// PTY output while an attach is in flight waits here; the oldest goes first past this many,
/// as in the Tauri app.
pub const QUEUED_OUTPUT: usize = 512;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Chunk {
    pub seq: u32,
    pub data: Vec<u8>,
}

/// The parts of `attach_result` that decide the restore.
#[derive(Clone, Debug, Default)]
pub struct Attached<'a> {
    pub cols: Option<u16>,
    pub rows: Option<u16>,
    pub last_seq: Option<u32>,
    pub snapshot: Option<Snapshot<'a>>,
}

#[derive(Clone, Copy, Debug)]
pub struct Snapshot<'a> {
    pub cols: u16,
    pub rows: u16,
    pub bytes: &'a [u8],
    pub format: Option<&'a str>,
}

/// A snapshot this build can decode: its format tag is ours. A worker outlives an install, so
/// an older format is routine and means no snapshot.
pub fn decodable(format: Option<&str>, local: &str) -> bool {
    format
        .map(str::trim)
        .is_some_and(|f| !f.is_empty() && f == local)
}

#[derive(Debug, PartialEq, Eq)]
pub struct Restore<'a> {
    /// The size the local terminal takes before anything is written.
    pub size: Option<(u16, u16)>,
    pub snapshot: Option<&'a [u8]>,
    /// The sequence the stream continues from.
    pub baseline: u32,
    /// Queued output to write after the restore, in order.
    pub write: Vec<Chunk>,
    /// Queued output the snapshot already holds, written only when it fails to decode.
    pub fallback: Vec<Chunk>,
}

/// What a successful attach restores. With a snapshot the stream continues from the daemon's
/// `last_seq`; without one from the client's own watermark, so output the client queued but
/// never drew is not skipped.
pub fn restore<'a>(
    attached: &Attached<'a>,
    policy: Policy,
    previous_seq: Option<u32>,
    queued: impl IntoIterator<Item = Chunk>,
    local_format: &str,
) -> Restore<'a> {
    let snapshot = attached
        .snapshot
        .filter(|s| !s.bytes.is_empty() && decodable(s.format, local_format));
    let size = match (snapshot, policy, attached.cols, attached.rows) {
        (Some(s), _, _, _) => Some((s.cols, s.rows)),
        (None, Policy::RelaunchRestore, Some(cols), Some(rows)) => Some((cols, rows)),
        _ => None,
    };
    let mut baseline = match snapshot {
        Some(_) => attached.last_seq.unwrap_or(0),
        None => previous_seq.unwrap_or(0),
    };
    let mut write = Vec::new();
    let mut fallback = Vec::new();
    for chunk in queued {
        if chunk.seq <= baseline {
            if snapshot.is_some() && previous_seq.is_none_or(|p| chunk.seq > p) {
                fallback.push(chunk);
            }
            continue;
        }
        baseline = chunk.seq;
        write.push(chunk);
    }
    Restore {
        size,
        snapshot: snapshot.map(|s| s.bytes),
        baseline,
        write,
        fallback,
    }
}

/// Whether the app sends `pty_resize` once the attach answers: when the PTY's size differs
/// from the one the app asked for, unless the attach keeps the process's size.
pub fn resize_after(requested: (u16, u16), attached: &Attached<'_>, policy: Policy) -> bool {
    policy.resizes_first()
        && (attached.cols, attached.rows) != (Some(requested.0), Some(requested.1))
}

/// Where a terminal's output stream stands.
#[derive(Debug)]
pub enum Stream {
    Attaching(VecDeque<Chunk>),
    Live { last_seq: u32 },
}

pub enum Output {
    Write(Vec<u8>),
    Queued,
    Stale,
}

impl Stream {
    /// One frame of PTY output: queued while the attach is in flight, dropped when the stream
    /// already holds its sequence, else written.
    pub fn receive(&mut self, chunk: Chunk) -> Output {
        match self {
            Stream::Attaching(queue) => {
                if queue.len() == QUEUED_OUTPUT {
                    queue.pop_front();
                }
                queue.push_back(chunk);
                Output::Queued
            }
            Stream::Live { last_seq } if chunk.seq <= *last_seq => Output::Stale,
            Stream::Live { last_seq } => {
                *last_seq = chunk.seq;
                Output::Write(chunk.data)
            }
        }
    }
}
