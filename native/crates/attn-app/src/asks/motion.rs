//! The title bar asks button calling for attention: once when a live open ask arrives, and
//! again every few minutes while live asks wait with the sheet closed, as the person chose.

use std::collections::{BTreeMap, HashSet};

use attn_ui::system::attention::PopBloom;
use rune::Tx;

use super::Lists;
use crate::State;

pub(crate) const MOTION: &str = "asks_button_motion";
pub(crate) const REMIND_EVERY: &str = "asks_button_remind_seconds";
pub(crate) const OFF: &str = "off";
pub(crate) const ARRIVAL: &str = "arrival";
pub(crate) const REMIND: &str = "remind";
pub(crate) const INTERVALS: [&str; 4] = ["30", "60", "120", "300"];
const DEFAULT_REMIND_SECONDS: f64 = 60.0;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Mode {
    Off,
    Arrival,
    Remind,
}

impl Mode {
    pub(crate) fn of(settings: &BTreeMap<String, String>) -> Mode {
        match settings.get(MOTION).map(String::as_str) {
            Some(OFF) => Mode::Off,
            Some(ARRIVAL) => Mode::Arrival,
            _ => Mode::Remind,
        }
    }
}

pub(crate) fn remind_every(settings: &BTreeMap<String, String>) -> f64 {
    settings
        .get(REMIND_EVERY)
        .and_then(|seconds| seconds.parse().ok())
        .unwrap_or(DEFAULT_REMIND_SECONDS)
}

#[derive(Default)]
pub(crate) struct Motion {
    /// The profile and its live open asks as last seen on a connection; none before the
    /// connection's first facts, which only seed it.
    seen: Option<(String, HashSet<String>)>,
    sheet_open: bool,
    pub(crate) pulse: Option<Pulse>,
    reminder: Option<Reminder>,
    played: u64,
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct Pulse {
    pub kind: PopBloom,
    pub played: u64,
    until: f64,
}

#[derive(Clone, Copy)]
struct Reminder {
    at: f64,
    every: f64,
}

/// Follows the live open asks, the sheet and the settings: plays an arrival for a live ask
/// that was not live before, and keeps one reminder armed while live asks wait unseen.
pub(crate) fn sync(state: &mut State, tx: &mut Tx) {
    let connected = state.facts.connection == attn_client::Connection::Connected;
    let sheet_open = super::is_open(state);
    let mode = Mode::of(&state.facts.settings);
    let every = remind_every(&state.facts.settings);
    let profile = state.facts.profiles.selected.clone();
    let live: HashSet<String> = Lists::of(&state.facts, state.window.now_ms)
        .live
        .iter()
        .map(|item| item.ask.id.clone())
        .collect();
    let now = tx.clock();
    let motion = &mut state.window.asks.motion;
    if !connected {
        *motion = Motion::default();
    }
    motion.sheet_open = sheet_open;
    if !connected {
        return;
    }
    let arrived = match &motion.seen {
        Some((was, seen)) if *was == profile && live.iter().any(|id| !seen.contains(id)) => {
            Some(if seen.is_empty() {
                PopBloom::ArrivalFromZero
            } else {
                PopBloom::Arrival
            })
        }
        _ => None,
    };
    let waiting = !live.is_empty() && !sheet_open && mode != Mode::Off;
    motion.seen = Some((profile, live));
    if !waiting {
        motion.pulse = None;
        motion.reminder = None;
        return;
    }
    if let Some(kind) = arrived {
        motion.play(kind, now, tx);
    }
    motion.reminder = match (mode, motion.reminder) {
        (Mode::Remind, Some(armed)) if arrived.is_none() && armed.every == every => Some(armed),
        (Mode::Remind, _) => Some(Reminder::arm(now, every, tx)),
        _ => None,
    };
}

/// Lets a played pulse go, plays a reminder that is due, and follows the sheet opening or
/// closing however it did. Whether what the button draws changed.
pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    if super::is_open(state) != state.window.asks.motion.sheet_open {
        sync(state, tx);
        return true;
    }
    let motion = &mut state.window.asks.motion;
    let mut changed = false;
    if motion.pulse.is_some_and(|pulse| now >= pulse.until) {
        motion.pulse = None;
        changed = true;
    }
    if let Some(reminder) = motion.reminder
        && now >= reminder.at
    {
        motion.reminder = Some(Reminder::arm(now, reminder.every, tx));
        motion.play(PopBloom::Reminder, now, tx);
        changed = true;
    }
    changed
}

impl Motion {
    fn play(&mut self, kind: PopBloom, now: f64, tx: &mut Tx) {
        self.played += 1;
        let until = now + kind.duration();
        self.pulse = Some(Pulse {
            kind,
            played: self.played,
            until,
        });
        tx.wake_at(until);
    }
}

impl Reminder {
    fn arm(now: f64, every: f64, tx: &mut Tx) -> Reminder {
        let at = now + every;
        tx.wake_at(at);
        Reminder { at, every }
    }
}
