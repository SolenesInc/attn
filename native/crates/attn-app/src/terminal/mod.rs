use std::collections::HashMap;
use std::hash::{BuildHasher, BuildHasherDefault, DefaultHasher};
use std::sync::{Arc, Mutex};
use std::time::Instant;

use attn_client::{Commands, Connection, Facts, Frame, Pulse, Reach, TerminalEvent, TerminalSink};
use attn_protocol::DesktopPane;
use attn_protocol::{
    AttachResultMessage, AttachSessionMessage, DetachSessionMessage, PtyInputMessage,
    PtyResizeMessage, SetTerminalThemeMessage, TerminalFocusMessage,
    TerminalPointerActivityMessage,
};
use attn_term::attach::{self, Attached, Chunk, Output, Policy, Snapshot, Stream};
use attn_term::mouse::{self, Action, WheelRows};
use attn_term::{FONT_SIZE, Grid, KeyInput, Metrics, SNAPSHOT_FORMAT, Vt, responses};
use attn_ui::colors::{TerminalColors, to_hex};
use rune::{Live, Tx};

mod annotating;
mod blocks;
mod filter;
mod find;
mod ids;
mod input_trace;
mod kitty;
mod latency;
mod links;
mod selection;
mod snapshots;
mod spoken;

pub(crate) use annotating::{
    Anchor, Annotation, Marking, Miss, Notice, Outcome, Popup, Transcript,
};
pub(crate) use blocks::Part;
pub(crate) use filter::{Filtered, Filtering, filter_field_key};
pub(crate) use find::{Finding, Toward, find_field_key};
pub(crate) use ids::{Known, Mark, Subject, ask_about_unresolved};
pub(crate) use input_trace::{Decision, Heard, InputTrace, Target, TraceId, Write};
pub(crate) use latency::type_into;
pub(crate) use links::accelerator;
pub(crate) use selection::Release;
pub(crate) use snapshots::miniature_mark;

/// How often a terminal tells the daemon its new size while its tile is being resized.
const RESIZE_INTERVAL: f64 = 0.25;
/// Seconds a synchronized update may hold the screen, as long as Ghostty's renderer waits.
const SYNCHRONIZED_UPDATE_LIMIT: f64 = 1.0;
/// An agent's grid this small is a tile mid-layout, not a size the agent should draw for.
const AGENT_USABLE: (u16, u16) = (21, 11);
/// An attach the daemon refused while the terminal's worker restarts is asked again this often,
/// for this long.
const ATTACH_RETRY: f64 = 0.15;
const ATTACH_RETRY_FOR: f64 = 3.0;
const FIRST_SIZE: (u16, u16) = (80, 24);
/// How often pointer motion over an agent's terminal tells the daemon the user is there:
/// sampled well inside the daemon's five-second quiet window.
const POINTER_ACTIVITY_INTERVAL: f64 = 0.25;

/// What the link hears about terminals, waiting for a window's next frame. The windows of one
/// link share it: each terminal's events go to the window that last attached it, the main
/// window's when none has, and what concerns no one terminal goes to every window.
#[derive(Clone)]
pub struct TerminalInbox {
    own: Arc<Queue>,
    main: Arc<Queue>,
    owners: Arc<Mutex<HashMap<String, Arc<Queue>>>>,
    taken: Pulse,
}

impl Default for TerminalInbox {
    fn default() -> TerminalInbox {
        let main = Arc::new(Queue::default());
        TerminalInbox {
            own: main.clone(),
            main,
            owners: Arc::default(),
            taken: Pulse::default(),
        }
    }
}

#[derive(Default)]
struct Queue {
    events: Mutex<Vec<(Instant, Reach, TerminalEvent)>>,
    wake: Mutex<Option<Arc<dyn Fn() + Send + Sync>>>,
}

impl Queue {
    fn push(&self, from: Reach, event: TerminalEvent) {
        self.events
            .lock()
            .unwrap()
            .push((input_trace::hop_time(), from, event));
        let wake = self.wake.lock().unwrap().clone();
        if let Some(wake) = wake {
            wake();
        }
    }
}

impl TerminalInbox {
    /// An inbox that beats `taken` each time the window has applied what it took.
    pub fn beating(taken: Pulse) -> TerminalInbox {
        TerminalInbox {
            taken,
            ..TerminalInbox::default()
        }
    }

    /// The link's terminal sink: queues each event for the window it belongs to and calls
    /// that window's wake, `wake` for this main window, so the window takes it.
    pub fn sink(&self, wake: impl Fn() + Send + Sync + 'static) -> TerminalSink {
        *self.main.wake.lock().unwrap() = Some(Arc::new(wake));
        self.sink_from(Reach::Home)
    }

    /// The sink of the link to this machine's own daemon, which wakes the windows as `sink`'s
    /// does.
    pub fn machine_sink(&self) -> TerminalSink {
        self.sink_from(Reach::Machine)
    }

    fn sink_from(&self, from: Reach) -> TerminalSink {
        let (main, owners) = (self.main.clone(), self.owners.clone());
        Box::new(move |event| match terminal_of(&event) {
            Some(id) => {
                let owner = owners.lock().unwrap().get(id).cloned();
                owner.unwrap_or_else(|| main.clone()).push(from, event);
            }
            None => {
                let mut queues: Vec<Arc<Queue>> = vec![main.clone()];
                for queue in owners.lock().unwrap().values() {
                    if !queues.iter().any(|q| Arc::ptr_eq(q, queue)) {
                        queues.push(queue.clone());
                    }
                }
                for queue in queues {
                    queue.push(from, event.clone());
                }
            }
        })
    }

    /// Another window's share of this link's terminal events, woken by `wake`.
    pub fn for_window(&self, wake: impl Fn() + Send + Sync + 'static) -> TerminalInbox {
        TerminalInbox {
            own: Arc::new(Queue {
                events: Mutex::default(),
                wake: Mutex::new(Some(Arc::new(wake))),
            }),
            main: self.main.clone(),
            owners: self.owners.clone(),
            taken: self.taken.clone(),
        }
    }

    /// Whether the window has taken everything the link handed it.
    pub fn is_empty(&self) -> bool {
        self.own.events.lock().unwrap().is_empty()
    }

    /// Whether this is the main window's inbox, which speaks for the link: the daemon's
    /// terminal colors are one per connection.
    fn is_main(&self) -> bool {
        Arc::ptr_eq(&self.own, &self.main)
    }

    fn take(&self) -> Vec<(Instant, Reach, TerminalEvent)> {
        std::mem::take(&mut *self.own.events.lock().unwrap())
    }

    /// This window attaches terminal `id`: its events come here from now on.
    fn claim(&self, id: &str) {
        let own = self.own.clone();
        self.owners.lock().unwrap().insert(id.to_owned(), own);
    }

