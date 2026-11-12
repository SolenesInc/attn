use std::collections::HashMap;
use std::hash::{BuildHasher, BuildHasherDefault, DefaultHasher};
use std::sync::{Arc, Mutex};

use attn_client::{Commands, Connection, Facts, Frame, TerminalEvent};
use attn_protocol::{
    AttachResultMessage, AttachSessionMessage, DetachSessionMessage, PtyInputMessage,
    PtyResizeMessage, SetTerminalThemeMessage, TerminalFocusMessage,
};
use attn_term::attach::{self, Attached, Chunk, Output, Policy, Snapshot, Stream};
use attn_term::{FONT_SIZE, Fonts, Grid, KeyInput, Metrics, SNAPSHOT_FORMAT, Theme, Vt, responses};
use rune::Tx;

/// How often a terminal tells the daemon its new size while its tile is being resized, as in
/// the Tauri app.
const RESIZE_INTERVAL: f64 = 0.25;
/// An agent's grid this small is a tile mid-layout, not a size the agent should draw for, as
/// in the Tauri app.
const AGENT_USABLE: (u16, u16) = (21, 11);
/// An attach the daemon refused while the terminal's worker restarts is asked again this often,
/// for this long, as in the Tauri app.
const ATTACH_RETRY: f64 = 0.15;
const ATTACH_RETRY_FOR: f64 = 3.0;
const FIRST_SIZE: (u16, u16) = (80, 24);

/// What the link hears about terminals, waiting for the window's next frame.
#[derive(Clone, Default)]
pub struct TerminalInbox(Arc<Mutex<Vec<TerminalEvent>>>);

impl TerminalInbox {
    /// The link's terminal sink: queues each event and calls `wake` so the window takes it.
    pub fn sink(
        &self,
        wake: impl Fn() + Send + Sync + 'static,
    ) -> Box<dyn Fn(TerminalEvent) + Send + Sync> {
        let queue = self.0.clone();
        Box::new(move |event| {
            queue.lock().unwrap().push(event);
            wake();
        })
    }

    /// Whether the window has taken everything the link handed on.
    pub fn is_empty(&self) -> bool {
        self.0.lock().unwrap().is_empty()
    }

    fn take(&self) -> Vec<TerminalEvent> {
        std::mem::take(&mut *self.0.lock().unwrap())
    }
}

/// The terminals the window draws, each attached to its PTY while its tile is drawn.
#[derive(Default)]
pub struct Terminals {
    pub(crate) inbox: TerminalInbox,
    panes: HashMap<String, Term>,
    connected: bool,
    daemon_instance: String,
    focused: Option<String>,
    fonts: Option<Fonts>,
    metrics: Option<Metrics>,
    theme: Theme,
}

struct Term {
    vt: Vt,
    /// The screen as the renderer last took it.
    grid: Grid,
    stream: Stream,
    phase: Phase,
    agent: bool,
    /// The daemon still has the session's process to bring back, so an attach revives it.
    recoverable: bool,
    /// The highest sequence this terminal has drawn, kept across attaches: an attach without a
    /// snapshot continues from it.
    watermark: Option<u32>,
    held: Vec<Vec<u8>>,
    tile: Option<Tile>,
    sent_size: Option<(u16, u16)>,
    sent_at: f64,
    resize_due: Option<f64>,
}

#[derive(Clone, Copy, Debug, PartialEq)]
struct Tile {
    size: (u16, u16),
    pixels: (u32, u32),
}

#[derive(Clone, Copy, Debug, PartialEq)]
enum Phase {
    /// Drawn, waiting for its tile's size or the connection.
    Waiting,
    Attaching(Attaching),
    /// Asked again at `at`, after the daemon refused for a reason that passes.
    Retrying {
        at: f64,
        attaching: Attaching,
    },
    Live,
    /// The process ended, or the attach failed: the screen stays as it was left.
    Stopped,
}

#[derive(Clone, Copy, Debug, PartialEq)]
struct Attaching {
    policy: Policy,
    /// The size the app gave the PTY before attaching, when the policy lets it.
    requested: Option<(u16, u16)>,
    started: f64,
    /// The tile changed size while the attach was in flight.
    relaid: bool,
}

