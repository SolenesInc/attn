use std::collections::{BTreeMap, HashMap, VecDeque};
use std::path::{Path, PathBuf};
use std::time::Instant;

use attn_client::Facts;
use rune::{Key, KeyEvent, Mods};
use serde::Serialize;
use serde_json::{Value, json};

/// Events the trace keeps, as the Tauri app's support trace does.
pub(crate) const TRACE_CAPACITY: usize = 512;
/// Decisions each terminal's sample carries, as the Tauri app's input observer keeps.
const RECENT_LIMIT: usize = 32;
/// Seconds between a terminal's samples while nothing calls for one sooner, and between two
/// samples for the same reason, as in the Tauri app.
const SAMPLE_EVERY: f64 = 30.0;
const REASON_COOLDOWN: f64 = 30.0;

pub(crate) const DIAGNOSTICS_FILE: &str = "terminal-diagnostics.jsonl";

/// One key, paste or composition, from the window that heard it to the daemon's socket, named
/// by the id the daemon's own trace echoes.
pub(crate) type TraceId = String;

#[derive(Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
enum Stage {
    Document,
    Terminal,
    Transport,
    Echo,
}

/// What became of an input at the terminal: the Tauri terminal's outcomes, and `held`,
/// `refused` and `stopped` for the native terminal's phases.
#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum Decision {
    Sent,
    Held,
    Refused,
    Stopped,
    NoTarget,
    Unmapped,
    Empty,
    Intercepted,
    Started,
}

impl Decision {
    fn wire(self) -> &'static str {
        match self {
            Decision::Sent => "sent",
            Decision::Held => "held",
            Decision::Refused => "refused",
            Decision::Stopped => "stopped",
            Decision::NoTarget => "no_target",
            Decision::Unmapped => "unmapped",
            Decision::Empty => "empty",
            Decision::Intercepted => "intercepted",
            Decision::Started => "started",
        }
    }

    /// The reason this outcome writes a sample at once.
    fn urgent(self) -> Option<&'static str> {
        match self {
            Decision::Refused | Decision::Stopped | Decision::NoTarget => Some(self.wire()),
            _ => None,
        }
    }
}

/// What the input was, without what it typed.
#[derive(Clone, Copy)]
pub(crate) enum Heard<'a> {
    Key(&'a KeyEvent, bool),
    Paste,
    CompositionStart,
    CompositionEnd,
}

impl Heard<'_> {
    fn event(&self) -> &'static str {
        match self {
            Heard::Key(..) => "keydown",
            Heard::Paste => "paste",
            Heard::CompositionStart => "compositionstart",
            Heard::CompositionEnd => "compositionend",
        }
    }
}

/// The element that held the keyboard when the input arrived, in the Tauri app's words.
#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum Target {
    Terminal,
    TerminalFind,
    TerminalFilter,
    Editor,
    Control,
    None,
}

impl Target {
    /// The element whose key is `focused`, `field` when it is a text field.
    pub(crate) fn of(focused: Option<&str>, field: bool, terminal: bool) -> Target {
        match focused {
            Some(key) if field && key.ends_with(".find.field") => Target::TerminalFind,
            Some(key) if field && key.ends_with(".filter.field") => Target::TerminalFilter,
            Some(_) if field => Target::Editor,
            Some(_) => Target::Control,
            None if terminal => Target::Terminal,
            None => Target::None,
        }
    }

    fn wire(self) -> &'static str {
        match self {
            Target::Terminal => "terminal",
            Target::TerminalFind => "terminal_find",
            Target::TerminalFilter => "terminal_filter",
            Target::Editor => "editor",
            Target::Control => "control",
            Target::None => "none",
        }
    }
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct Event {
    sequence: u64,
    trace_id: TraceId,
    at_unix_ms: i64,
    monotonic_ms: f64,
    stage: Stage,
    event: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    runtime_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    target: Option<&'static str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    outcome: Option<&'static str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    key_class: Option<&'static str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    repeat: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    modifiers: Option<u8>,
    #[serde(skip_serializing_if = "Option::is_none")]
    socket_state: Option<u8>,
    #[serde(skip_serializing_if = "Option::is_none")]
    initial_state_received: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    probe_id: Option<String>,
    /// When it happened on the trace's own clock, for the key's arrival and its echo's hops,
    /// which a frame's clock can't time.
    #[serde(skip_serializing_if = "Option::is_none")]
    instant_ms: Option<f64>,
}