    /// This window lets go of terminal `id`: true when it held the attach, which is then its
    /// to end. A terminal another window attached since stays with that window.
    fn release(&self, id: &str) -> bool {
        let mut owners = self.owners.lock().unwrap();
        let owned = owners
            .get(id)
            .is_some_and(|owner| Arc::ptr_eq(owner, &self.own));
        if owned {
            owners.remove(id);
        }
        owned
    }
}

/// The terminal an event is about, by runtime id; none for what concerns a session.
fn terminal_of(event: &TerminalEvent) -> Option<&str> {
    match event {
        TerminalEvent::Frame(Frame::PtyOutput { terminal, .. })
        | TerminalEvent::Frame(Frame::KittyImage { terminal, .. }) => Some(terminal),
        TerminalEvent::Attached(result) => Some(&result.id),
        TerminalEvent::Resized(resized) => Some(&resized.id),
        TerminalEvent::Placements(described) => Some(&described.id),
        TerminalEvent::ImageResult(result) => Some(&result.id),
        TerminalEvent::Snapshot(result) => Some(&result.id),
        TerminalEvent::Desync(desync) => Some(&desync.id),
        TerminalEvent::Respawned(respawned) => Some(&respawned.id),
        TerminalEvent::Exited(exited) => Some(&exited.id),
        TerminalEvent::MessagesChanged(_) => None,
    }
}

/// What became of what the person typed for a terminal.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Typed {
    /// Sent, or held until the terminal attaches.
    Taken,
    /// Refused here, since the terminal's machine is not streaming.
    Refused,
    Nowhere,
}

/// The terminals the window draws, each attached to its PTY while its tile is drawn.
#[derive(Default)]
pub struct Terminals {
    pub(crate) inbox: TerminalInbox,
    panes: HashMap<String, Term>,
    /// The screen each hidden terminal last showed, without its images, by runtime id: a tile
    /// shown again draws it until its attach brings the current one.
    hidden_screens: HashMap<String, Grid>,
    snapshots: HashMap<String, snapshots::Snapshot>,
    up: Up,
    daemon_instance: String,
    /// The link to this machine's own daemon, which the terminals that run here ride while the
    /// daemon on the app's port relays the app to its home.
    pub(crate) machine: Commands,
    machine_instance: String,
    focused: Option<String>,
    metrics: Option<Metrics>,
    theme: TerminalColors,
    /// A screen changed since the renderer last took the grids.
    undrawn: bool,
    /// What an input method is composing, and for which terminal, until it commits.
    preedit: Option<(String, String)>,
    /// The terminal whose slot shows the pointing hand over a link, with ⌘ or Ctrl held.
    pointing: Option<String>,
    /// Each terminal's find bar, by runtime id, kept with its query once closed.
    finds: HashMap<String, Finding>,
    images: kitty::Images,
    filters: HashMap<String, Filtering>,
    /// What a comment or note box edits once its popup or panel has closed.
    unshown_comment: rune::TextEdit,
    /// What the terminals have to tell the person, until the window shows it.
    said: Option<String>,
    ids: ids::Ids,
    pub(crate) latency: latency::InputLatency,
    /// The app's input trace, which the main window hands its tile windows.
    pub(crate) trace: Arc<Mutex<InputTrace>>,
    /// What a screen reader last heard of the terminal holding the keys, while one listens.
    heard: Option<spoken::Heard>,
    /// Another window is in front of this one.
    in_background: bool,
}

struct Term {
    vt: Vt,
    /// The screen as the renderer last took it.
    grid: Grid,
    /// The grid is the screen the terminal showed before its tile hid, held until the attach
    /// answers.
    holding_hidden_screen: bool,
    stream: Stream,
    phase: Phase,
    /// The link the terminal attaches on, and hears its stream from.
    via: Reach,
    agent: bool,
    place: links::Place,
    /// The daemon still has the session's process to bring back, so an attach revives it.
    recoverable: bool,
    /// The highest sequence this terminal has drawn, kept across attaches: an attach without a
    /// snapshot continues from it.
    watermark: Option<u32>,
    held: Vec<(Vec<u8>, TraceId)>,
    tile: Option<Tile>,
    /// The slot the terminal draws in, in points, and the scale it draws at.
    slot: Option<((f32, f32), f32)>,
    /// The cell text goes to next, in points from the slot's top-left, kept where output and
    /// layout leave it so the input method's caret follows without a view run.
    caret: Live<[f32; 4]>,
    sent_size: Option<(u16, u16)>,
    sent_at: f64,
    resize_due: Option<f64>,
    pointer: Pointer,
    view: selection::View,
    blocks: blocks::TermBlocks,
    placements: attn_term::kitty::Placements,
    /// How far back the view was scrolled when the window last drew it.
    scrolled_back: u32,
    /// When the program began the synchronized update the window waits out before drawing.
    synchronizing_since: Option<f64>,
    /// An agent terminal's annotations, once its session is known.
    marking: Option<annotating::Marking>,
    /// Its machine has streamed to this tile since the tile was drawn, so its screen is one
    /// the tile has shown.
    reached: bool,
    /// What the user typed into the terminal since a screen reader last looked.
    typed: Vec<spoken::Typing>,
}

#[derive(Debug)]
struct Pointer {
    /// The button whose press the program heard, owed its release.
    reported: Option<u8>,
    /// The cell of the last report, where a release off the grid lands.
    cell: Option<(u16, u16)>,
    /// Where the pointer last moved to, so a release at the same point is no motion.
    at: Option<(f32, f32)>,
    wheel: WheelRows,
    active_at: f64,
}

impl Default for Pointer {
    fn default() -> Pointer {
        Pointer {
            reported: None,
            cell: None,
            at: None,
            wheel: WheelRows::default(),
            active_at: f64::NEG_INFINITY,
        }
    }
}

/// Which links have their daemon's state.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
struct Up {
    home: bool,
    machine: bool,
}

impl Up {
    fn of(self, via: Reach) -> bool {
        match via {
            Reach::Home => self.home,
            Reach::Machine => self.machine,
        }
    }
}

/// The link a terminal of the session on `host` rides: this machine's own, for a session
/// that runs here, but the home's to revive one, which starts it again.
fn rides(facts: &Facts, host: Option<&str>, recoverable: bool) -> Reach {
    if facts.machine.runs(host) && !recoverable {
        Reach::Machine
    } else {
        Reach::Home
    }
}