impl Term {
    fn new(agent: bool, theme: &Theme) -> Term {
        Term {
            vt: themed(Vt::new(FIRST_SIZE.0, FIRST_SIZE.1), theme),
            grid: Grid::default(),
            stream: Stream::Attaching(Default::default()),
            phase: Phase::Waiting,
            agent,
            recoverable: false,
            watermark: None,
            held: Vec::new(),
            tile: None,
            sent_size: None,
            sent_at: f64::NEG_INFINITY,
            resize_due: None,
        }
    }
}

/// The mark a terminal's slot carries, so the renderer finds the terminal it draws.
pub fn mark(runtime_id: &str) -> u32 {
    BuildHasherDefault::<DefaultHasher>::default().hash_one(runtime_id) as u32
}

impl Terminals {
    /// Attaches the terminals the current desktop draws and lets go of the rest.
    pub(crate) fn follow(
        &mut self,
        facts: &Facts,
        keyboard: Option<String>,
        now: f64,
        daemon: &Commands,
    ) {
        self.follow_connection(facts, daemon);
        let drawn: Vec<(String, bool, bool)> = facts
            .current_desktop()
            .into_iter()
            .flat_map(|desktop| &desktop.panes)
            .filter(|pane| !pane.runtime_id.is_empty())
            .map(|pane| {
                let session = facts.sessions.iter().find(|s| s.id == pane.session_id);
                let recoverable =
                    session.is_some_and(|s| s.terminal_exit.is_none() && s.state == "recoverable");
                (pane.runtime_id.clone(), pane.kind == "agent", recoverable)
            })
            .collect();
        let gone: Vec<String> = self
            .panes
            .keys()
            .filter(|id| !drawn.iter().any(|(drawn, ..)| drawn == *id))
            .cloned()
            .collect();
        for id in gone {
            let term = self.panes.remove(&id).expect("listed above");
            if self.connected
                && matches!(
                    term.phase,
                    Phase::Attaching(_) | Phase::Retrying { .. } | Phase::Live
                )
            {
                let _ = daemon.send(&DetachSessionMessage {
                    id,
                    ..DetachSessionMessage::new()
                });
            }
        }
        for (id, agent, recoverable) in drawn {
            self.panes
                .entry(id)
                .or_insert_with(|| Term::new(agent, &self.theme))
                .recoverable = recoverable;
        }
        self.attach_waiting(now, daemon);
        self.focus(keyboard, daemon);
    }

    /// Streams start over on a new connection, from the start when the daemon itself is new.
    /// Following the facts keeps a connection's change in order with the state it brought.
    fn follow_connection(&mut self, facts: &Facts, daemon: &Commands) {
        let connected = facts.connection == Connection::Connected;
        if connected == self.connected {
            return;
        }
        self.connected = connected;
        if !connected {
            for term in self.panes.values_mut() {
                term.phase = Phase::Waiting;
                term.sent_size = None;
            }
            return;
        }
        if facts.daemon_instance != self.daemon_instance {
            self.daemon_instance.clone_from(&facts.daemon_instance);
            for term in self.panes.values_mut() {
                term.watermark = None;
            }
        }
        send_theme(&self.theme, daemon);
        self.focused = None;
    }

    /// Tells the daemon which terminal has the keyboard when that changes, and claims that
    /// terminal's PTY for this window's tile: the daemon sizes a PTY for the client that last
    /// asked.
    pub(crate) fn focus(&mut self, keyboard: Option<String>, daemon: &Commands) {
        if keyboard == self.focused {
            return;
        }
        self.focused = keyboard.clone();
        let Some(id) = keyboard.filter(|_| self.connected) else {
            return;
        };
        let _ = daemon.send(&TerminalFocusMessage {
            id: id.clone(),
            ..TerminalFocusMessage::new()
        });
        if let Some(term) = self.panes.get_mut(&id)
            && term.phase == Phase::Live
        {
            term.sent_size = None;
            let at = term.sent_at;
            send_resize(&id, term, at, Pixels::Sent, daemon);
        }
    }

    pub fn metrics(&mut self) -> Metrics {
        *self.metrics.get_or_insert_with(|| {
            self.fonts
                .get_or_insert_with(Fonts::default)
                .metrics(FONT_SIZE)
        })
    }