impl Event {
    /// The event as a terminal's sample lists it, in the Tauri observer's shape.
    fn recent(&self) -> Value {
        let mut entry = json!({ "at": self.at_unix_ms, "event": self.event });
        let fields = [
            ("runtimeId", self.runtime_id.clone().map(Value::from)),
            ("outcome", self.outcome.map(Value::from)),
            ("keyClass", self.key_class.map(Value::from)),
            ("repeat", self.repeat.map(Value::from)),
            ("modifiers", self.modifiers.map(Value::from)),
            ("socketState", self.socket_state.map(Value::from)),
            (
                "initialStateReceived",
                self.initial_state_received.map(Value::from),
            ),
        ];
        for (name, value) in fields {
            if let Some(value) = value {
                entry[name] = value;
            }
        }
        entry
    }

    fn counter(&self) -> String {
        match self.outcome {
            Some(outcome) => format!("{}:{outcome}", self.event),
            None => self.event.to_owned(),
        }
    }
}

/// What one terminal's samples carry.
#[derive(Default)]
struct Observed {
    recent: VecDeque<Event>,
    counts: BTreeMap<String, u32>,
    last_send: Option<Event>,
    last_sampled: Option<f64>,
    reported: HashMap<&'static str, f64>,
    due: Vec<&'static str>,
}

/// A bounded trace of the keys, pastes and compositions the app's windows heard, the decision the
/// terminal made on each and the write it sent, never what they typed. Each terminal's
/// decisions are sampled to `terminal-diagnostics.jsonl`, where `attn debug input` reads them.
#[derive(Default)]
pub struct InputTrace {
    log: Option<PathBuf>,
    wall: crate::WallClock,
    started_ms: i64,
    next_trace: u64,
    total: u64,
    events: VecDeque<Event>,
    last_decided: Option<TraceId>,
    field_trace: Option<TraceId>,
    observed: HashMap<String, Observed>,
    epoch: Epoch,
    /// The last input sent to each terminal, until the first output that left after it.
    awaiting: HashMap<String, Awaiting>,
    /// Inputs whose echo a terminal took, until a frame draws that terminal's screen.
    drawing: Vec<(String, TraceId, f64)>,
}

struct Awaiting {
    trace: TraceId,
    left: Instant,
}

/// The write that carries an input to the daemon: when it left, and the connection it left on.
pub(crate) struct Write<'a> {
    pub left: Instant,
    pub socket_open: bool,
    pub connected: bool,
    pub probe: Option<&'a str>,
}

struct Epoch(Instant);

impl Default for Epoch {
    fn default() -> Epoch {
        Epoch(hop_time())
    }
}

/// The moment a hop happens, on the platform's monotonic clock: the clock a key's arrival
/// comes on, so every hop measures against it. No test clock can stand in for it.
#[allow(clippy::disallowed_methods)]
pub(crate) fn hop_time() -> Instant {
    Instant::now()
}

impl InputTrace {
    pub(crate) fn log_to(&mut self, debug_dir: &Path) {
        self.log = Some(debug_dir.join(DIAGNOSTICS_FILE));
    }

    pub(crate) fn started_at(&mut self, wall: crate::WallClock) {
        self.started_ms = wall.now_ms();
        self.wall = wall;
    }

    pub(crate) fn log(&self) -> Option<&Path> {
        self.log.as_deref()
    }

    pub(crate) fn forget(&mut self, runtime: &str) {
        self.observed.remove(runtime);
        self.awaiting.remove(runtime);
        self.drawing.retain(|(r, ..)| r != runtime);
    }

    /// A key a focused text field heard first; the window's key handler continues its trace
    /// when the field passes the key on.
    pub(crate) fn field_heard(&mut self, heard: Heard, target: Target, now: f64) {
        self.field_trace = Some(self.heard(heard, target, now));
    }

    /// An input the window heard, continuing the trace of a key its text field passed on.
    pub(crate) fn window_heard(
        &mut self,
        heard: Heard,
        target: Target,
        passed_on: bool,
        now: f64,
    ) -> TraceId {
        match self.field_trace.take().filter(|_| passed_on) {
            Some(trace) => trace,
            None => self.heard(heard, target, now),
        }
    }

    fn heard(&mut self, heard: Heard, target: Target, now: f64) -> TraceId {
        self.next_trace += 1;
        let trace = format!(
            "n{}-{}",
            base36(self.started_ms as u64),
            base36(self.next_trace)
        );
        let (key_class, repeat, modifiers) = match heard {
            Heard::Key(ev, repeat) => (Some(key_class(ev)), Some(repeat), Some(modifiers(ev.mods))),
            _ => (None, None, None),
        };
        let event = Event {
            target: Some(target.wire()),
            key_class,
            repeat,
            modifiers,
            ..self.event(&trace, Stage::Document, heard.event(), None, now)
        };
        self.push(event);
        trace
    }