/// The commands of the link `via` names.
fn link<'a>(via: Reach, home: &'a Commands, machine: &'a Commands) -> &'a Commands {
    match via {
        Reach::Home => home,
        Reach::Machine => machine,
    }
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
    /// Its machine is unreachable or still connecting: the screen stays as it was left, typing
    /// is refused and nothing streams until the machine is back.
    Away,
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
    fn new(agent: bool, theme: &TerminalColors) -> Term {
        Term {
            vt: themed(Vt::new(FIRST_SIZE.0, FIRST_SIZE.1), theme),
            grid: Grid::default(),
            holding_hidden_screen: false,
            stream: Stream::Attaching(Default::default()),
            phase: Phase::Waiting,
            via: Reach::Home,
            agent,
            place: links::Place::default(),
            recoverable: false,
            watermark: None,
            held: Vec::new(),
            tile: None,
            slot: None,
            caret: Live::new([0.0; 4]),
            sent_size: None,
            sent_at: f64::NEG_INFINITY,
            resize_due: None,
            pointer: Pointer::default(),
            view: selection::View::default(),
            blocks: Default::default(),
            placements: Default::default(),
            scrolled_back: 0,
            synchronizing_since: None,
            marking: None,
            reached: false,
            typed: Vec::new(),
        }
    }
}

/// A window's root hears its window come to the front or go.
pub(crate) fn window_focused(state: &mut crate::State, focused: bool, _: &mut Tx) {
    state.terminals.window_in_front(focused);
    if focused {
        state.terminals.window_activated(&state.daemon);
    }
}

/// The key of the element a terminal draws in.
pub(crate) fn slot_key(runtime_id: &str) -> String {
    format!("terminal.{runtime_id}")
}

/// The mark a terminal's slot carries, so the renderer finds the terminal it draws.
pub fn mark(runtime_id: &str) -> u32 {
    BuildHasherDefault::<DefaultHasher>::default().hash_one(runtime_id) as u32
}

impl Terminals {
    /// Keeps the terminals of `panes`, attached once drawn, and lets go of the rest.
    pub(crate) fn follow(
        &mut self,
        facts: &Facts,
        panes: &[&DesktopPane],
        keyboard: Option<String>,
        now: f64,
        daemon: &Commands,
    ) {
        self.follow_connection(facts, daemon);
        self.undrawn = true;
        let machines = crate::machine::Machines::of(facts);
        let drawn: Vec<(String, Option<String>, bool, links::Place, Reach, bool)> = panes
            .iter()
            .filter(|pane| !pane.runtime_id.is_empty())
            .map(|pane| {
                let session = facts.sessions.iter().find(|s| s.id == pane.session_id);
                let recoverable =
                    session.is_some_and(|s| s.terminal_exit.is_none() && s.state == "recoverable");
                let place = links::Place {
                    cwd: session
                        .map(|s| s.directory.clone())
                        .filter(|d| !d.is_empty()),
                    host: session
                        .and_then(|s| s.host.clone())
                        .filter(|h| !h.is_empty()),
                    away: machines.away(session.and_then(|s| s.host.as_deref())),
                };
                let agent = (pane.kind == "agent").then(|| pane.session_id.clone());
                let via = rides(facts, place.host.as_deref(), recoverable);
                let streams = match via {
                    Reach::Machine => true,
                    Reach::Home => {
                        !matches!(facts.connection, Connection::HomeUnreachable(_))
                            && crate::outpost::reach(place.host.as_deref(), &facts.outposts)
                                .streams()
                    }
                };
                (
                    pane.runtime_id.clone(),
                    agent,
                    recoverable,
                    place,
                    via,
                    streams,
                )
            })
            .collect();
        let gone: Vec<String> = self
            .panes
            .keys()
            .filter(|id| !drawn.iter().any(|(drawn, ..)| drawn == *id))
            .cloned()
            .collect();
        for id in gone {
            let mut term = self.panes.remove(&id).expect("listed above");
            self.finds.remove(&id);
            self.filters.remove(&id);
            self.images.forget(&id);
            self.latency.forget(&id);
            self.trace.lock().unwrap().forget(&id);
            save_leaving_note(term.marking.as_ref(), daemon);
            let rides = link(term.via, daemon, &self.machine);
            release(&id, &mut term, None, 0, rides);
            let mut screen = std::mem::take(&mut term.grid);
            screen.set_images(Vec::new(), 0);
            self.hidden_screens.insert(id.clone(), screen);
            if self.inbox.release(&id)
                && self.up.of(term.via)
                && matches!(
                    term.phase,
                    Phase::Attaching(_) | Phase::Retrying { .. } | Phase::Live
                )
            {
                let _ = link(term.via, daemon, &self.machine).send(&DetachSessionMessage {
                    id,
                    ..DetachSessionMessage::new()
                });
            }
        }
        self.hidden_screens.retain(|id, _| {
            facts
                .desktops
                .iter()
                .any(|desktop| desktop.panes.iter().any(|pane| pane.runtime_id == *id))
        });
        for (id, agent, recoverable, place, via, streams) in drawn {
            let hidden_screens = &mut self.hidden_screens;
            let theme = &self.theme;
            let term = self.panes.entry(id.clone()).or_insert_with_key(|id| {
                let mut term = Term::new(agent.is_some(), theme);
                if let Some(screen) = hidden_screens.remove(id) {
                    term.grid = screen;
                    term.holding_hidden_screen = true;
                }
                term
            });
            term.recoverable = recoverable;
            if term.via != via {
                let attached = matches!(
                    term.phase,
                    Phase::Attaching(_) | Phase::Retrying { .. } | Phase::Live
                );
                if attached && self.up.of(term.via) {
                    let _ = link(term.via, daemon, &self.machine).send(&DetachSessionMessage {
                        id: id.clone(),
                        ..DetachSessionMessage::new()
                    });
                }
                if attached {
                    term.phase = Phase::Waiting;
                    term.stream = Stream::Attaching(Default::default());
                    term.sent_size = None;
                }
                term.watermark = None;
                term.via = via;
            }
            term.reached |= streams;
            match (streams, term.phase) {
                (true, Phase::Away) => term.phase = Phase::Waiting,
                (false, Phase::Attaching(_) | Phase::Retrying { .. } | Phase::Live) => {
                    if self.up.of(term.via) {
                        let _ = link(term.via, daemon, &self.machine).send(&DetachSessionMessage {
                            id,
                            ..DetachSessionMessage::new()
                        });
                    }
                    Terminals::go_away(term);
                }
                (false, Phase::Waiting) => Terminals::go_away(term),
                _ => {}
            }
            term.place = place;
            let session = agent.filter(|s| !s.is_empty());
            if term.marking.as_ref().map(|m| &m.session) != session.as_ref() {
                save_leaving_note(term.marking.as_ref(), daemon);
                term.marking = session.map(annotating::Marking::new);
            }
        }
        self.attach_waiting(now, daemon);
        self.focus(keyboard, daemon);
    }

