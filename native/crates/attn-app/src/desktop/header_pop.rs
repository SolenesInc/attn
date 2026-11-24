use attn_client::Facts;
use attn_ui::Tokens;
use attn_ui::blocks::Actions;
use attn_ui::system::keys_hint;
use rune::{El, Key, KeyEvent, Tx};

use crate::State;
use crate::action::Action;

/// What a header popover shows about its tile's agent.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum What {
    Usage,
    /// The details of the session's pull requests.
    Details,
    /// The delegation chain the session sits in.
    Chain,
    /// The agent's open asks.
    Ask,
    /// The seeds the agent tends and the one it reports to.
    Seed,
}

impl What {
    fn part(self) -> &'static str {
        match self {
            What::Usage => "usage",
            What::Details => "details",
            What::Chain => "role",
            What::Ask => "ask",
            What::Seed => "seed",
        }
    }

    fn shown_for(self, session: &attn_protocol::Session, facts: &Facts) -> bool {
        match self {
            What::Usage => super::usage::shown(session).is_some(),
            What::Details => super::provenance::pull_request(session).is_some(),
            What::Chain => super::chain::shown(session, facts),
            What::Ask => !crate::asks::of_session(facts, session).is_empty(),
            What::Seed => super::seed_chip::shown(session, facts),
        }
    }

    /// The row a popover listing several things picks first.
    fn first_pick(self, session: &str, facts: &Facts) -> usize {
        match self {
            What::Chain => super::chain::current_row(session, facts),
            What::Seed => super::seed_chip::first_pick(session, facts),
            What::Usage | What::Details | What::Ask => 0,
        }
    }

    /// Scrolls a popover listing several things to the row it picks.
    fn reveal(self, pop: &Pop, picked: usize, tx: &mut Tx) {
        if self == What::Chain {
            super::chain::reveal(pop, picked, tx);
        }
    }

    /// How long the pointer rests before the popover opens, and how long it may be away
    /// before the popover closes, in seconds.
    fn delays(self) -> (f64, f64) {
        match self {
            What::Usage | What::Ask | What::Seed => (0.16, 0.24),
            What::Details | What::Chain => (0.0, 0.12),
        }
    }
}

fn has(what: What, session: &str, facts: &Facts) -> bool {
    facts
        .sessions
        .iter()
        .find(|s| s.id == session)
        .is_some_and(|s| what.shown_for(s, facts))
}

/// Where a popover hangs: a piece of a tile's header, or a sidebar row's delegation chain.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Host {
    Tile(String),
    Row(String),
}

/// A tile header's popover, or a sidebar row's chain: the piece it hangs from, and whether a
/// click, the keyboard or the Action menu pinned it, which keeps it until Escape or a press
/// outside it and gives it the keyboard.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Pop {
    pub(crate) host: Host,
    pub(crate) session: String,
    pub(crate) what: What,
    pub(crate) pinned: bool,
}

impl Pop {
    pub(crate) fn anchor(&self) -> String {
        match &self.host {
            Host::Tile(leaf) => anchor(leaf, self.what),
            Host::Row(row) => row_anchor(row),
        }
    }

    pub(crate) fn on(&self, host: &Host, what: What) -> bool {
        self.host == *host && self.what == what
    }

    /// The popover's own key.
    pub(crate) fn surface(&self) -> String {
        format!("{}.popover", self.anchor())
    }

    /// Of a tile's delegation chain and its pull requests, the one the popover is not, while the
    /// tile shows it.
    pub(crate) fn other(&self, facts: &Facts) -> Option<What> {
        let other = match (&self.host, self.what) {
            (Host::Tile(_), What::Chain) => What::Details,
            (Host::Tile(_), What::Details) => What::Chain,
            _ => return None,
        };
        has(other, &self.session, facts).then_some(other)
    }
}

pub(crate) fn anchor(leaf: &str, what: What) -> String {
    format!("tile.{leaf}.{}", what.part())
}