    /// A terminal's tile was laid out `w`×`h` points at `scale`: the grid that fits is the size
    /// the app asks the daemon for.
    pub(crate) fn laid_out(
        &mut self,
        id: &str,
        (w, h): (f32, f32),
        scale: f32,
        tx: &mut Tx,
        daemon: &Commands,
    ) {
        let metrics = self.metrics();
        let Some(term) = self.panes.get_mut(id) else {
            return;
        };
        if w <= 0.0 || h <= 0.0 {
            return;
        }
        let size = metrics.fit(w, h);
        if term.agent && mid_layout(size, term.vt.size(), (w, h), &metrics) {
            return;
        }
        term.tile = Some(Tile {
            size,
            pixels: metrics.pixels(size, scale),
        });
        let now = tx.clock();
        match &mut term.phase {
            Phase::Waiting => self.attach_waiting(now, daemon),
            Phase::Live => {
                let due = term.sent_at + RESIZE_INTERVAL;
                if now >= due {
                    send_resize(id, term, now, Pixels::Sent, daemon);
                } else {
                    term.resize_due = Some(due);
                    tx.wake_at(due);
                }
            }
            Phase::Attaching(attaching) | Phase::Retrying { attaching, .. } => {
                attaching.relaid = true;
            }
            Phase::Stopped => {}
        }
    }

    fn attach_waiting(&mut self, now: f64, daemon: &Commands) {
        if !self.connected {
            return;
        }
        for (id, term) in &mut self.panes {
            if term.phase != Phase::Waiting || term.tile.is_none() {
                continue;
            }
            let tile = term.tile.expect("checked above");
            let policy = if term.recoverable {
                Policy::Revive
            } else {
                Policy::SameAppRemount
            };
            let requested = policy.resizes_first().then_some(tile.size);
            if requested.is_some() {
                term.sent_size = None;
                let at = term.sent_at;
                send_resize(id, term, at, Pixels::Sent, daemon);
            }
            term.stream = Stream::Attaching(Default::default());
            let attaching = Attaching {
                policy,
                requested,
                started: now,
                relaid: false,
            };
            send_attach(id, term, attaching, daemon);
        }
    }

    /// Takes what the link heard since the last frame. Nothing it changes is in the view:
    /// terminal output needs only the frame the wake already asked for.
    pub(crate) fn drain(&mut self, tx: &mut Tx, daemon: &Commands) {
        for event in self.inbox.take() {
            self.apply(event, tx, daemon);
        }
        let now = tx.clock();
        for (id, term) in &mut self.panes {
            match term.phase {
                Phase::Live if term.resize_due.is_some_and(|due| now >= due) => {
                    send_resize(id, term, now, Pixels::Sent, daemon);
                }
                Phase::Retrying { at, attaching } if now >= at => {
                    send_attach(id, term, attaching, daemon);
                }
                _ => {}
            }
        }
    }

    fn apply(&mut self, event: TerminalEvent, tx: &mut Tx, daemon: &Commands) {
        match event {
            TerminalEvent::Frame(Frame::PtyOutput {
                terminal,
                seq,
                data,
            }) => {
                let Some(term) = self.panes.get_mut(&terminal) else {
                    return;
                };
                if let Output::Write(bytes) = term.stream.receive(Chunk { seq, data }) {
                    term.watermark = Some(seq);
                    term.vt.write(&bytes);
                    answer(&terminal, term, daemon);
                }
            }
            TerminalEvent::Frame(Frame::KittyImage { .. }) => {}
            TerminalEvent::Attached(result) => self.attached(*result, tx, daemon),
            TerminalEvent::Resized(resized) => {
                let metrics = self.metrics();
                if let Some(term) = self.panes.get_mut(&resized.id) {
                    let size = (clamp(resized.cols), clamp(resized.rows));
                    term.vt.resize(size.0, size.1, cell_pixels(&metrics));
                }
            }
            TerminalEvent::Desync(desync) => self.restart(&desync.id, None, tx.clock(), daemon),
            TerminalEvent::Respawned(respawned) => self.restart(
                &respawned.id,
                Some(Policy::RelaunchRestore),
                tx.clock(),
                daemon,
            ),
            TerminalEvent::Exited(exited) => {
                if let Some(term) = self.panes.get_mut(&exited.id) {
                    term.phase = Phase::Stopped;
                    let notice = exit_notice(exited.exit_code, exited.signal.as_deref());
                    term.vt.write(format!("\r\n{notice}\r\n").as_bytes());
                }
            }
        }
    }