    /// Streams start over on a new connection, from the start when the daemon itself is new.
    /// Following the facts keeps a connection's change in order with the state it brought.
    fn follow_connection(&mut self, facts: &Facts, daemon: &Commands) {
        let up = Up {
            home: facts.connection == Connection::Connected,
            machine: facts.machine.connected,
        };
        if up.machine && facts.machine.daemon_instance != self.machine_instance {
            self.machine_instance
                .clone_from(&facts.machine.daemon_instance);
            for term in self.panes.values_mut().filter(|t| t.via == Reach::Machine) {
                term.watermark = None;
            }
        }
        let was = std::mem::replace(&mut self.up, up);
        for via in [Reach::Home, Reach::Machine] {
            if was.of(via) && !up.of(via) {
                for term in self.panes.values_mut().filter(|t| t.via == via) {
                    term.phase = Phase::Waiting;
                    term.sent_size = None;
                }
            }
        }
        if up.machine && !was.machine {
            self.focused = None;
        }
        if up.home == was.home {
            return;
        }
        self.snapshots.clear();
        if !up.home {
            return;
        }
        self.refetch_markings();
        if facts.daemon_instance != self.daemon_instance {
            self.daemon_instance.clone_from(&facts.daemon_instance);
            for term in self.panes.values_mut() {
                term.watermark = None;
            }
        }
        if self.inbox.is_main() {
            send_theme(&self.theme, daemon);
        }
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
        let Some(id) = keyboard else {
            return;
        };
        let via = self.panes.get(&id).map_or(Reach::Home, |t| t.via);
        if !self.up.of(via) {
            return;
        }
        let daemon = link(via, daemon, &self.machine);
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

    /// The window came to the front: claims the PTY of the terminal with its keyboard again.
    pub(crate) fn window_activated(&mut self, daemon: &Commands) {
        let keyboard = self.focused.take();
        self.focus(keyboard, daemon);
    }

    pub fn metrics(&mut self) -> Metrics {
        *self.metrics.get_or_insert_with(|| Metrics::of(FONT_SIZE))
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
        let scale_changed = term.slot.is_some_and(|(_, previous)| previous != scale);
        term.slot = Some(((w, h), scale));
        place_caret(term, &metrics);
        let size = metrics.fit(w, h, scale);
        if term.agent && mid_layout(size, term.vt.size(), (w, h), scale, &metrics) {
            return;
        }
        let next = Tile {
            size,
            pixels: metrics.pixels(size, scale),
        };
        if scale_changed && term.tile != Some(next) {
            term.sent_size = None;
        }
        term.tile = Some(next);
        let now = tx.clock();
        match &mut term.phase {
            Phase::Waiting => self.attach_waiting(now, daemon),
            Phase::Live => {
                let due = term.sent_at + RESIZE_INTERVAL;
                if now >= due {
                    let daemon = link(term.via, daemon, &self.machine);
                    send_resize(id, term, now, Pixels::Sent, daemon);
                } else {
                    term.resize_due = Some(due);
                    tx.wake_at(due);
                }
            }
            Phase::Attaching(attaching) | Phase::Retrying { attaching, .. } => {
                attaching.relaid = true;
            }
            Phase::Away | Phase::Stopped => {}
        }
    }

    /// Re-fits terminals `ids` when the display scale changes without moving their view-point
    /// rects, such as dragging a window between a 1x and 2x monitor.
    pub(crate) fn set_scale(&mut self, scale: f32, ids: &[String], daemon: &Commands) {
        let metrics = self.metrics();
        for id in ids {
            let Some(term) = self.panes.get_mut(id) else {
                continue;
            };
            let Some(((w, h), previous)) = term.slot else {
                continue;
            };
            if previous == scale {
                continue;
            }
            term.slot = Some(((w, h), scale));
            place_caret(term, &metrics);
            let size = metrics.fit(w, h, scale);
            if term.agent && mid_layout(size, term.vt.size(), (w, h), scale, &metrics) {
                continue;
            }
            let next = Tile {
                size,
                pixels: metrics.pixels(size, scale),
            };
            if term.tile == Some(next) {
                continue;
            }
            term.tile = Some(next);
            match &mut term.phase {
                Phase::Live => {
                    term.sent_size = None;
                    let at = term.sent_at;
                    send_resize(
                        id,
                        term,
                        at,
                        Pixels::Sent,
                        link(term.via, daemon, &self.machine),
                    );
                }
                Phase::Attaching(attaching) | Phase::Retrying { attaching, .. } => {
                    attaching.relaid = true;
                }
                Phase::Waiting | Phase::Away | Phase::Stopped => {}
            }
        }
    }

    fn attach_waiting(&mut self, now: f64, home: &Commands) {
        for (id, term) in &mut self.panes {
            if term.phase != Phase::Waiting || term.tile.is_none() || !self.up.of(term.via) {
                continue;
            }
            let daemon = link(term.via, home, &self.machine);
            let tile = term.tile.expect("checked above");
            let policy = if term.recoverable {
                Policy::Revive
            } else {
                Policy::SameAppRemount
            };
            let requested = policy.resizes_first().then_some(tile.size);
            self.inbox.claim(id);
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

    /// The machine is not streaming: whatever was typed for it is dropped, never replayed.
    fn go_away(term: &mut Term) {
        term.phase = Phase::Away;
        term.held.clear();
        term.sent_size = None;
        term.resize_due = None;
    }

    /// Whether terminal `id` waits for its machine, and whether the tile has shown its screen.
    pub(crate) fn away(&self, id: &str) -> Option<bool> {
        let term = self.panes.get(id)?;
        (term.phase == Phase::Away).then_some(term.reached)
    }

    /// What the terminals have to tell the person, once.
    pub(crate) fn take_said(&mut self) -> Option<String> {
        self.said.take()
    }

    /// Holds the screen while the program draws a synchronized update, and ends one that
    /// outlasts `SYNCHRONIZED_UPDATE_LIMIT`. True when the held screen is to be drawn now.
    fn follow_synchronized_update(term: &mut Term, now: f64, tx: &mut Tx) -> bool {
        match (term.vt.synchronizing(), term.synchronizing_since) {
            (true, None) => {
                term.synchronizing_since = Some(now);
                tx.wake_at(now + SYNCHRONIZED_UPDATE_LIMIT);
                false
            }
            (true, Some(since)) if now >= since + SYNCHRONIZED_UPDATE_LIMIT => {
                term.vt.end_synchronized_update();
                term.synchronizing_since = None;
                draw_even_when_resting(tx);
                true
            }
            (false, Some(_)) => {
                term.synchronizing_since = None;
                true
            }
            _ => false,
        }
    }

    /// Takes what the link heard since the last frame, `typing` being the terminal keys type
    /// into. Output changes nothing in the view beyond an open find bar's count: true when that
    /// count changed, or a snapshot arrived.
    pub(crate) fn drain(&mut self, typing: Option<&str>, tx: &mut Tx, daemon: &Commands) -> bool {
        let events = self.inbox.take();
        let heard = !events.is_empty();
        let mut pictured = false;
        for (received, from, event) in events {
            if !self.hears(from, &event) {
                continue;
            }
            pictured |= matches!(event, TerminalEvent::Snapshot(_));
            self.apply(event, received, tx, daemon);
            self.undrawn = true;
        }
        if let (true, Some(metrics)) = (heard, self.metrics) {
            self.panes
                .values_mut()
                .for_each(|term| place_caret(term, &metrics));
        }
        if heard {
            self.inbox.taken.beat();
        }
        let recounted = heard && self.refind();
        let now = tx.clock();
        for term in self.panes.values_mut() {
            if heard || term.synchronizing_since.is_some() {
                self.undrawn |= Terminals::follow_synchronized_update(term, now, tx);
            }
        }
        for (id, term) in &mut self.panes {
            let daemon = link(term.via, daemon, &self.machine);
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
        self.speak_quiet_output(typing, tx);
        recounted | pictured
    }

    /// Whether the window takes `event`, which came from link `from`: a terminal's stream comes
    /// only from the link it attached on, and what concerns its session only from the home.
    fn hears(&self, from: Reach, event: &TerminalEvent) -> bool {
        match terminal_of(event).and_then(|id| self.panes.get(id)) {
            Some(term) => term.via == from,
            None => from == Reach::Home || terminal_of(event).is_some(),
        }
    }

    fn apply(&mut self, event: TerminalEvent, received: Instant, tx: &mut Tx, home: &Commands) {
        let machine = self.machine.clone();
        let via = terminal_of(&event)
            .and_then(|id| self.panes.get(id))
            .map_or(Reach::Home, |t| t.via);
        let daemon = link(via, home, &machine);
        match event {
            TerminalEvent::Frame(Frame::PtyOutput {
                terminal,
                seq,
                data,
            }) => {
                let Some(term) = self
                    .panes
                    .get_mut(&terminal)
                    .filter(|t| t.phase != Phase::Away)
                else {
                    return;
                };
                if let Output::Write(bytes) = term.stream.receive(Chunk { seq, data }) {
                    term.watermark = Some(seq);
                    term.vt.write(&bytes);
                    self.trace
                        .lock()
                        .unwrap()
                        .output(&terminal, received, tx.clock());
                    copy_writes(term, tx);
                    Terminals::wrote(term);
                    answer(&terminal, term, daemon);
                }
            }
            TerminalEvent::Frame(Frame::KittyImage {
                terminal,
                image,
                generation,
                width,
                height,
                format,
                pixels,
            }) => self.image_arrived(
                &terminal,
                image,
                generation,
                (width, height),
                format,
                &pixels,
            ),
            TerminalEvent::Placements(described) => self.placements_described(
                &described.id,
                described.seq,
                &described.placements,
                daemon,
            ),
            TerminalEvent::ImageResult(result) => self.image_refused(&result),
            TerminalEvent::Snapshot(result) => self.snapshot_arrived(&result),
            TerminalEvent::Attached(result) => {
                let id = result.id.clone();
                let resumes = result.snapshot.is_some()
                    || self.panes.get(&id).is_some_and(|t| t.watermark.is_some());
                self.attached(*result, tx, daemon);
                self.restored(&id, resumes, tx);
                self.images.unanswered(&id);
                self.ask_images(&id, daemon);
            }
            TerminalEvent::Resized(resized) => {
                let metrics = self.metrics();
                if let Some(term) = self.panes.get_mut(&resized.id) {
                    let size = (clamp(resized.cols), clamp(resized.rows));
                    term.vt.resize(size.0, size.1, cell_pixels(&metrics));
                    if let Some(marking) = term.marking.as_mut() {
                        marking.reflowed();
                    }
                    Terminals::hover_moved(term);
                }
            }
            TerminalEvent::Desync(desync) => self.restart(&desync.id, None, tx.clock(), daemon),
            TerminalEvent::Respawned(respawned) => self.restart(
                &respawned.id,
                Some(Policy::RelaunchRestore),
                tx.clock(),
                daemon,
            ),
            TerminalEvent::MessagesChanged(changed) => self.messages_changed(&changed.session_id),
            TerminalEvent::Exited(exited) => {
                if let Some(term) = self.panes.get_mut(&exited.id) {
                    term.phase = Phase::Stopped;
                    term.holding_hidden_screen = false;
                    let notice = exit_notice(exited.exit_code, exited.signal.as_deref());
                    term.vt.write(format!("\r\n{notice}\r\n").as_bytes());
                    Terminals::wrote(term);
                }
            }
        }
    }

    /// The daemon replaced the terminal's stream: start the screen over from a new attach.
    fn restart(&mut self, id: &str, policy: Option<Policy>, now: f64, daemon: &Commands) {
        let Some(term) = self.panes.get_mut(id).filter(|t| t.phase != Phase::Away) else {
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
        Terminals::forget_screen(term);
        term.watermark = None;
        term.stream = Stream::Attaching(Default::default());
        if !self.up.of(term.via) {
            term.phase = Phase::Waiting;
            return;
        }
        term.phase = Phase::Attaching(Attaching {
            policy: policy.unwrap_or(Policy::SameAppRemount),
            requested: None,
            started: now,
            relaid: false,
        });
        self.inbox.claim(id);
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
            term.holding_hidden_screen = false;
            term.phase = Phase::Stopped;
            term.watermark = None;
            term.vt
                .write(format!("\r\n[Failed to attach PTY: {error}]\r\n").as_bytes());
            term.held.clear();
            return;
        }
        term.holding_hidden_screen = false;
        if result.running == Some(false)
            && let Some(screen) = &result.screen
        {
            term.phase = Phase::Stopped;
            term.vt = themed(Vt::new(clamp(screen.cols), clamp(screen.rows)), &self.theme);
            Terminals::forget_screen(term);
            let text = screen
                .text
                .trim_end()
                .replace("\r\n", "\n")
                .replace('\n', "\r\n");
            term.vt.write(text.as_bytes());
            Terminals::wrote(term);
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
            Terminals::forget_screen(term);
            let seeded = result.snapshot.as_ref().and_then(|s| s.blocks.as_deref());
            Terminals::seed_blocks(term, seeded.unwrap_or_default());
            let placed = result
                .snapshot
                .as_ref()
                .and_then(|s| s.placements.as_deref());
            Terminals::seed_placements(term, placed.unwrap_or_default());
        }
        if let Some((cols, rows)) = restore.size {
            term.vt.resize(cols, rows, cell);
            if let Some(marking) = term.marking.as_mut() {
                marking.reflowed();
            }
            Terminals::hover_moved(term);
        }
        if restore.snapshot.is_some() && !decoded {
            for chunk in &restore.fallback {
                term.vt.write(&chunk.data);
                Terminals::wrote(term);
            }
            term.vt.take_clipboard_writes();
        }
        for chunk in &restore.write {
            term.vt.write(&chunk.data);
            copy_writes(term, tx);
            Terminals::wrote(term);
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
        for (data, trace) in std::mem::take(&mut term.held) {
            let left = input_trace::hop_time();
            let socket_open = send_input(&id, &data, "user", Some(&trace), daemon);
            let write = Write {
                left,
                socket_open,
                connected: self.up.of(term.via),
                probe: None,
            };
            self.trace
                .lock()
                .unwrap()
                .sent(&trace, &id, write, tx.clock());
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

    /// Drops the input typed for `id` while it attaches; whether there was any.
    pub(crate) fn discard_held(&mut self, id: &str) -> bool {
        self.panes
            .get_mut(id)
            .is_some_and(|term| !std::mem::take(&mut term.held).is_empty())
    }

    /// Sends what the user typed or pasted to the terminal, held while it attaches, and traces
    /// what became of it.
    pub(crate) fn input(
        &mut self,
        id: &str,
        data: &[u8],
        trace: &TraceId,
        now: f64,
        daemon: &Commands,
    ) -> Typed {
        let Some(term) = self.panes.get_mut(id) else {
            self.trace
                .lock()
                .unwrap()
                .decide(trace, id, Decision::NoTarget, now);
            return Typed::Nowhere;
        };
        let via = term.via;
        let daemon = link(via, daemon, &self.machine);
        let outcome = match term.phase {
            Phase::Live => Decision::Sent,
            Phase::Waiting | Phase::Attaching(_) | Phase::Retrying { .. } => {
                term.held.push((data.to_vec(), trace.clone()));
                Decision::Held
            }
            Phase::Away => Decision::Refused,
            Phase::Stopped => Decision::Stopped,
        };
        self.trace.lock().unwrap().decide(trace, id, outcome, now);
        match outcome {
            Decision::Sent => {
                let left = input_trace::hop_time();
                let socket_open = send_input(id, data, "user", Some(trace), daemon);
                let write = Write {
                    left,
                    socket_open,
                    connected: self.up.of(via),
                    probe: None,
                };
                self.trace.lock().unwrap().sent(trace, id, write, now);
            }
            Decision::Refused => return Typed::Refused,
            _ => {}
        }
        Typed::Taken
    }

    pub(crate) fn connected(&self) -> bool {
        self.up.home
    }

    /// The link terminal `id` attaches on.
    pub(crate) fn rides(&self, id: &str) -> Reach {
        self.panes.get(id).map_or(Reach::Home, |t| t.via)
    }

    pub(crate) fn live(&self, id: &str) -> bool {
        self.panes
            .get(id)
            .is_some_and(|term| matches!(term.phase, Phase::Live))
    }

    pub(crate) fn encode_key(&mut self, id: &str, key: &KeyInput) -> Option<Vec<u8>> {
        Some(self.panes.get_mut(id)?.vt.encode_key(key))
    }

    pub(crate) fn encode_paste(&mut self, id: &str, text: &str) -> Option<Vec<u8>> {
        Some(self.panes.get_mut(id)?.vt.encode_paste(text))
    }

    /// The pointer moved to `at` over a terminal's slot, in its points, with a button `held`
    /// on this terminal or anywhere else.
    pub(crate) fn pointer_moved(
        &mut self,
        id: &str,
        at: (f32, f32),
        held: bool,
        (mods, accelerator): (u8, bool),
        now: f64,
        daemon: &Commands,
    ) -> bool {
        let pointing = self.hover(id, Some(at), accelerator);
        let metrics = self.metrics();
        let Some(term) = self.panes.get_mut(id) else {
            return pointing;
        };
        if term.pointer.at.replace(at) == Some(at) {
            return pointing;
        }
        if term.agent && self.up.home && now - term.pointer.active_at >= POINTER_ACTIVITY_INTERVAL {
            term.pointer.active_at = now;
            let _ = daemon.send(&TerminalPointerActivityMessage {
                id: id.into(),
                ..TerminalPointerActivityMessage::new()
            });
        }
        let modes = term.vt.mouse_modes();
        let Some(cell) = cell_at(term, &metrics, at).filter(|_| term.phase != Phase::Away) else {
            return pointing;
        };
        if !modes.tracking || !mouse::reports_move(modes, term.pointer.reported, held) {
            return pointing;
        }
        let report = mouse::report(Action::Move, term.pointer.reported, cell, modes.sgr, mods);
        send_input(
            id,
            report.as_bytes(),
            "pointer",
            None,
            link(term.via, daemon, &self.machine),
        );
        term.pointer.cell = Some(cell);
        pointing
    }

    /// The pointer left terminal `id`'s slot: nothing there is hovered any more.
    pub(crate) fn pointer_left(&mut self, id: &str) -> bool {
        self.hover(id, None, false)
    }

    /// `button` went down at `at` on a terminal's slot. A program that tracks the mouse hears
    /// it, unless Alt holds a primary or middle press for the terminal's own selection: true
    /// when the program heard it.
    pub(crate) fn pointer_pressed(
        &mut self,
        id: &str,
        button: u8,
        at: (f32, f32),
        mods: u8,
        daemon: &Commands,
    ) -> bool {
        let metrics = self.metrics();
        let Some(term) = self.panes.get_mut(id) else {
            return false;
        };
        let alt = mods & mouse::modifiers(false, true, false) != 0;
        if alt && button != mouse::SECONDARY {
            return false;
        }
        let modes = term.vt.mouse_modes();
        let Some(cell) =
            cell_at(term, &metrics, at).filter(|_| modes.tracking && term.phase != Phase::Away)
        else {
            return false;
        };
        let report = mouse::report(Action::Press, Some(button), cell, modes.sgr, mods);
        send_input(
            id,
            report.as_bytes(),
            "pointer",
            None,
            link(term.via, daemon, &self.machine),
        );
        term.pointer.reported = Some(button);
        term.pointer.cell = Some(cell);
        true
    }

    /// The button held on a terminal came up at `at`, wherever that is, or was cancelled with
    /// no point.
    pub(crate) fn pointer_released(
        &mut self,
        id: &str,
        at: Option<(f32, f32)>,
        mods: u8,
        daemon: &Commands,
    ) {
        let metrics = self.metrics();
        let Some(term) = self.panes.get_mut(id) else {
            return;
        };
        let cell = at.and_then(|at| cell_at(term, &metrics, at));
        release(id, term, cell, mods, link(term.via, daemon, &self.machine));
    }

    /// The wheel turned `dy` points over `at`: true when the terminal took it, reporting it to
    /// a program that tracks the mouse, as arrow keys on the alternate screen, or else
    /// scrolling the view through the history.
    pub(crate) fn wheel(&mut self, id: &str, dy: f32, at: (f32, f32), daemon: &Commands) -> bool {
        let metrics = self.metrics();
        let Some(term) = self.panes.get_mut(id) else {
            return false;
        };
        let row_h = term
            .slot
            .map_or(metrics.cell_h, |(_, scale)| metrics.cell_points(scale).1);
        let lines = term.pointer.wheel.take(-dy / row_h);
        let modes = term.vt.mouse_modes();
        if !modes.tracking && !modes.alternate_screen {
            if lines != 0 && Terminals::scroll_view(term, lines) {
                self.undrawn = true;
            }
            return true;
        }
        if lines != 0 && term.phase != Phase::Away {
            Terminals::anchor_selection(term);
            let cell = cell_at(term, &metrics, at).unwrap_or((0, 0));
            let source = if modes.tracking { "pointer" } else { "user" };
            send_input(
                id,
                mouse::wheel(lines, cell, modes).as_bytes(),
                source,
                None,
                link(term.via, daemon, &self.machine),
            );
        }
        true
    }

    /// The text on a terminal's screen, row by row with trailing blanks trimmed.
    pub fn screen_text(&mut self, id: &str) -> Option<Vec<String>> {
        let term = self.panes.get_mut(id)?;
        let mut grid = Grid::default();
        term.vt.sync(&mut grid);
        Some(grid.text_lines())
    }

    /// An input method's composition for terminal `id` so far.
    pub(crate) fn compose(&mut self, id: &str, text: &str) {
        self.preedit = Some((id.to_owned(), text.to_owned()));
    }

    /// The input method committed or dropped its composition, wherever the keyboard is now.
    pub(crate) fn end_composition(&mut self) {
        self.preedit = None;
    }

    /// Terminal `id`'s columns and rows, once it has a screen.
    pub(crate) fn size(&mut self, id: &str) -> Option<(u16, u16)> {
        self.panes.get_mut(id).map(|term| term.vt.size())
    }

    /// What an input method is composing for terminal `id`.
    pub(crate) fn preedit(&self, id: &str) -> Option<&str> {
        self.preedit
            .as_ref()
            .filter(|(composing, _)| composing == id)
            .map(|(_, text)| text.as_str())
    }

    /// The cell text goes to next in terminal `id`, as x, y, width and height in points from
    /// its slot's top-left: the top-left itself until the terminal is laid out.
    pub(crate) fn caret(&self, id: &str) -> Option<&Live<[f32; 4]>> {
        Some(&self.panes.get(id)?.caret)
    }

    /// The font size and line height a cell of terminal `id` draws at now.
    pub(crate) fn cell_font(&self, id: &str) -> Option<(f32, f32)> {
        let metrics = self.metrics?;
        let term = self.panes.get(id)?;
        let h = term.caret.get()[3];
        let cell_h = term
            .slot
            .map_or(metrics.cell_h, |(_, scale)| metrics.cell_points(scale).1);
        Some((FONT_SIZE * h / cell_h, h))
    }

    /// How tall a terminal's row stands at rest, in points, whatever grid a resize still waits on.
    pub(crate) fn row_height(&self) -> Option<f32> {
        self.metrics.map(|metrics| metrics.cell_h)
    }

    /// Whether this window shows terminal `id`.
    pub(crate) fn shows(&self, id: &str) -> bool {
        self.panes.contains_key(id)
    }

    /// Whether terminal `id` has its screen from the daemon, so its output can be read.
    pub(crate) fn has_output(&self, id: &str) -> bool {
        self.panes
            .get(id)
            .is_some_and(|term| matches!(term.phase, Phase::Live | Phase::Stopped))
    }

    /// Terminal `id`'s history and screen as text, one line per row.
    pub(crate) fn text(&mut self, id: &str) -> Option<String> {
        if !self.has_output(id) {
            return None;
        }
        let vt = &mut self.panes.get_mut(id)?.vt;
        let cols = vt.size().0;
        let rows = vt.history() + u32::from(vt.size().1);
        let lines: Vec<String> = (0..rows)
            .map(|row| vt.row_text(row, 0..cols).trim_end().to_owned())
            .collect();
        Some(lines.join("\n"))
    }

    /// What a screen reader reads of terminal `id`: its screen as the window last drew it, with
    /// the caret at the cursor.
    pub(crate) fn reader_text(&self, id: &str) -> Option<rune::DrawnText> {
        let (text, cursor) = self.panes.get(id)?.grid.text_and_cursor();
        Some(rune::DrawnText {
            text,
            anchor: cursor,
            focus: cursor,
            wraps: Vec::new(),
        })
    }

    /// The text of a terminal's screen as the window last drew it, row by row.
    pub fn drawn_text(&self, id: &str) -> Option<Vec<String>> {
        Some(self.panes.get(id)?.grid.text_lines())
    }

    /// The colors every terminal draws with until its program sets its own, which the daemon
    /// also answers color queries with.
    pub(crate) fn set_theme(&mut self, theme: TerminalColors, daemon: &Commands) {
        if theme == self.theme {
            return;
        }
        self.theme = theme;
        self.undrawn = true;
        for term in self.panes.values_mut() {
            term.vt.set_theme(&theme);
        }
        if self.up.home && self.inbox.is_main() {
            send_theme(&theme, daemon);
        }
    }

    /// Each drawn terminal's screen brought up to date, by the mark of the slot it draws in.
    pub(crate) fn grids(&mut self) -> impl Iterator<Item = (u32, &Grid)> {
        self.undrawn = false;
        self.refresh_hovers();
        let theme = self.theme;
        let metrics = self.metrics();
        let finds = &self.finds;
        let images = &mut self.images;
        let ids = &mut self.ids;
        self.panes.iter_mut().map(move |(id, term)| {
            Terminals::bring_up_to_date(images, finds, ids, &theme, metrics, id, term);
            (mark(id), &term.grid)
        })
    }

    /// Brings terminal `id`'s grid, images, overlays and scroll position up to what its screen
    /// holds now.
    fn bring_up_to_date(
        images: &mut kitty::Images,
        finds: &HashMap<String, find::Finding>,
        ids: &mut ids::Ids,
        theme: &TerminalColors,
        metrics: Metrics,
        id: &str,
        term: &mut Term,
    ) {
        if Terminals::grid_follows_screen(term) && term.vt.sync(&mut term.grid) {
            term.view.ids = None;
        }
        let top = term.vt.view_top();
        let cell = term
            .slot
            .map_or((0, 0), |(_, scale)| metrics.pixels((1, 1), scale));
        let placed = Terminals::kitty_images(images, id, term, top, cell);
        term.grid.set_images(placed, top);
        let mut overlays = Terminals::overlays(term, theme, finds.get(id));
        overlays.extend(Terminals::id_overlays(id, term, ids, theme));
        term.grid.set_overlays(overlays);
        term.scrolled_back = term.vt.scrolled_back();
    }

    /// Whether the terminal's grid takes what its screen holds, rather than keeping what it
    /// showed while the program draws a synchronized update or the screen is hidden.
    fn grid_follows_screen(term: &Term) -> bool {
        term.synchronizing_since.is_none() && !term.holding_hidden_screen
    }

    /// A frame drew this window's terminals: the echoes are drawn of each terminal whose grid
    /// took its screen.
    pub(crate) fn echoes_drawn(&self) {
        self.trace.lock().unwrap().drawn(|id| {
            self.panes
                .get(id)
                .is_some_and(Terminals::grid_follows_screen)
        });
    }

    /// Terminal `id` brought up to what its screen holds now, whether or not a frame drew it.
    fn current(&mut self, id: &str) -> Option<&Term> {
        let metrics = self.metrics();
        let term = self.panes.get_mut(id)?;
        let theme = &self.theme;
        Terminals::bring_up_to_date(
            &mut self.images,
            &self.finds,
            &mut self.ids,
            theme,
            metrics,
            id,
            term,
        );
        Some(term)
    }

    /// The laid-out terminals fitted for a display scale other than `scale`.
    pub(crate) fn fitted_for_other_scale(&self, scale: f32) -> impl Iterator<Item = &str> {
        self.panes
            .iter()
            .filter(move |(_, term)| term.slot.is_some_and(|(_, previous)| previous != scale))
            .map(|(id, _)| id.as_str())
    }

    /// Whether a screen changed since the renderer last took the grids.
    pub(crate) fn undrawn(&self) -> bool {
        self.undrawn
    }

    /// The size the terminal's own screen has, which follows the daemon's PTY.
    pub fn screen_size(&mut self, id: &str) -> Option<(u16, u16)> {
        Some(self.panes.get_mut(id)?.vt.size())
    }
}

/// Asks for the next frame from a poll a timer ran: a resting window draws for that poll only
/// when it asks.
fn draw_even_when_resting(tx: &mut Tx) {
    tx.wake_after(std::time::Duration::ZERO);
}

/// Puts on the clipboard what the program's clipboard writes asked for, without a gesture.
fn copy_writes(term: &mut Term, tx: &mut Tx) {
    for text in term.vt.take_clipboard_writes() {
        tx.copy(text);
    }
}

/// Sends the replies the terminal wrote to the program's queries, less those the daemon
/// answers itself.
fn answer(id: &str, term: &mut Term, daemon: &Commands) {
    let replies = responses::without_daemon_owned(&term.vt.take_responses());
    if !replies.is_empty() {
        send_input(id, &replies, "response", None, daemon);
    }
}

fn place_caret(term: &mut Term, metrics: &Metrics) {
    if let Some((slot, scale)) = term.slot {
        let rect = metrics.cell_rect(term.vt.size(), slot, scale, term.vt.cursor());
        term.caret.set(rect);
    }
}

fn cell_at(term: &mut Term, metrics: &Metrics, at: (f32, f32)) -> Option<(u16, u16)> {
    let (slot, scale) = term.slot?;
    metrics.cell_at(term.vt.size(), slot, scale, at)
}

/// Sends the release a program is owed for the press it heard, at `cell` or where it last
/// heard the pointer.
fn release(id: &str, term: &mut Term, cell: Option<(u16, u16)>, mods: u8, daemon: &Commands) {
    if term.pointer.reported.take().is_none() || term.phase == Phase::Away {
        return;
    }
    let Some(cell) = cell.or(term.pointer.cell.take()) else {
        return;
    };
    let sgr = term.vt.mouse_modes().sgr;
    let report = mouse::report(Action::Release, None, cell, sgr, mods);
    send_input(id, report.as_bytes(), "pointer", None, daemon);
}

/// Whether the write left for the daemon.
fn send_input(
    id: &str,
    data: &[u8],
    source: &'static str,
    trace: Option<&TraceId>,
    daemon: &Commands,
) -> bool {
    daemon
        .send(&PtyInputMessage {
            id: id.into(),
            data: String::from_utf8_lossy(data).into_owned(),
            source: Some(source.into()),
            trace_id: trace.cloned(),
            ..PtyInputMessage::new()
        })
        .is_ok()
}

/// Appends `line` to the debug log at `path`, starting it over with a marker once it would
/// pass its cap.
pub(crate) fn append_capped(path: &std::path::Path, line: &str, at_ms: i64) -> std::io::Result<()> {
    use std::io::Write;
    const LOG_CAP_BYTES: u64 = 8 * 1024 * 1024;
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir)?;
    }
    let size = std::fs::metadata(path).map_or(0, |m| m.len());
    let full = size + line.len() as u64 > LOG_CAP_BYTES;
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .append(!full)
        .write(true)
        .truncate(full)
        .open(path)?;
    if full {
        writeln!(
            file,
            "{}",
            serde_json::json!({ "at": at_ms, "kind": "rotate" })
        )?;
    }
    file.write_all(line.as_bytes())
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
/// PTY the daemon attached at another size leaves it out.
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
/// terminal's grid is healthy and fits the tile. A grid already
/// that small is followed as it grows; one that overflows the tile is always refit.
fn mid_layout(
    size: (u16, u16),
    grid: (u16, u16),
    (w, h): (f32, f32),
    scale: f32,
    metrics: &Metrics,
) -> bool {
    let unusable = |(cols, rows): (u16, u16)| cols < AGENT_USABLE.0 || rows < AGENT_USABLE.1;
    if !unusable(size) {
        return false;
    }
    let (cell_w, cell_h) = metrics.cell_points(scale);
    let overflows = f32::from(grid.1) * cell_h > h + 1.0 || f32::from(grid.0) * cell_w > w + 1.0;
    let shrinks = size.0 < grid.0 || size.1 < grid.1;
    !overflows && !(unusable(grid) && !shrinks)
}

/// A refusal the daemon gives while the terminal's worker restarts.
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

fn save_leaving_note(marking: Option<&annotating::Marking>, daemon: &Commands) {
    if let Some(save) = marking.and_then(annotating::Marking::leaving) {
        let _ = daemon.send(&save);
    }
}

/// What the terminal says when its process ends: the daemon ends sessions with SIGHUP, SIGINT
/// or SIGTERM, which are not crashes.
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

fn themed(mut vt: Vt, theme: &TerminalColors) -> Vt {
    vt.set_theme(theme);
    vt
}

fn send_theme(theme: &TerminalColors, daemon: &Commands) {
    let _ = daemon.send(&SetTerminalThemeMessage {
        foreground: to_hex(theme.fg),
        background: to_hex(theme.bg),
        cursor: to_hex(theme.cursor),
        ansi_palette: theme.ansi.iter().map(|&c| to_hex(c)).collect(),
        ..SetTerminalThemeMessage::new()
    });
}