    /// The terminal's decision on `trace`'s input.
    pub(crate) fn decide(&mut self, trace: &TraceId, runtime: &str, outcome: Decision, now: f64) {
        let Some(event) = self.events.iter().rev().find(|e| &e.trace_id == trace) else {
            return;
        };
        let decision = Event {
            outcome: Some(outcome.wire()),
            key_class: event.key_class,
            repeat: event.repeat,
            modifiers: event.modifiers,
            ..self.event(trace, Stage::Terminal, event.event, Some(runtime), now)
        };
        self.last_decided = Some(trace.clone());
        self.observe(&decision, outcome.urgent(), now);
        self.push(decision);
    }

    /// What the window decided on an input no terminal decided on: a terminal held the
    /// keyboard, and something else took the key or nothing did.
    pub(crate) fn settle(&mut self, trace: &TraceId, runtime: Option<&str>, taken: bool, now: f64) {
        if self.last_decided.as_ref() == Some(trace) {
            return;
        }
        if let Some(runtime) = runtime {
            let outcome = if taken {
                Decision::Intercepted
            } else {
                Decision::Unmapped
            };
            self.decide(trace, runtime, outcome, now);
        }
    }

    /// The write that carries `trace`'s input left for the daemon, or found no connection.
    pub(crate) fn sent(&mut self, trace: &TraceId, runtime: &str, write: Write, now: f64) {
        let event = Event {
            socket_state: Some(if write.socket_open { 1 } else { 3 }),
            initial_state_received: Some(write.connected),
            probe_id: write.probe.map(str::to_owned),
            instant_ms: Some(self.ms(write.left)),
            ..self.event(
                trace,
                Stage::Transport,
                "websocket_send",
                Some(runtime),
                now,
            )
        };
        let awaiting = Awaiting {
            trace: trace.clone(),
            left: write.left,
        };
        self.awaiting.insert(runtime.to_owned(), awaiting);
        let unready = (!write.socket_open || !write.connected).then_some("transport_unready");
        let observed = self.observed.entry(runtime.to_owned()).or_default();
        observed.last_send = Some(event.clone());
        self.observe(&event, unready, now);
        self.push(event);
    }

    /// The key behind `trace` reached the window at `arrived`, which its heard event carries.
    pub(crate) fn arrived(&mut self, trace: &TraceId, arrived: Instant) {
        let at = self.ms(arrived);
        if let Some(heard) = self
            .events
            .iter_mut()
            .rev()
            .find(|e| &e.trace_id == trace && e.stage == Stage::Document)
        {
            heard.instant_ms = Some(at);
        }
    }

    /// Terminal `runtime` took output the link heard at `received`: the echo of the last input
    /// sent to it, when it is the first output heard after that input left.
    pub(crate) fn output(&mut self, runtime: &str, received: Instant, now: f64) {
        let Some(awaiting) = self
            .awaiting
            .get(runtime)
            .filter(|awaiting| received >= awaiting.left)
        else {
            return;
        };
        let trace = awaiting.trace.clone();
        self.awaiting.remove(runtime);
        for (event, at) in [("output_received", received), ("output_taken", hop_time())] {
            let event = Event {
                instant_ms: Some(self.ms(at)),
                ..self.event(&trace, Stage::Echo, event, Some(runtime), now)
            };
            self.push(event);
        }
        self.drawing.push((runtime.to_owned(), trace, now));
    }

    /// A frame that draws the screens of the terminals `shown` names is encoded: the echoes
    /// those terminals took are drawn.
    pub(crate) fn drawn(&mut self, shown: impl Fn(&str) -> bool) {
        if self.drawing.is_empty() {
            return;
        }
        let at = self.ms(hop_time());
        let (drawn, waiting) = std::mem::take(&mut self.drawing)
            .into_iter()
            .partition(|(runtime, ..)| shown(runtime));
        self.drawing = waiting;
        for (runtime, trace, now) in drawn {
            let event = Event {
                instant_ms: Some(at),
                ..self.event(&trace, Stage::Echo, "echo_drawn", Some(&runtime), now)
            };
            self.push(event);
        }
    }

    fn ms(&self, at: Instant) -> f64 {
        at.saturating_duration_since(self.epoch.0).as_secs_f64() * 1000.0
    }