    /// The daemon replaced the terminal's stream: start the screen over from a new attach.
    fn restart(&mut self, id: &str, policy: Option<Policy>, now: f64, daemon: &Commands) {
        let Some(term) = self.panes.get_mut(id) else {
            return;
        };
        let streaming = matches!(term.phase, Phase::Attaching(_) | Phase::Live);
        let reviving = matches!(
            term.phase,
            Phase::Attaching(Attaching {
                policy: Policy::Revive,
                ..
            })
        );
        match policy {
            None if !streaming => return,
            Some(_) if reviving => return,
            _ => {}
        }
        let (cols, rows) = term.vt.size();
        term.vt = themed(Vt::new(cols, rows), &self.theme);
        term.watermark = None;
        term.stream = Stream::Attaching(Default::default());
        if !self.connected {
            term.phase = Phase::Waiting;
            return;
        }
        term.phase = Phase::Attaching(Attaching {
            policy: policy.unwrap_or(Policy::SameAppRemount),
            requested: None,
            started: now,
            relaid: false,
        });
        let _ = daemon.send(&AttachSessionMessage {
            id: id.into(),
            attach_policy: policy.map(|p| p.wire().into()),
            ..AttachSessionMessage::new()
        });
    }

    fn attached(&mut self, result: AttachResultMessage, tx: &mut Tx, daemon: &Commands) {
        let metrics = self.metrics();
        let Some(term) = self.panes.get_mut(&result.id) else {
            return;
        };
        let Phase::Attaching(attaching) = term.phase else {
            return;
        };
        let id = result.id.clone();
        if !result.success {
            let error = result.error.unwrap_or_default();
            let now = tx.clock();
            if passes(&error) && now - attaching.started < ATTACH_RETRY_FOR {
                let at = now + ATTACH_RETRY;
                term.phase = Phase::Retrying { at, attaching };
                tx.wake_at(at);
                return;
            }
            term.phase = Phase::Stopped;
            term.watermark = None;
            term.vt
                .write(format!("\r\n[Failed to attach PTY: {error}]\r\n").as_bytes());
            term.held.clear();
            return;
        }
        if result.running == Some(false)
            && let Some(screen) = &result.screen
        {
            term.phase = Phase::Stopped;
            term.vt = themed(Vt::new(clamp(screen.cols), clamp(screen.rows)), &self.theme);
            let text = screen
                .text
                .trim_end()
                .replace("\r\n", "\n")
                .replace('\n', "\r\n");
            term.vt.write(text.as_bytes());
            term.vt.take_responses();
            term.held.clear();
            return;
        }
        let snapshot_bytes = result
            .snapshot
            .as_ref()
            .and_then(|s| base64_decode(&s.snapshot_b64));
        let attached =
            Attached {
                cols: result.cols.map(clamp),
                rows: result.rows.map(clamp),
                last_seq: result.last_seq.and_then(|s| u32::try_from(s).ok()),
                snapshot: result.snapshot.as_ref().zip(snapshot_bytes.as_deref()).map(
                    |(s, bytes)| Snapshot {
                        cols: clamp(s.cols),
                        rows: clamp(s.rows),
                        bytes,
                        format: s.format.as_deref(),
                    },
                ),
            };
        let queued = match std::mem::replace(&mut term.stream, Stream::Live { last_seq: 0 }) {
            Stream::Attaching(queue) => queue,
            Stream::Live { .. } => Default::default(),
        };
        let restore = attach::restore(
            &attached,
            attaching.policy,
            term.watermark,
            queued,
            SNAPSHOT_FORMAT,
        );
        let cell = cell_pixels(&metrics);
        let restored = restore.snapshot.and_then(Vt::restore);
        let decoded = restored.is_some();
        if let Some(vt) = restored {
            term.vt = themed(vt, &self.theme);
        }
        if let Some((cols, rows)) = restore.size {
            term.vt.resize(cols, rows, cell);
        }
        if restore.snapshot.is_some() && !decoded {
            for chunk in &restore.fallback {
                term.vt.write(&chunk.data);
            }
        }
        for chunk in &restore.write {
            term.vt.write(&chunk.data);
        }
        term.watermark = Some(restore.baseline);
        term.stream = Stream::Live {
            last_seq: restore.baseline,
        };
        answer(&id, term, daemon);
        if result.running == Some(false) {
            term.phase = Phase::Stopped;
            term.held.clear();
            return;
        }
        term.phase = Phase::Live;
        for data in std::mem::take(&mut term.held) {
            send_input(&id, &data, "user", daemon);
        }
        let at = term.sent_at;
        if attaching.relaid && attaching.policy != Policy::RelaunchRestore {
            term.sent_size = None;
            send_resize(&id, term, at, Pixels::Sent, daemon);
        } else if let Some(requested) = attaching.requested
            && attach::resize_after(requested, &attached, attaching.policy)
        {
            term.sent_size = None;
            send_resize(&id, term, at, Pixels::Left, daemon);
        }
    }