/// The key of the delegation chain's trigger on the sidebar row keyed `row`.
pub(crate) fn row_anchor(row: &str) -> String {
    format!("{row}.chain")
}

enum Timer {
    Open(Pop, usize, f64),
    Close(f64),
}

/// The window's one header popover. The pointer opens it by resting on its piece and lets it
/// go by leaving both.
#[derive(Default)]
pub(crate) struct HeaderPop {
    pub(crate) open: Option<Pop>,
    timer: Option<Timer>,
    /// The row a popover listing several things has picked.
    pub(crate) picked: usize,
}

/// `piece` opens the popover of `what` when the pointer moves onto it, as the shelf's peek does,
/// so a popover closed under a still pointer stays closed, and lets it go once the pointer
/// leaves.
pub(crate) fn hovers(piece: El, leaf: &str, session: &str, what: What) -> El {
    hovers_on(piece, Host::Tile(leaf.to_owned()), session, what)
}

pub(crate) fn hovers_on(piece: El, host: Host, session: &str, what: What) -> El {
    let session = session.to_owned();
    let left = host.clone();
    piece
        .on_pointer_move_local(move |state: &mut State, _, tx: &mut Tx| {
            rest(state, &host, &session, what, tx)
        })
        .on_hover_change(move |state: &mut State, inside, tx: &mut Tx| {
            if !inside {
                leave(&mut state.window.desk.header_pop, &left, what, tx);
            }
        })
}

/// The pointer left the piece on `host` that shows `what`: it lets go of its own popover and
/// of its own pending open, never of another piece's.
fn leave(pop: &mut HeaderPop, host: &Host, what: What, tx: &mut Tx) {
    let here = |p: &Pop| p.on(host, what);
    if pop.open.as_ref().is_some_and(here) {
        close_soon(pop, tx);
    } else if matches!(&pop.timer, Some(Timer::Open(p, ..)) if here(p)) {
        pop.timer = None;
    }
}

fn rest(state: &mut State, host: &Host, session: &str, what: What, tx: &mut Tx) {
    let first = what.first_pick(session, &state.facts);
    let pop = &mut state.window.desk.header_pop;
    let here = |p: &Pop| p.on(host, what);
    if pop.open.as_ref().is_some_and(here) {
        if matches!(pop.timer, Some(Timer::Close(_))) {
            pop.timer = None;
        } else {
            tx.live_only();
        }
        return;
    }
    if pop.open.as_ref().is_some_and(|p| p.pinned)
        || matches!(&pop.timer, Some(Timer::Open(p, ..)) if here(p))
    {
        tx.live_only();
        return;
    }
    let opening = Pop {
        host: host.clone(),
        session: session.to_owned(),
        what,
        pinned: false,
    };
    match what.delays().0 {
        0.0 => {
            pop.timer = None;
            pop.picked = first;
            what.reveal(&opening, first, tx);
            pop.open = Some(opening);
        }
        delay => {
            let at = tx.clock() + delay;
            pop.timer = Some(Timer::Open(opening, first, at));
            tx.wake_at(at);
        }
    }
}

/// The pointer moved onto the open popover, or off it.
pub(crate) fn hover_popover(state: &mut State, inside: bool, tx: &mut Tx) {
    let pop = &mut state.window.desk.header_pop;
    if inside {
        pop.timer = None;
    } else {
        close_soon(pop, tx);
    }
}

fn close_soon(pop: &mut HeaderPop, tx: &mut Tx) {
    pop.timer = None;
    let Some(open) = pop.open.as_ref().filter(|p| !p.pinned) else {
        return;
    };
    let at = tx.clock() + open.what.delays().1;
    pop.timer = Some(Timer::Close(at));
    tx.wake_at(at);
}

/// A press on the piece pins its popover, which takes the keyboard, and a second press closes
/// it.
pub(crate) fn press(state: &mut State, leaf: &str, session: &str, what: What, tx: &mut Tx) {
    press_on(state, Host::Tile(leaf.to_owned()), session, what, tx);
}