    /// Writes the samples the last input made due, naming each terminal's pane and session in
    /// `facts`.
    pub(crate) fn write_due(&mut self, facts: &Facts, now: f64) {
        let Some(log) = self.log.clone() else {
            for observed in self.observed.values_mut() {
                observed.due.clear();
            }
            return;
        };
        let at = self.wall_ms();
        for (runtime, observed) in &mut self.observed {
            if observed.due.is_empty() {
                continue;
            }
            let reasons = std::mem::take(&mut observed.due);
            observed.last_sampled = Some(now);
            let pane = facts
                .desktops
                .iter()
                .flat_map(|d| &d.panes)
                .find(|p| &p.runtime_id == runtime);
            let pane_id = pane.map(|p| p.pane_id.clone());
            let session = pane.map(|p| p.session_id.clone()).filter(|s| !s.is_empty());
            let record = json!({
                "at": at,
                "kind": "input",
                "schema": 1,
                "reasons": reasons,
                "appStartedAt": self.started_ms,
                "pane": pane_id,
                "session": session,
                "runtimeId": runtime,
                "state": { "runtimeId": runtime, "paneId": pane_id, "sessionId": session },
                "counts": observed.counts,
                "recent": observed.recent.iter().map(Event::recent).collect::<Vec<_>>(),
                "lastSend": observed.last_send.as_ref().map(Event::recent),
            });
            if let Err(why) = super::append_capped(&log, &format!("{record}\n"), at) {
                eprintln!("attn-app: could not log a terminal input sample: {why}");
            }
        }
    }

    /// The trace's events, oldest first, as a test reads them.
    pub(crate) fn events(&self) -> Value {
        json!(self.events)
    }

    /// The trace as the report's `diagnostics.input.frontend` carries it.
    pub(crate) fn snapshot(&self, now: f64) -> Value {
        json!({
            "capacity": TRACE_CAPACITY,
            "total": self.total,
            "capturedAtUnixMs": self.wall_ms(),
            "capturedAtMonotonicMs": now * 1000.0,
            "events": self.events,
        })
    }

    fn observe(&mut self, event: &Event, urgent: Option<&'static str>, now: f64) {
        let Some(runtime) = &event.runtime_id else {
            return;
        };
        let observed = self.observed.entry(runtime.clone()).or_default();
        *observed.counts.entry(event.counter()).or_default() += 1;
        if observed.recent.len() == RECENT_LIMIT {
            observed.recent.pop_front();
        }
        observed.recent.push_back(event.clone());
        let reason = urgent.filter(|reason| {
            observed
                .reported
                .get(reason)
                .is_none_or(|at| now - at >= REASON_COOLDOWN)
        });
        if let Some(reason) = reason {
            observed.reported.insert(reason, now);
            if !observed.due.contains(&reason) {
                observed.due.push(reason);
            }
        } else if observed.due.is_empty()
            && observed
                .last_sampled
                .is_none_or(|at| now - at >= SAMPLE_EVERY)
        {
            observed.due.push("sample");
        }
    }

    fn event(
        &self,
        trace: &TraceId,
        stage: Stage,
        event: &'static str,
        runtime: Option<&str>,
        now: f64,
    ) -> Event {
        Event {
            sequence: 0,
            trace_id: trace.clone(),
            at_unix_ms: self.wall_ms(),
            monotonic_ms: now * 1000.0,
            stage,
            event,
            runtime_id: runtime.map(str::to_owned),
            target: None,
            outcome: None,
            key_class: None,
            repeat: None,
            modifiers: None,
            socket_state: None,
            initial_state_received: None,
            probe_id: None,
            instant_ms: None,
        }
    }

    fn push(&mut self, mut event: Event) {
        self.total += 1;
        event.sequence = self.total;
        if self.events.len() == TRACE_CAPACITY {
            self.events.pop_front();
        }
        self.events.push_back(event);
    }

    fn wall_ms(&self) -> i64 {
        self.wall.now_ms()
    }
}

fn key_class(ev: &KeyEvent) -> &'static str {
    match &ev.key {
        Key::Text(text) if text.chars().count() == 1 => "text",
        Key::Command(_) => "text",
        Key::Modifier { .. } => "modifier",
        Key::Enter => "enter",
        Key::Escape => "escape",
        _ => "other",
    }
}

fn modifiers(mods: Mods) -> u8 {
    u8::from(mods.shift)
        | u8::from(mods.ctrl) << 1
        | u8::from(mods.alt) << 2
        | u8::from(mods.logo) << 3
}

fn base36(mut n: u64) -> String {
    const DIGITS: &[u8; 36] = b"0123456789abcdefghijklmnopqrstuvwxyz";
    let mut out = Vec::new();
    loop {
        out.push(DIGITS[(n % 36) as usize]);
        n /= 36;
        if n == 0 {
            break;
        }
    }
    out.reverse();
    String::from_utf8(out).expect("ascii digits")
}