    /// Sends what the user typed or pasted to the terminal, held while it attaches.
    pub(crate) fn input(&mut self, id: &str, data: &[u8], daemon: &Commands) -> bool {
        let Some(term) = self.panes.get_mut(id) else {
            return false;
        };
        match term.phase {
            Phase::Live => send_input(id, data, "user", daemon),
            Phase::Waiting | Phase::Attaching(_) | Phase::Retrying { .. } => {
                term.held.push(data.to_vec())
            }
            Phase::Stopped => {}
        }
        true
    }

    pub(crate) fn encode_key(&mut self, id: &str, key: &KeyInput) -> Option<Vec<u8>> {
        Some(self.panes.get_mut(id)?.vt.encode_key(key))
    }

    pub(crate) fn encode_paste(&mut self, id: &str, text: &str) -> Option<Vec<u8>> {
        Some(self.panes.get_mut(id)?.vt.encode_paste(text))
    }

    /// The text on a terminal's screen, row by row with trailing blanks trimmed.
    pub fn screen_text(&mut self, id: &str) -> Option<Vec<String>> {
        let term = self.panes.get_mut(id)?;
        let mut grid = Grid::default();
        term.vt.sync(&mut grid);
        Some(grid.text_lines())
    }

    /// The colors every terminal draws with until its program sets its own, which the daemon
    /// also answers color queries with.
    pub(crate) fn set_theme(&mut self, theme: Theme, daemon: &Commands) {
        if theme == self.theme {
            return;
        }
        self.theme = theme;
        for term in self.panes.values_mut() {
            term.vt.set_theme(&theme);
        }
        if self.connected {
            send_theme(&theme, daemon);
        }
    }

    /// Each drawn terminal's screen brought up to date, by the mark of the slot it draws in.
    pub(crate) fn grids(&mut self) -> impl Iterator<Item = (u32, &mut Grid)> {
        self.panes.iter_mut().map(|(id, term)| {
            term.vt.sync(&mut term.grid);
            (mark(id), &mut term.grid)
        })
    }

    /// The size the terminal's own screen has, which follows the daemon's PTY.
    pub fn screen_size(&mut self, id: &str) -> Option<(u16, u16)> {
        Some(self.panes.get_mut(id)?.vt.size())
    }
}

/// Sends the replies the terminal wrote to the program's queries, less those the daemon
/// answers itself.
fn answer(id: &str, term: &mut Term, daemon: &Commands) {
    let replies = responses::without_daemon_owned(&term.vt.take_responses());
    if !replies.is_empty() {
        send_input(id, &replies, "response", daemon);
    }
}

fn send_input(id: &str, data: &[u8], source: &'static str, daemon: &Commands) {
    let _ = daemon.send(&PtyInputMessage {
        id: id.into(),
        data: String::from_utf8_lossy(data).into_owned(),
        source: Some(source.into()),
        ..PtyInputMessage::new()
    });
}

fn send_attach(id: &str, term: &mut Term, attaching: Attaching, daemon: &Commands) {
    let revive = attaching.policy == Policy::Revive;
    let size = term.tile.map(|t| t.size).filter(|_| revive);
    let _ = daemon.send(&AttachSessionMessage {
        id: id.into(),
        attach_policy: Some(attaching.policy.wire().into()),
        cols: size.map(|s| i64::from(s.0)),
        rows: size.map(|s| i64::from(s.1)),
        ..AttachSessionMessage::new()
    });
    term.phase = Phase::Attaching(attaching);
}