pub(crate) fn press_on(state: &mut State, host: Host, session: &str, what: What, tx: &mut Tx) {
    if pinned_on(state, &host, what) {
        close(state);
    } else {
        pinned(state, host, session, what, tx);
    }
}

/// Whether the popover of `what` is pinned on `host`.
pub(crate) fn pinned_on(state: &State, host: &Host, what: What) -> bool {
    state
        .window
        .desk
        .header_pop
        .open
        .as_ref()
        .is_some_and(|p| p.pinned && p.on(host, what))
}

fn pinned(state: &mut State, host: Host, session: &str, what: What, tx: &mut Tx) {
    let first = what.first_pick(session, &state.facts);
    let open = Pop {
        host,
        session: session.to_owned(),
        what,
        pinned: true,
    };
    hold(state, open, first, tx);
}

fn hold(state: &mut State, open: Pop, picked: usize, tx: &mut Tx) {
    let pop = &mut state.window.desk.header_pop;
    pop.timer = None;
    pop.picked = picked;
    tx.focus(open.surface(), true);
    open.what.reveal(&open, picked, tx);
    pop.open = Some(open);
    crate::garden::follow(state, tx);
}

/// Gives the open chain or pull requests the keyboard, or closes them if pinned. Otherwise pins
/// the chain of the agent on screen, or its pull requests when it sits in no chain.
pub(crate) fn take_keyboard(state: &mut State, tx: &mut Tx) {
    let pop = &state.window.desk.header_pop;
    let open = pop
        .open
        .as_ref()
        .filter(|open| matches!(open.what, What::Chain | What::Details))
        .cloned();
    match open {
        Some(open) if open.pinned => close(state),
        Some(open) => {
            let picked = pop.picked;
            hold(
                state,
                Pop {
                    pinned: true,
                    ..open
                },
                picked,
                tx,
            );
        }
        None => {
            let Some(session) = state.window.on_screen(&state.facts) else {
                return;
            };
            let what = [What::Chain, What::Details]
                .into_iter()
                .find(|what| has(*what, &session, &state.facts));
            if let Some(what) = what {
                pin(state, &session, what, tx);
            }
        }
    }
}

/// How a sidebar row's agent stands in the open delegation chain or pull requests.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) struct Shown {
    /// The popover opened from this agent.
    pub(crate) opened: bool,
    /// The popover's pick is this agent.
    pub(crate) picked: bool,
}

/// The agents of the open delegation chain or pull requests that their sidebar rows mark: the one
/// it opened from, and the one it picks.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) struct Marked {
    opened: Option<String>,
    picked: Option<String>,
}

impl Marked {
    pub(crate) fn of(state: &State) -> Marked {
        let pop = &state.window.desk.header_pop;
        match &pop.open {
            Some(open) if open.what == What::Chain => Marked {
                opened: Some(open.session.clone()),
                picked: super::chain::agent_at(&open.session, pop.picked, &state.facts),
            },
            Some(open) if open.what == What::Details => Marked {
                opened: Some(open.session.clone()),
                picked: None,
            },
            _ => Marked::default(),
        }
    }

    pub(crate) fn shown(&self, session: &str) -> Shown {
        Shown {
            opened: self.opened.as_deref() == Some(session),
            picked: self.picked.as_deref() == Some(session),
        }
    }
}

/// Tab in a tile's pinned delegation chain or pull requests goes to the other one.
pub(crate) fn switch_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let m = ev.mods;
    if ev.key != Key::Tab || m.cmd || m.ctrl || m.alt {
        return false;
    }
    let Some(open) = state.window.desk.header_pop.open.clone() else {
        return false;
    };
    let Some(other) = open.other(&state.facts) else {
        return false;
    };
    pinned(state, open.host, &open.session, other, tx);
    true
}