/// Whether a resize tells the daemon the tile's pixel size. The resize that only reconciles a
/// PTY the daemon attached at another size leaves it out, as the Tauri app does.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Pixels {
    Sent,
    Left,
}

fn send_resize(id: &str, term: &mut Term, now: f64, pixels: Pixels, daemon: &Commands) {
    term.resize_due = None;
    let Some(tile) = term.tile else {
        return;
    };
    if term.sent_size == Some(tile.size) {
        return;
    }
    term.sent_size = Some(tile.size);
    term.sent_at = now;
    let px = |n: u32| (pixels == Pixels::Sent).then_some(i64::from(n));
    let _ = daemon.send(&PtyResizeMessage {
        id: id.into(),
        cols: i64::from(tile.size.0),
        rows: i64::from(tile.size.1),
        xpixel: px(tile.pixels.0),
        ypixel: px(tile.pixels.1),
        ..PtyResizeMessage::new()
    });
}

/// An agent's tile laid out too small to be a size the agent should draw for, while the
/// terminal's grid is healthy and fits the tile, as the Tauri app judges it. A grid already
/// that small is followed as it grows; one that overflows the tile is always refit.
fn mid_layout(size: (u16, u16), grid: (u16, u16), (w, h): (f32, f32), metrics: &Metrics) -> bool {
    let unusable = |(cols, rows): (u16, u16)| cols < AGENT_USABLE.0 || rows < AGENT_USABLE.1;
    if !unusable(size) {
        return false;
    }
    let overflows = f32::from(grid.1) * metrics.cell_h > h + 1.0
        || f32::from(grid.0) * metrics.cell_w > w + 1.0;
    let shrinks = size.0 < grid.0 || size.1 < grid.1;
    !overflows && !(unusable(grid) && !shrinks)
}

/// A refusal the daemon gives while the terminal's worker restarts, as the Tauri app tells it.
fn passes(error: &str) -> bool {
    let error = error.to_lowercase();
    if error.contains("websocket not connected") || error.contains("daemon is recovering") {
        return false;
    }
    (error.contains("dial unix") && error.contains("no such file or directory"))
        || [
            "connection refused",
            "resource temporarily unavailable",
            "broken pipe",
            "i/o timeout",
        ]
        .iter()
        .any(|transient| error.contains(transient))
}

fn clamp(n: i64) -> u16 {
    n.clamp(1, i64::from(u16::MAX)) as u16
}

fn cell_pixels(metrics: &Metrics) -> (u32, u32) {
    (metrics.cell_w as u32, metrics.cell_h as u32)
}

/// What the terminal says when its process ends, as the Tauri app's exit notice: the daemon
/// ends sessions with SIGHUP, SIGINT or SIGTERM, which are not crashes.
fn exit_notice(code: i64, signal: Option<&str>) -> String {
    let signal = signal.map(str::trim).filter(|s| !s.is_empty());
    let signal = match signal {
        Some(s) => Some(if s.to_ascii_uppercase().starts_with("SIG") {
            s.to_ascii_uppercase()
        } else {
            format!("SIG{}", s.to_ascii_uppercase())
        }),
        None => match code {
            129 => Some("SIGHUP".into()),
            130 => Some("SIGINT".into()),
            143 => Some("SIGTERM".into()),
            _ => None,
        },
    };
    let graceful = signal
        .as_deref()
        .is_some_and(|s| matches!(s, "SIGHUP" | "SIGINT" | "SIGTERM"));
    if (code == 0 && signal.is_none()) || graceful {
        "[Session ended]".into()
    } else {
        format!("[Process exited with code {code}]")
    }
}

fn base64_decode(text: &str) -> Option<Vec<u8>> {
    use base64::Engine;
    base64::engine::general_purpose::STANDARD.decode(text).ok()
}

fn themed(mut vt: Vt, theme: &Theme) -> Vt {
    vt.set_theme(theme);
    vt
}

fn send_theme(theme: &Theme, daemon: &Commands) {
    let _ = daemon.send(&SetTerminalThemeMessage {
        foreground: Theme::hex(theme.fg),
        background: Theme::hex(theme.bg),
        cursor: Theme::hex(theme.cursor),
        ansi_palette: theme.ansi.iter().map(|&c| Theme::hex(c)).collect(),
        ..SetTerminalThemeMessage::new()
    });
}