/// The line under a delegation chain or pull requests that says what its keys do: `pinned` and
/// the key to the tile's other one while it has the keyboard, the key that gives it the keyboard
/// otherwise. `end` stands at its far end where no key does.
pub(crate) fn foot(
    state: &State,
    pop: &Pop,
    pinned: impl IntoIterator<Item = El>,
    end: Option<El>,
    t: &Tokens,
) -> Option<Actions> {
    let (keys, end) = if pop.pinned {
        let other = pop.other(&state.facts).map(|other| {
            let said = match other {
                What::Details => "pull requests",
                _ => "chain",
            };
            keys_hint(["⇥"], said, t)
        });
        (pinned.into_iter().collect::<Vec<_>>(), other.or(end))
    } else {
        let take = state
            .keymap
            .binding(&Action::SessionDetails)
            .map(|keys| keys_hint(state.keymap.keycaps(keys), "use the keyboard", t));
        (take.into_iter().collect(), end)
    };
    if keys.is_empty() && end.is_none() {
        return None;
    }
    let line = keys.into_iter().fold(Actions::new("keys"), Actions::push);
    Some(match end {
        Some(end) => line.then_end().push(end),
        None => line,
    })
}

/// Pins the popover of `what` on the tile on screen that shows `session`.
pub(crate) fn pin(state: &mut State, session: &str, what: What, tx: &mut Tx) {
    let leaf = state
        .facts
        .current_desktop()
        .and_then(|d| d.panes.iter().find(|p| p.session_id == session))
        .map(|p| p.pane_id.clone());
    if let Some(leaf) = leaf {
        pinned(state, Host::Tile(leaf), session, what, tx);
    }
}

pub(crate) fn close(state: &mut State) {
    let pop = &mut state.window.desk.header_pop;
    pop.timer = None;
    pop.open = None;
}

impl HeaderPop {
    /// Lets the popover go, and any it was about to open, once its tile leaves the current
    /// desktop or its agent stops having what it shows.
    pub(crate) fn follow(&mut self, facts: &attn_client::Facts) {
        let stands = |pop: &Pop| {
            let hosted = match &pop.host {
                Host::Tile(leaf) => facts
                    .current_desktop()
                    .is_some_and(|d| d.panes.iter().any(|p| &p.pane_id == leaf)),
                Host::Row(_) => true,
            };
            hosted
                && facts
                    .sessions
                    .iter()
                    .find(|s| s.id == pop.session)
                    .is_some_and(|s| pop.what.shown_for(s, facts))
        };
        if self.open.as_ref().is_some_and(|p| !stands(p)) {
            self.open = None;
        }
        if matches!(&self.timer, Some(Timer::Open(pop, ..)) if !stands(pop)) {
            self.timer = None;
        }
    }
}

/// Lets a sidebar row's popover go, and any it was about to open, once the sidebar stops
/// drawing its row. Returns whether it did.
pub(crate) fn follow_rows(state: &mut State) -> bool {
    let gone = |state: &State, pop: &Pop| match &pop.host {
        Host::Row(row) => !crate::sidebar::draws_row(state, row),
        Host::Tile(_) => false,
    };
    let pop = &state.window.desk.header_pop;
    let open_gone = pop.open.as_ref().is_some_and(|p| gone(state, p));
    let timer_gone = matches!(&pop.timer, Some(Timer::Open(p, ..)) if gone(state, p));
    let pop = &mut state.window.desk.header_pop;
    if open_gone {
        pop.open = None;
        pop.timer = None;
    }
    if timer_gone {
        pop.timer = None;
    }
    open_gone || timer_gone
}

/// Opens or closes the popover whose moment came. Returns whether it did.
pub(crate) fn expire(state: &mut State, now: f64) -> bool {
    let pop = &mut state.window.desk.header_pop;
    let due = match &pop.timer {
        Some(Timer::Open(.., at) | Timer::Close(at)) => *at <= now,
        None => false,
    };
    if !due {
        return false;
    }
    pop.open = match pop.timer.take() {
        Some(Timer::Open(open, first, _)) => {
            pop.picked = first;
            Some(open)
        }
        _ => None,
    };
    true
}
